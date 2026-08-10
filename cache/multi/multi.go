// Package multi implements an ordered chain of cache backends.
//
// Reads walk the tiers in declaration order; a hit in a later tier is returned
// immediately and promoted into the earlier ones. Writes fan out concurrently
// and report a joined error if any target failed. Purges run in reverse — the
// durable tier first — because that ordering is what closes the re-promotion
// race, and is therefore a correctness property rather than a detail.
//
// The chain is generic: any registered cache type can occupy any position, in
// any number, at any nesting depth. `redis` in front of `s3` is the motivating
// deployment and the worked example, not a constraint. Note though that the
// *risk analysis* around it is not generic — it assumes early tiers are fast
// with bounded client timeouts and the last tier is durable and unbounded, and
// nothing here validates either half.
package multi

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/internal/log"
)

const CacheType = "multi"

const (
	// ConfigKeyLayers is the ordered list of tiers. Declaration order is read
	// order: a TOML list is already ordered, and a `priority` key would be a
	// second source of truth.
	ConfigKeyLayers = "layers"
	// ConfigKeyPromoteOnHit turns read-through promotion off. Defaults to true,
	// because promotion is the behaviour the chain exists to provide and
	// type = "multi" is new, so no existing config changes meaning.
	ConfigKeyPromoteOnHit = "promote_on_hit"

	// per-layer keys the chain itself reads. Everything else is passed through
	// to the backend untouched.
	ConfigKeyType    = "type"
	ConfigKeyName    = "name"
	ConfigKeyMaxZoom = "max_zoom"
)

// ChainStats crosses the cache→observability import cycle as a plain snapshot,
// the same way WritePoolStats does. The per-tier wrapper labels every tier
// write sub_command="set" and cannot tell a promotion from an ordinary chain
// write, so promotions need their own counter.
type ChainStats struct {
	Promotions        uint64
	PromotionsDropped uint64
}

type chainStats struct {
	promotions        atomic.Uint64
	promotionsDropped atomic.Uint64
}

// Cache is an ordered chain of cache backends.
type Cache struct {
	// original is the chain as constructed: immutable after New, and free of
	// observability wrappers. Instrumentation re-derives from it on every
	// SetObservability rather than wrapping whatever is currently installed,
	// which is what makes a second call idempotent instead of double-counting.
	original []cache.NamedTier
	// active is what Get/Set/Purge actually call, parallel to original.
	active []cache.Interface

	promoteOnHit bool

	// stats is shared with every chain derived through WithTiers: those are
	// re-instrumentations of the same logical chain, not new ones.
	stats *chainStats
}

var (
	_ cache.Interface = (*Cache)(nil)
	_ cache.Tiered    = (*Cache)(nil)
)

func init() {
	cache.Register(CacheType, New) //nolint:errcheck
}

// New builds a chain from config. It is the cache.InitFunc registered for
// type = "multi".
func New(config dict.Dicter) (cache.Interface, error) {
	layers, err := config.MapSlice(ConfigKeyLayers)
	if err != nil {
		return nil, err
	}
	if len(layers) == 0 {
		return nil, ErrNoLayers
	}
	if len(layers) == 1 {
		log.Warnf("cache/multi: configured with a single layer. harmless, but almost certainly a config mistake")
	}

	promote := true
	if promote, err = config.Bool(ConfigKeyPromoteOnHit, &promote); err != nil {
		return nil, err
	}

	tiers := make([]cache.NamedTier, len(layers))
	names := make(map[string]bool, len(layers))
	// derived tracks how many tiers of each type have been seen, so a second
	// `s3` becomes "s3#2".
	derived := make(map[string]int, len(layers))
	maxZooms := make([]uint, len(layers))

	for i, layer := range layers {
		cacheType, err := layer.String(ConfigKeyType, nil)
		if err != nil {
			return nil, err
		}

		name, err := tierName(layer, cacheType, derived)
		if err != nil {
			return nil, err
		}
		if names[name] {
			return nil, ErrDuplicateTierName{Name: name}
		}
		names[name] = true

		defaultMaxZoom := uint(tegola.MaxZ)
		if maxZooms[i], err = layer.Uint(ConfigKeyMaxZoom, &defaultMaxZoom); err != nil {
			return nil, err
		}

		// ForTier, not For: a deadline is per-tier, detachment is a property of
		// the outermost cache only. Building children through For would detach
		// every tier independently, so the fan-out below would return before
		// any tier write and the joined error would have nothing to join.
		tier, err := cache.ForTier(cacheType, layer)
		if err != nil {
			return nil, err
		}

		tiers[i] = cache.NamedTier{Name: name, Cache: tier}
	}

	warnOnMaxZoomInversion(tiers, maxZooms)

	return NewChain(tiers, promote)
}

