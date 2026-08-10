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
	"github.com/go-spatial/tegola/maths"
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
func ParseKey(str string) (*Key, error) {
	var err error
	var key Key

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
	maxXYatZ := maths.Exp2(placeholder) - 1

	placeholder, err = strconv.ParseUint(zxy[1], 10, 32)
	if err != nil || placeholder > maxXYatZ {
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
	if err != nil || placeholder > maxXYatZ {
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
	MapName   string
	LayerName string
	Z         uint
	X         uint
	Y         uint
}

func (k Key) String() string {
	return filepath.Join(
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
