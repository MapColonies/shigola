// Package atlas provides an abstraction for a collection of Maps.
package atlas

import (
	"context"
	"sync"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/internal/observer"
	"github.com/MapColonies/shigola/observability"
	"github.com/MapColonies/shigola/tms"
	"github.com/MapColonies/shigola/tracing"
	"github.com/go-spatial/geom/slippy"
)

// defaultAtlas is instantiated for convenience
var defaultAtlas = &Atlas{}

const (
	// MaxZoom will not render tile beyond this zoom level
	MaxZoom = shigola.MaxZ
)

// Atlas holds a collection of maps.
// If the pointer to Atlas is nil, it will make use of the default atlas; as the container for maps.
// This is equivalent to using the functions in the package.
// An Atlas is safe to use concurrently.
type Atlas struct {
	// for managing current access to the map container
	sync.RWMutex
	// hold maps
	maps map[string]Map
	// holds a reference to the cache backend
	cacher cache.Interface

	// holds a reference to the observer backend
	observer observability.Interface

	// holds a reference to the tracing backend.
	//
	// Separate from observer, not folded into it: one is a metrics
	// abstraction and the other is not, they are configured and switched on
	// independently, and a build with only one of them configured has to
	// behave as though the other did not exist (MAPCO-11497).
	tracer tracing.Interface

	// publishBuildInfo indicates if we should publish the build info on change of observer
	// this is set by calling PublishBuildInfo, which will publish
	// the build info on the observer and insure changes to observer
	// also publishes the build info.
	publishBuildInfo bool

	// cacheCollectorsRegistered stops the write-pool and promotion collectors
	// being registered twice. Unlike the cache wrappers, a collector cannot be
	// unwrapped and re-derived — prometheus simply rejects the duplicate.
	cacheCollectorsRegistered bool
}

// AllMaps returns a slice of all maps contained in the Atlas so far.
func (a *Atlas) AllMaps() []Map {

	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		return defaultAtlas.AllMaps()
	}

	a.RLock()
	defer a.RUnlock()

	var maps []Map
	for i := range a.maps {
		m := a.maps[i]
		// make an explicit copy of the layers
		layers := make([]Layer, len(m.Layers))
		copy(layers, m.Layers)
		m.Layers = layers
		m.tracer = a.tracer

		maps = append(maps, m)
	}

	return maps
}

// SeedMapTile will generate a tile in grid and persist it to the configured
// cache backend, under grid's key.
//
// grid is required: one argument feeds both the encode and the key, so the
// bytes and the name they are filed under cannot come apart.
func (a *Atlas) SeedMapTile(ctx context.Context, m Map, grid *tms.TileMatrixSet, z, x, y uint) error {

	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		return defaultAtlas.SeedMapTile(ctx, m, grid, z, x, y)
	}

	if grid == nil {
		return ErrNilGrid
	}

	if len(m.Params) > 0 {
		return nil
	}

	ctx = context.WithValue(ctx, observability.ObserveVarMapName, m.Name)
	// confirm we have a cache backend
	if a.cacher == nil {
		return ErrMissingCache
	}

	tile := slippy.Tile{Z: slippy.Zoom(z), X: x, Y: y}

	// encode the tile
	b, err := m.Encode(ctx, grid, tile, nil)
	if err != nil {
		return err
	}

	// cache key
	key, err := cache.NewKey(grid, m.Name, "", z, x, y)
	if err != nil {
		return err
	}

	return a.cacher.Set(ctx, &key, b)
}