// NewChain builds a chain over already-constructed tiers. Tests use it to get a
// chain without going through config.
func NewChain(tiers []cache.NamedTier, promote bool) (*Cache, error) {
	if len(tiers) == 0 {
		return nil, ErrNoLayers
	}

	c := &Cache{
		original:     make([]cache.NamedTier, len(tiers)),
		active:       make([]cache.Interface, len(tiers)),
		promoteOnHit: promote,
		stats:        &chainStats{},
	}
	copy(c.original, tiers)
	for i := range tiers {
		c.active[i] = tiers[i].Cache
	}

	return c, nil
}

// tierName resolves a tier's name: the explicit `name` key if present,
// otherwise the type with an index suffix on collision.
func tierName(layer dict.Dicter, cacheType string, derived map[string]int) (string, error) {
	if v, ok := layer.Interface(ConfigKeyName); ok && v != nil {
		return layer.String(ConfigKeyName, nil)
	}

	derived[cacheType]++
	if n := derived[cacheType]; n > 1 {
		return cacheType + "#" + strconv.Itoa(n), nil
	}

	return cacheType, nil
}

// warnOnMaxZoomInversion reports a tier that cannot store what an earlier tier
// would promote into it. Every backend no-ops Set above its own max_zoom, so
// promotion into such a tier succeeds while storing nothing and those tiles
// re-promote on every request — silently, and forever.
func warnOnMaxZoomInversion(tiers []cache.NamedTier, maxZooms []uint) {
	for i := range maxZooms {
		for j := i + 1; j < len(maxZooms); j++ {
			if maxZooms[i] < maxZooms[j] {
				log.Warnf(
					"cache/multi: tier (%v) max_zoom (%v) is below the later tier (%v) max_zoom (%v). "+
						"promotion into (%v) above zoom %v will succeed while storing nothing, so those tiles re-promote on every request",
					tiers[i].Name, maxZooms[i], tiers[j].Name, maxZooms[j], tiers[i].Name, maxZooms[i],
				)
			}
		}
	}
}

// Tiers returns this chain's own tiers, in read order. atlas descends further
// by re-asserting cache.Tiered on each one, which is what gives a nested chain
// path-qualified labels.
func (c *Cache) Tiers() []cache.NamedTier {
	out := make([]cache.NamedTier, len(c.original))
	copy(out, c.original)
	return out
}

// WithTiers returns a new chain over the given tiers, which must be parallel to
// Tiers(). A new value rather than a mutation: the original tiers stay
// immutable so instrumentation is idempotent.
func (c *Cache) WithTiers(tiers []cache.Interface) cache.Interface {
	if len(tiers) != len(c.original) {
		log.Errorf("cache/multi: WithTiers got %v tiers for a chain of %v. ignoring", len(tiers), len(c.original))
		return c
	}

	active := make([]cache.Interface, len(tiers))
	copy(active, tiers)

	return &Cache{
		original:     c.original,
		active:       active,
		promoteOnHit: c.promoteOnHit,
		stats:        c.stats,
	}
}

