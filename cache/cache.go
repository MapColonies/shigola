package cache

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/tms"
)

// Interface defines a cache back end
type Interface interface {
	Get(ctx context.Context, key *Key) (val []byte, hit bool, err error)
	Set(ctx context.Context, key *Key, val []byte) error
	Purge(ctx context.Context, key *Key) error
}

// Wrapped Cache are for cache backend that wrap other cache backends
// Original will return the first cache backend to be wrapped
type Wrapped interface {
	Original() Interface
}

// NamedTier pairs one tier of a composite cache with its name.
//
// Names are public API. They appear in the tier metric label and in
// `tegola cache seed --cache-tiers`, which means they end up in dashboards,
// alerts and cron jobs — renaming a tier, or reordering layers such that a
// derived collision suffix shifts, silently breaks both.
type NamedTier struct {
	Name  string
	Cache Interface
}

// Tiered is satisfied by composite caches that want their tiers instrumented
// individually. It is optional: asserted for, never required.
//
// It exists because cache cannot import observability — observability imports
// cache — so a chain cannot instrument itself. atlas descends through this
// interface instead, and both of the decorators For applies forward it, or the
// descent stops at the decorator and every per-tier metric silently disappears.
type Tiered interface {
	// Tiers returns this chain's own tiers in read order, free of
	// observability wrappers but still carrying their read deadlines.
	// Instrumentation goes *outside* the deadline, so a timed-out read still
	// lands in the latency histogram.
	Tiers() []NamedTier

	// WithTiers returns a new cache over the given tiers, still decorated.
	// A new value rather than a mutation, so instrumentation is idempotent:
	// the chain keeps its original tiers and re-derives from them each time
	// rather than wrapping whatever is currently installed.
	WithTiers(tiers []Interface) Interface
}

// WritePoolUser is implemented by caches that detach writes of their own —
// today only a composite cache, for promotion, which happens inside Get with
// the client still waiting.
//
// The pool is injected after construction rather than passed to the
// constructor because InitFunc takes only a config dict, and widening it would
// break every in-tree backend and every out-of-tree implementation. Injection
// happens inside For/ForTier, before the cache is visible to any caller, so
// there is no window in which a chain exists without its pool.
//
// An implementer must forward the pool to any tier that wants one, which is
// what makes a nested chain share the parent's pool: one pool per constructed
// tree, not per chain.
type WritePoolUser interface {
	UseWritePool(*WritePool)
}

// WritePoolHolder lets atlas reach the pool to register a collector over its
// Stats. Implemented by the detachment decorator.
type WritePoolHolder interface {
	WritePool() *WritePool
}

// ChainStats crosses the cache→observability import cycle the same way
// WritePoolStats does.
//
// Promotions need their own counter because the per-tier wrapper labels every
// tier write sub_command="set" and cannot tell a promotion from an ordinary
// chain write. Differencing tier counters instead (tier0_sets − tier1_sets) is
// roughly right on the serve path and wrong whenever write tiers are bounded or
// promotions are dropped — and it encodes a chain invariant into a dashboard
// query, where nothing notices when it stops holding.
type ChainStats struct {
	// Promotions counts promotions that completed.
	Promotions uint64
	// PromotionsDropped counts promotions the write pool refused at admission.
	// A rising value with a falling hot-tier hit ratio is the signature of a
	// hot tier that has quietly stopped filling.
	PromotionsDropped uint64
}

// ChainStatsHolder is implemented by composite caches that promote.
type ChainStatsHolder interface {
	ChainStats() ChainStats
}

// unwrap peels one layer off c: an observability wrapper, or one of the
// decorators For applies.
//
// The decorators do not implement Wrapped, deliberately — that is what stops
// SetObservability stripping them — so only this package can see past them.
// Everything that needs to reach through the stack goes through here rather
// than teaching another package the stack's shape.
func unwrap(c Interface) (Interface, bool) {
	switch v := c.(type) {
	case *detachedCache:
		return v.cache, true
	case *tieredDetachedCache:
		return v.cache, true
	case *deadlineCache:
		return v.cache, true
	case *tieredDeadlineCache:
		return v.cache, true
	case Wrapped:
		return v.Original(), true
	}

	return nil, false
}

// WritePoolOf returns the detached-write pool behind c, or nil if it has none.
//
// The pool is held by the detachment decorator, which sits *inside* the
// observability wrapper — so a direct type assertion on the configured cache
// finds nothing the moment an observer is configured.
func WritePoolOf(c Interface) *WritePool {
	for c != nil {
		if h, ok := c.(WritePoolHolder); ok {
			return h.WritePool()
		}

		next, ok := unwrap(c)
		if !ok {
			return nil
		}
		c = next
	}

	return nil
}