// PurgeMapTile will purge a map tile, cut in grid, from the configured cache
// backend.
//
// grid is required, and must be the one the tile was seeded in: it is the first
// segment of the key, so purging with the wrong one removes another scheme's
// tile and leaves the intended one in place.
func (a *Atlas) PurgeMapTile(ctx context.Context, m Map, grid *tms.TileMatrixSet, tile *shigola.Tile) error {
	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		return defaultAtlas.PurgeMapTile(ctx, m, grid, tile)
	}

	// Checked here rather than left to cache.NewKey, which reports its own
	// cache.ErrNilGrid: one mistake should not have two names depending on
	// which of these three seams the caller reached for.
	if grid == nil {
		return ErrNilGrid
	}

	if len(m.Params) > 0 {
		return nil
	}

	if a.cacher == nil {
		return ErrMissingCache
	}

	// cache key
	key, err := cache.NewKey(grid, m.Name, "", tile.Z, tile.X, tile.Y)
	if err != nil {
		return err
	}

	return a.cacher.Purge(ctx, &key)
}

// Map looks up a Map by name and returns a copy of the Map
func (a *Atlas) Map(mapName string) (Map, error) {
	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		return defaultAtlas.Map(mapName)
	}

	a.RLock()
	defer a.RUnlock()

	m, ok := a.maps[mapName]
	if !ok {
		return Map{}, ErrMapNotFound{
			Name: mapName,
		}
	}

	// make an explicit copy of the layers
	layers := make([]Layer, len(m.Layers))
	copy(layers, m.Layers)
	m.Layers = layers

	// Handed out here rather than stored by AddMap: maps are registered before
	// either backend is configured, so a tracer captured at AddMap time would
	// always be the no-op. A Map is a value that gets copied and filtered on
	// the way to Encode, and the copies carry this with them.
	m.tracer = a.tracer

	return m, nil
}

// AddMap registers a map by name. if the map already exists it will be overwritten
func (a *Atlas) AddMap(m Map) {
	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		defaultAtlas.AddMap(m)
		return
	}
	a.Lock()
	defer a.Unlock()

	if a.maps == nil {
		a.maps = map[string]Map{}
	}

	a.maps[m.Name] = m
}

// GetCache returns the registered cache if one is registered, otherwise nil
func (a *Atlas) GetCache() cache.Interface {
	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		return defaultAtlas.GetCache()
	}
	return a.cacher
}

// CacheWritePool returns the detached-write pool of the configured cache, or
// nil if there is none.
func (a *Atlas) CacheWritePool() *cache.WritePool {
	if a == nil {
		return defaultAtlas.CacheWritePool()
	}

	return cache.WritePoolOf(a.GetCache())
}

// instrumentCache wraps the whole cache, and descends into a composite one so
// each tier is instrumented under its own label.
//
// Instrumentation applied only from the outside yields a single hits_total for
// the entire chain, where "hit" means "hit in some tier" — which answers none
// of the questions the layered cache exists to answer. The chain cannot
// instrument itself — observability and tracing both import cache, so the
// dependency cannot run the other way — and it is constructed before either
// backend exists, so this is the only place the three can meet.
func instrumentCache(o observability.Interface, t tracing.Interface, c cache.Interface) cache.Interface {
	// Strip a previous instrumentation rather than wrapping it. Without this a
	// second SetObservability puts an instrumented cache inside another one and
	// double-counts everything — which is what the whole tree did until
	// prometheus's accessor was renamed to Original(), since the assertion
	// could never succeed.
	c = stripInstrumentation(c)

	c = instrumentTiers(o, t, c, "")

	if o != nil {
		c = o.InstrumentedCache(c)
	}

	// Spans go on *outside* the metric wrapper, so that by the time the
	// metric wrapper takes its duration observation the operation's span is
	// already the active one in ctx. That is the ordering trace exemplars
	// need (MAPCO-11496): an observation made before the span exists can only
	// be exemplared against the request's span, not against the tier read it
	// actually measured. Costs nothing today and cannot be reordered later
	// without silently coarsening every exemplar.
	if t != nil {
		c = t.InstrumentedCache(c)
	}

	return c
}