// Stats returns a snapshot of the chain's promotion counters.
func (c *Cache) Stats() ChainStats {
	return ChainStats{
		Promotions:        c.stats.promotions.Load(),
		PromotionsDropped: c.stats.promotionsDropped.Load(),
	}
}

// Get walks the tiers in read order and returns the first hit, promoting it
// into the earlier tiers.
//
// A tier that errors is logged and skipped, and a chain in which every tier
// errored is a miss, never an error. That is not symmetry with writes — it
// follows from what the callers do. The tile middleware regenerates on both the
// error and the miss branch but only *caches* on the miss branch, so a chain
// reporting an error during a hot-tier outage would stop tiles being written to
// a perfectly healthy durable tier, turning a partial failure into a total one.
// The seed worker has the same shape.
//
// This does not protect the tile database: a total cache outage means every
// request regenerates. Stampede protection is impossible below this seam,
// because generation happens above it.
func (c *Cache) Get(ctx context.Context, key *cache.Key) ([]byte, bool, error) {
	for i, tier := range c.active {
		val, hit, err := tier.Get(ctx, key)
		if err != nil {
			// Counted by the per-tier observability wrapper, which sits outside
			// this call and can see the error; the chain only logs.
			log.Errorf("cache/multi: tier (%v) get: %v", c.original[i].Name, err)
			continue
		}
		if !hit {
			continue
		}

		if i > 0 {
			c.promote(ctx, i, key, val)
		}

		return val, true, nil
	}

	return nil, false, nil
}

// promote writes a lower-tier hit into the tiers above it.
//
// A failed promotion must never turn a hit into an error: the caller has a
// valid tile.
func (c *Cache) promote(ctx context.Context, hitAt int, key *cache.Key, val []byte) {
	if !c.promoteOnHit || cache.PromotionDisabled(ctx) {
		return
	}

	candidates := make([]int, 0, hitAt)
	for i := 0; i < hitAt; i++ {
		candidates = append(candidates, i)
	}

	for _, sel := range c.selectTiers(ctx, candidates) {
		if err := c.active[sel.index].Set(sel.ctx, key, val); err != nil {
			log.Errorf("cache/multi: promoting into tier (%v): %v", c.original[sel.index].Name, err)
			continue
		}
		c.stats.promotions.Add(1)
	}
}

// Set writes to every write target concurrently and waits for all of them.
//
// The chain does not detach: it is already off the request path when the
// outermost decorator detached it, and detaching per tier would consume a pool
// slot per tier for one logical write and destroy the joined error the seed
// path depends on.
func (c *Cache) Set(ctx context.Context, key *cache.Key, val []byte) error {
	all := make([]int, len(c.active))
	for i := range all {
		all[i] = i
	}

	targets := c.selectTiers(ctx, all)

	written := make(map[int]bool, len(targets))
	errs := make([]error, len(targets))

	var wg sync.WaitGroup
	for i, sel := range targets {
		written[sel.index] = true

		wg.Add(1)
		go func(i int, sel selection) {
			defer wg.Done()

			if err := c.active[sel.index].Set(sel.ctx, key, val); err != nil {
				errs[i] = ErrTier{Name: c.original[sel.index].Name, Op: "set", Err: err}
			}
		}(i, sel)
	}
	wg.Wait()

	// Every target is attempted, and a failure in one does not abort the
	// others: seed exists to populate durably, and a chain that reported
	// success because the hot tier accepted while the durable tier rejected
	// means a planet seed with a broken durable config exits 0 having
	// accomplished nothing.
	err := errors.Join(errs...)

	if cache.InvalidateUnwritten(ctx) {
		err = errors.Join(err, c.purgeUnwritten(ctx, key, written))
	}

	return err
}

