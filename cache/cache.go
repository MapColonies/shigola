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
// For builds the *outermost* cache. A composite cache must build its children
// through ForTier instead — see there for why the two are not the same call.
func For(cacheType string, config dict.Dicter) (Interface, error) {
	return ForTier(cacheType, config)
}

// ForTier returns a configured cache of the given type for use as one tier of a
// composite cache.
//
// It applies the read deadline, because a deadline is per-tier by design: every
// tier carries its own timeout_ms. It does not apply anything that is a
// property of the outermost cache only.
//
// The asymmetry with For is the point, and nothing enforces it: a composite
// cache written outside this repo that calls For for its children compiles and
// is silently broken.
func ForTier(cacheType string, config dict.Dicter) (Interface, error) {
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

	if timeout > 0 {
		built = newDeadlineCache(built, timeout)
	}

	return built, nil
}