// TieredOf returns the composite cache behind c, if there is one.
//
// The decorators forward Tiered, but the observability wrapper does not — so a
// caller that wants the tier list has to reach through it. Which callers do:
// `tegola cache seed` resolves --cache-tiers against the tree, and by then the
// configured cache has already been instrumented.
func TieredOf(c Interface) (Tiered, bool) {
	for c != nil {
		if t, ok := c.(Tiered); ok {
			return t, true
		}

		next, ok := unwrap(c)
		if !ok {
			return nil, false
		}
		c = next
	}

	return nil, false
}

// ChainStatsOf returns the promotion-statistics source behind c, if there is
// one. A live handle rather than a snapshot, because a metrics collector has to
// re-read it on every scrape.
func ChainStatsOf(c Interface) (ChainStatsHolder, bool) {
	for c != nil {
		if h, ok := c.(ChainStatsHolder); ok {
			return h, true
		}

		next, ok := unwrap(c)
		if !ok {
			return nil, false
		}
		c = next
	}

	return nil, false
}

// InjectWritePool gives c the pool if it wants one, looking through the
// decorators this package applies on the way.
//
// Exported because a composite cache forwarding the pool to its tiers lives in
// another package and cannot see those decorators itself.
func InjectWritePool(c Interface, pool *WritePool) {
	if pool == nil || c == nil {
		return
	}

	switch v := c.(type) {
	case WritePoolUser:
		v.UseWritePool(pool)
	case *deadlineCache:
		InjectWritePool(v.cache, pool)
	case *tieredDeadlineCache:
		InjectWritePool(v.cache, pool)
	}
}

// ParseKey will parse a string in the format /:map/:layer/:z/:x/:y into a Key struct. The :layer value is optional
// ParseKey also supports other OS delimiters (i.e. Windows - "\")
//
// The path carries no tileMatrixSetId — tegola's native routes do not name a
// grid — so the tile is read as a WebMercatorQuad one. Use ParseKeyForGrid when
// the grid is known.
func ParseKey(str string) (*Key, error) {
	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		return nil, err
	}

	return ParseKeyForGrid(str, grid)
}

// ParseKeyForGrid will parse a key, validate z/x/y against grid's matrix at that
// zoom, and record the grid on the returned Key.
func ParseKeyForGrid(str string, grid *tms.TileMatrixSet) (*Key, error) {
	var err error
	var key Key

	key.TileMatrixSetId = grid.ID()

	// convert to all slashes to forward slashes. without this reading from certain OSes (i.e. windows)
	// will fail our keyParts check since it uses backslashes.
	str = filepath.ToSlash(str)

	// remove the base-path and the first slash, then split the parts
	keyParts := strings.Split(strings.TrimLeft(str, "/"), "/")

	// we're expecting a z/x/y scheme
	if len(keyParts) < 3 || len(keyParts) > 5 {
		err = ErrInvalidFileKeyParts{
			path:          str,
			keyPartsCount: len(keyParts),
		}

		log.Println(err.Error())
		return nil, err
	}

	var zxy []string

	switch len(keyParts) {
	case 5: // map, layer, z, x, y
		key.MapName = keyParts[0]
		key.LayerName = keyParts[1]
		zxy = keyParts[2:]
	case 4: // map, z, x, y
		key.MapName = keyParts[0]
		zxy = keyParts[1:]
	case 3: // z, x, y
		zxy = keyParts
	}

	// parse our URL vals into integers
	var placeholder uint64
	placeholder, err = strconv.ParseUint(zxy[0], 10, 32)
	if err != nil || placeholder > tegola.MaxZ {
		err = ErrInvalidFileKey{
			path: str,
			key:  "Z",
			val:  zxy[0],
		}

		log.Printf("cache: invalid file key: %s", err.Error())
		return nil, err
	}

	key.Z = uint(placeholder)

	// x and y are bounded independently: a grid need not be a square pyramid.
	cols, rows, err := grid.MatrixSize(int(key.Z))
	if err != nil {
		return nil, err
	}
	maxXatZ := uint64(cols - 1)
	maxYatZ := uint64(rows - 1)

	placeholder, err = strconv.ParseUint(zxy[1], 10, 32)
	if err != nil || placeholder > maxXatZ {
		err = ErrInvalidFileKey{
			path: str,
			key:  "X",
			val:  zxy[1],
		}

		log.Printf("cache: invalid file key: %s", err.Error())
		return nil, err
	}

	key.X = uint(placeholder)

	// trim the extension if it exists
	yParts := strings.Split(zxy[2], ".")
	placeholder, err = strconv.ParseUint(yParts[0], 10, 64)
	if err != nil || placeholder > maxYatZ {
		err = ErrInvalidFileKey{
			path: str,
			key:  "Y",
			val:  zxy[2],
		}

		log.Printf("cache: invalid file key: %s", err.Error())
		return nil, err
	}
	key.Y = uint(placeholder)

	return &key, nil
}