// stripInstrumentation peels every instrumentation wrapper off c, leaving the
// cache as it was constructed.
//
// Both kinds have to come off, and in either order they were applied: metrics
// and tracing are installed by separate calls, so a cache reaching here can
// carry one, the other, or both. Missing one would nest a second wrapper
// inside it — which for metrics means double counting and for tracing means
// two spans per read, each claiming to be the whole operation.
func stripInstrumentation(c cache.Interface) cache.Interface {
	for {
		switch w := c.(type) {
		case tracing.Cache:
			if !w.IsTracer() {
				return c
			}
			c = w.Original()
		case observability.Cache:
			if !w.IsObserver() {
				return c
			}
			c = w.Original()
		default:
			return c
		}
	}
}

// instrumentTiers returns c with each of its tiers wrapped in per-tier
// instrumentation, recursively, with names qualified by path.
//
// The recursion is not thoroughness. Without it a nested chain reports one
// aggregate label and its inner tiers are invisible — precisely the blindness
// this exists to remove — and two nested chains each holding a `redis` would
// share a label, silently making both series wrong.
//
// Idempotent, because Tiers() returns the chain's *original* tiers: free of
// both instrumentation wrappers, though still carrying their read deadlines.
// Each call re-derives from those rather than wrapping whatever is currently
// installed.
func instrumentTiers(o observability.Interface, t tracing.Interface, c cache.Interface, path string) cache.Interface {
	to, _ := o.(observability.TieredCacheObserver)
	if to == nil && t == nil {
		return c
	}

	tiered, ok := c.(cache.Tiered)
	if !ok {
		return c
	}

	tiers := tiered.Tiers()
	wrapped := make([]cache.Interface, len(tiers))

	for i, tier := range tiers {
		name := tier.Name
		if path != "" {
			name = path + "/" + name
		}

		// Depth first: a nested chain has its own tiers instrumented before it
		// is itself wrapped.
		inner := instrumentTiers(o, t, tier.Cache, name)

		if to != nil {
			inner = to.InstrumentedTierCache(name, inner)
		}

		// Outside the metric wrapper, for the reason instrumentCache gives.
		if t != nil {
			inner = t.InstrumentedTierCache(name, inner)
		}

		wrapped[i] = inner
	}

	// WithTiers re-wraps, so the decorators For applied survive this.
	return tiered.WithTiers(wrapped)
}

// SetCache sets the cache backend
func (a *Atlas) SetCache(c cache.Interface) {
	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		defaultAtlas.SetCache(c)
		return
	}
	// Instrument it with whatever backends are already set, through the same
	// path SetObservability and SetTracing use, rather than the whole-cache
	// wrapper this applied on its own until MAPCO-11497.
	//
	// In shigola's own startup this changes nothing: root.go calls SetCache
	// before either setter, so the shallow wrapper was always immediately
	// re-derived. It does change one case, deliberately — an embedding caller
	// who configures a backend *before* handing over the cache now gets per-
	// tier metrics and tier spans, where the shallow path silently gave them
	// only the whole-cache family. That is the behaviour they should always
	// have had, and unlike the shallow path this one is idempotent.
	a.cacher = instrumentCache(a.observer, a.tracer, c)
}

// SetObservability will set the observability backend
func (a *Atlas) SetObservability(o observability.Interface) {
	if a == nil {
		defaultAtlas.SetObservability(o)
		return
	}
	if a.observer != nil {
		a.observer.Shutdown()
	}
	a.observer = o
	if a.publishBuildInfo {
		a.observer.Init()
	}
	if a.cacher != nil {
		a.cacher = instrumentCache(o, a.tracer, a.cacher)

		// Registered once per atlas. The collectors are process-wide by
		// nature — one cache, one pool — and prometheus rejects a second
		// registration of the same metric name outright, so re-registering on
		// a second SetObservability would panic rather than refresh anything.
		if !a.cacheCollectorsRegistered {
			if collectors := cacheCollectors(a.cacher); len(collectors) > 0 {
				o.MustRegister(collectors...)
				a.cacheCollectorsRegistered = true
			}
		}
	}
	for _, aMap := range a.maps {

		collectors, err := aMap.Collectors("tegola", o.CollectorConfig)
		if err != nil {
			log.Errorf("failed to register collector for map: %v ignoring", aMap.Name)
			continue
		}
		o.MustRegister(collectors...)
	}
}

