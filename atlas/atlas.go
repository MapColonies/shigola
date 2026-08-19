// Package atlas provides an abstraction for a collection of Maps.
package atlas

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/internal/observer"
	"github.com/MapColonies/shigola/observability"
	"github.com/go-spatial/geom/slippy"
)

var (
	simplifyGeometries    bool
	simplificationMaxZoom uint = 10
)

func init() {
	// TODO(arolek): the following env variable processing was pulled form the mvt package when
	// geometry processing was pulled out of the encoding package. This functionality could be
	// deprecated/removed as it's not well documented and is really a band aid to work around
	// some simplification issues. These concepts could just as easily live in the config file.
	options := strings.ToLower(env.Getenv("OPTIONS"))
	if strings.Contains(options, "dontsimplifygeo") {
		simplifyGeometries = false
		log.Debugf("simplification is disable")
	}

	if strings.Contains(options, "simplifymaxzoom=") {
		idx := strings.Index(options, "simplifymaxzoom=")
		idx += 16

		eidx := strings.IndexAny(options[idx:], ",.\t \n")
		if eidx == -1 {
			eidx = len(options)
		} else {
			eidx += idx
		}

		i, err := strconv.Atoi(options[idx:eidx])
		if err != nil {
			log.Errorf("invalid value for SimplifyMaxZoom (%v). using default (%v).", options[idx:eidx], simplificationMaxZoom)
			return
		}

		simplificationMaxZoom = uint(i + 1)

		log.Debugf("SimplifyMaxZoom set to (%v)", simplificationMaxZoom)
	}
}

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

		maps = append(maps, m)
	}

	return maps
}

// SeedMapTile will generate a tile and persist it to the
// configured cache backend
func (a *Atlas) SeedMapTile(ctx context.Context, m Map, z, x, y uint) error {

	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		return defaultAtlas.SeedMapTile(ctx, m, z, x, y)
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
	b, err := m.Encode(ctx, tile, nil)
	if err != nil {
		return err
	}

	// cache key
	key, err := cache.NewKey(m.TileGrid(), m.Name, "", z, x, y)
	if err != nil {
		return err
	}

	return a.cacher.Set(ctx, &key, b)
}

// PurgeMapTile will purge a map tile from the configured cache backend
func (a *Atlas) PurgeMapTile(ctx context.Context, m Map, tile *shigola.Tile) error {
	if a == nil {
		// Use the default Atlas if a, is nil. This way the empty value is
		// still useful.
		return defaultAtlas.PurgeMapTile(ctx, m, tile)
	}

	if len(m.Params) > 0 {
		return nil
	}

	if a.cacher == nil {
		return ErrMissingCache
	}

	// cache key
	key, err := cache.NewKey(m.TileGrid(), m.Name, "", tile.Z, tile.X, tile.Y)
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
// instrument itself (observability imports cache), and it is constructed before
// the observer exists, so this is the only place the two can meet.
func instrumentCache(o observability.Interface, c cache.Interface) cache.Interface {
	// Strip a previous instrumentation rather than wrapping it. Without this a
	// second SetObservability puts an instrumented cache inside another one and
	// double-counts everything — which is what the whole tree did until
	// prometheus's accessor was renamed to Original(), since the assertion
	// could never succeed.
	if w, ok := c.(observability.Cache); ok && w.IsObserver() {
		c = w.Original()
	}

	return o.InstrumentedCache(instrumentTiers(o, c, ""))
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
// observability wrappers, though still carrying their read deadlines. Each call
// re-derives from those rather than wrapping whatever is currently installed.
func instrumentTiers(o observability.Interface, c cache.Interface, path string) cache.Interface {
	to, ok := o.(observability.TieredCacheObserver)
	if !ok {
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
		wrapped[i] = to.InstrumentedTierCache(name, instrumentTiers(o, tier.Cache, name))
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
	// let's see if we have an observer set. If so, we need to wrap
	// the given cache with the observer.
	if a.observer != nil {
		c = a.observer.InstrumentedCache(c)
	}
	a.cacher = c
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
		a.cacher = instrumentCache(o, a.cacher)

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
func SeedMapTile(ctx context.Context, m Map, z, x, y uint) error {
	return defaultAtlas.SeedMapTile(ctx, m, z, x, y)
}

// PurgeMapTile will purge a map tile from the configured cache backend
// for the defaultAtlas
func PurgeMapTile(ctx context.Context, m Map, tile *shigola.Tile) error {
	return defaultAtlas.PurgeMapTile(ctx, m, tile)
}

// CacheWritePool returns the defaultAtlas cache's detached-write pool, if it
// has one
func CacheWritePool() *cache.WritePool { return defaultAtlas.CacheWritePool() }

// SetObservability sets the observability backend for the defaultAtlas
func SetObservability(o observability.Interface) { defaultAtlas.SetObservability(o) }

func StartSubProcesses() { defaultAtlas.StartSubProcesses() }