type Key struct {
	// TileMatrixSetId names the grid the tile was cut in. Without it the 2:1
	// WorldCRS84Quad grid and the square WebMercatorQuad grid collide at equal
	// z/x/y and one grid's tiles are served for the other's (ADR-0007).
	//
	// It leads the key so that every cache backend — all of which build their
	// path or redis key from String() — partitions by grid, and so that a purge
	// can address one grid's tiles as a subtree.
	//
	// An unset value means WebMercatorQuad, the grid tegola served before this
	// field existed.
	TileMatrixSetId string
	MapName         string
	LayerName       string
	Z               uint
	X               uint
	Y               uint
}

func (k Key) String() string {
	tileMatrixSetId := k.TileMatrixSetId
	if tileMatrixSetId == "" {
		tileMatrixSetId = tms.WebMercatorQuad
	}

	return filepath.Join(
		tileMatrixSetId,
		k.MapName,
		k.LayerName,
		strconv.FormatUint(uint64(k.Z), 10),
		strconv.FormatUint(uint64(k.X), 10),
		strconv.FormatUint(uint64(k.Y), 10))
}

// InitFunc initialize a cache given a config map.
// The InitFunc should validate the config map, and report any errors.
// This is called by the For function.
type InitFunc func(dict.Dicter) (Interface, error)

var cache map[string]InitFunc

// Register is called by the init functions of the cache.
func Register(cacheType string, init InitFunc) error {
	if cache == nil {
		cache = make(map[string]InitFunc)
	}

	if _, ok := cache[cacheType]; ok {
		return fmt.Errorf("Cache (%v) already exists", cacheType)

	}
	cache[cacheType] = init

	return nil
}

// Registered returns the cache's that have been registered.
func Registered() (c []string) {
	for k := range cache {
		c = append(c, k)
	}
	sort.Strings(c)
	return c
}

// For function returns a configured cache of the given type, provided the correct config map.
//
// For builds the *outermost* cache: it creates the write pool, applies the read
// deadline and applies the detachment decorator. A composite cache must build
// its children through ForTier instead — see there for why the two are not the
// same call.
func For(cacheType string, config dict.Dicter) (Interface, error) {
	pool := NewWritePool(detachedWriteSlots, detachedWriteTimeout)

	c, err := ForTier(cacheType, config, pool)
	if err != nil {
		return nil, err
	}

	return newDetachedCache(c, pool), nil
}

// ForTier returns a configured cache of the given type for use as one tier of a
// composite cache.
//
// It applies the read deadline, because a deadline is per-tier by design: every
// tier carries its own timeout_ms. It does not detach, because detachment is a
// property of the outermost cache only — detaching per tier would make a
// composite Set return before any tier write, leaving the joined error nothing
// to join and silently under-seeding.
//
// The asymmetry with For is the point, and nothing enforces it: a composite
// cache written outside this repo that calls For for its children compiles and
// is silently broken.
//
// pool may be nil, which a composite cache passes when building its own tiers:
// it has no pool at construction and forwards the one it is later given.
func ForTier(cacheType string, config dict.Dicter, pool *WritePool) (Interface, error) {
	if cache == nil {
		return nil, fmt.Errorf("No cache backends registered.")
	}

	c, ok := cache[cacheType]
	if !ok {
		return nil, fmt.Errorf("No cache backends registered by the cache type: (%v)", cacheType)
	}

	// Before construction, so a malformed timeout_ms fails without first
	// opening connections to the backend.
	timeout, err := timeoutFor(config)
	if err != nil {
		return nil, err
	}

	built, err := c(config)
	if err != nil {
		return nil, err
	}

	// Before the decorator goes on, so the injection sees the cache itself.
	InjectWritePool(built, pool)

	if timeout > 0 {
		built = newDeadlineCache(built, timeout)
	}

	return built, nil
}