// purgeUnwritten deletes the tile from every tier the write did not target.
//
// Write first, purge second. The reverse order reopens the re-promotion race:
// purging the hot tier first leaves a window in which a concurrent read misses
// it, falls through to the not-yet-updated durable tier, and promotes the old
// tile back. Both halves live here rather than in the caller so the ordering is
// guaranteed rather than trusted.
func (c *Cache) purgeUnwritten(ctx context.Context, key *cache.Key, written map[int]bool) error {
	var errs []error

	for i := len(c.active) - 1; i >= 0; i-- {
		if written[i] {
			continue
		}

		if err := c.active[i].Purge(ctx, key); err != nil {
			errs = append(errs, ErrTier{Name: c.original[i].Name, Op: "purge", Err: err})
		}
	}

	return errors.Join(errs...)
}

// Purge deletes the tile from every tier, durable first, sequentially.
//
// The ordering is the correctness property. Purging the hot tier first opens a
// window in which a concurrent read misses it, hits the still-stale durable
// tier, and promotes the stale tile back — after which purge reports success
// while the hot tier serves stale data. Reversing the order closes the window
// by construction, with no lock and no coordination.
//
// Purge always targets every tier: there is no --cache-tiers equivalent, and it
// is never detached. It is strict for the same reason it is ordered — a false
// failure costs a re-run, a false success costs stale tiles served indefinitely.
func (c *Cache) Purge(ctx context.Context, key *cache.Key) error {
	var errs []error

	for i := len(c.active) - 1; i >= 0; i-- {
		if err := c.active[i].Purge(ctx, key); err != nil {
			errs = append(errs, ErrTier{Name: c.original[i].Name, Op: "purge", Err: err})
		}
	}

	return errors.Join(errs...)
}

// selection is one tier chosen as a write target, with the context to call it
// with — a nested chain gets the restriction rewritten relative to itself.
type selection struct {
	index int
	ctx   context.Context
}

// selectTiers narrows candidates by the write-tier restriction, if one is in
// force. Absent a restriction every candidate is selected.
//
// Nesting is handled by path: a target of "multi/s3" selects the tier named
// "multi" and passes "s3" down to it. A target naming a tier outright selects
// the whole of it, so the restriction is cleared below that point.
func (c *Cache) selectTiers(ctx context.Context, candidates []int) []selection {
	names, restricted := cache.WriteTiers(ctx)
	if !restricted {
		out := make([]selection, len(candidates))
		for i, index := range candidates {
			out[i] = selection{index: index, ctx: ctx}
		}
		return out
	}

	var out []selection
	for _, index := range candidates {
		name := c.original[index].Name

		var nested []string
		exact := false

		for _, target := range names {
			switch {
			case target == name:
				exact = true
			case strings.HasPrefix(target, name+"/"):
				nested = append(nested, strings.TrimPrefix(target, name+"/"))
			}
		}

		switch {
		case exact:
			out = append(out, selection{index: index, ctx: cache.WithoutWriteTiers(ctx)})
		case len(nested) > 0:
			out = append(out, selection{index: index, ctx: cache.WithWriteTiers(ctx, nested)})
		}
	}

	return out
}

// TierNames returns every tier name in the tree, path-qualified, which is the
// set --cache-tiers is validated against.
func TierNames(c cache.Interface) []string {
	tiered, ok := c.(cache.Tiered)
	if !ok {
		return nil
	}

	var out []string
	for _, tier := range tiered.Tiers() {
		out = append(out, tier.Name)
		for _, nested := range TierNames(tier.Cache) {
			out = append(out, tier.Name+"/"+nested)
		}
	}

	return out
}

// LastTierName returns the name of the last tier in read order — the durable
// one by construction — recursing into a nested chain so the rule is "the last
// tier of the last tier". It is what `tegola cache seed` targets by default.
func LastTierName(c cache.Interface) (string, bool) {
	tiered, ok := c.(cache.Tiered)
	if !ok {
		return "", false
	}

	tiers := tiered.Tiers()
	if len(tiers) == 0 {
		return "", false
	}

	last := tiers[len(tiers)-1]
	if nested, ok := LastTierName(last.Cache); ok {
		return last.Name + "/" + nested, true
	}

	return last.Name, true
}