// SetTracing sets the tracing backend.
//
// It re-derives cache instrumentation the same way SetObservability does, and
// for the same reason: the chain is built before either backend exists, so this
// is the only place the two can meet. Order between the two setters does not
// matter — instrumentCache strips whatever is installed and rebuilds from the
// original cache — which is what lets them stay independently configurable.
func (a *Atlas) SetTracing(t tracing.Interface) {
	if a == nil {
		defaultAtlas.SetTracing(t)
		return
	}

	a.tracer = t

	if a.cacher != nil {
		a.cacher = instrumentCache(a.observer, t, a.cacher)
	}
}

func (a *Atlas) Observer() observability.Interface {
	if a == nil {
		return defaultAtlas.Observer()
	}
	if a.observer == nil {
		return nil
	}
	if _, ok := a.observer.(observer.Null); ok {
		return nil
	}
	return a.observer
}

// Tracing returns the tracing backend, never nil.
//
// Unlike Observer, which reports nil for the null backend so that callers can
// skip work, this hands back the no-op: every caller here starts spans through
// it rather than deciding whether to, and a nil check at each of those sites
// would be the branch the null backend exists to remove.
func (a *Atlas) Tracing() tracing.Interface {
	if a == nil {
		return defaultAtlas.Tracing()
	}
	if a.tracer == nil {
		return tracing.NullTracer
	}
	return a.tracer
}

func (a *Atlas) StartSubProcesses() {
	if a == nil {
		defaultAtlas.StartSubProcesses()
		return
	}
	o := a.Observer()
	if o == nil {
		return
	}
	a.publishBuildInfo = true
	o.Init()
}

// AllMaps returns all registered maps in defaultAtlas
func AllMaps() []Map {
	return defaultAtlas.AllMaps()
}

// GetMap returns a copy of the a map by name from defaultAtlas. if the map does not exist it will return an error
func GetMap(mapName string) (Map, error) {
	return defaultAtlas.Map(mapName)
}

// AddMap registers a map by name with defaultAtlas. if the map already exists it will be overwritten
func AddMap(m Map) {
	defaultAtlas.AddMap(m)
}

// GetCache returns the registered cache for defaultAtlas, if one is registered, otherwise nil
func GetCache() cache.Interface {
	return defaultAtlas.GetCache()
}

// SetCache sets the cache backend for defaultAtlas
func SetCache(c cache.Interface) {
	defaultAtlas.SetCache(c)
}

// SeedMapTile will generate a tile and persist it to the
// configured cache backend for the defaultAtlas
func SeedMapTile(ctx context.Context, m Map, grid *tms.TileMatrixSet, z, x, y uint) error {
	return defaultAtlas.SeedMapTile(ctx, m, grid, z, x, y)
}

// PurgeMapTile will purge a map tile from the configured cache backend
// for the defaultAtlas
func PurgeMapTile(ctx context.Context, m Map, grid *tms.TileMatrixSet, tile *shigola.Tile) error {
	return defaultAtlas.PurgeMapTile(ctx, m, grid, tile)
}

// CacheWritePool returns the defaultAtlas cache's detached-write pool, if it
// has one
func CacheWritePool() *cache.WritePool { return defaultAtlas.CacheWritePool() }

// SetObservability sets the observability backend for the defaultAtlas
func SetObservability(o observability.Interface) { defaultAtlas.SetObservability(o) }

// SetTracing sets the tracing backend for the defaultAtlas
func SetTracing(t tracing.Interface) { defaultAtlas.SetTracing(t) }

// Tracing returns the defaultAtlas tracing backend, never nil.
func Tracing() tracing.Interface { return defaultAtlas.Tracing() }

func StartSubProcesses() { defaultAtlas.StartSubProcesses() }
