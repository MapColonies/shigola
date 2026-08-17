package cache

import "context"

// detachedCache hands Set to a bounded write pool and returns.
//
// It is applied by For to *every* cache, at the outermost construction only, so
// "no user response waits on a cache save" is a property of every deployment
// rather than of type = "multi". A plain [cache] type = "redis" gets the same
// non-blocking writes.
//
// Applied once, at the top, and this is load-bearing. Detaching per tier would
// make a composite Set return before any tier write, so its fan-out would have
// no errors to join and `tegola cache seed` would report success for writes it
// never waited on. That is why a composite cache builds its children through
// ForTier, which applies the deadline and never this.
//
// Get is a pass-through: the client is waiting. Purge is a pass-through too —
// it is an operation whose entire purpose is a guarantee of removal, and its
// tier ordering is a correctness property that a fire-and-forget hand-off would
// discard.
type detachedCache struct {
	cache Interface
	pool  *WritePool
}

// tieredDetachedCache is detachedCache over a composite cache. Two types for
// the same reason the deadline decorator has two: a single type carrying
// Tiers() would make every cache satisfy cache.Tiered and lie about it.
type tieredDetachedCache struct {
	detachedCache
}

var (
	_ Interface       = (*detachedCache)(nil)
	_ Interface       = (*tieredDetachedCache)(nil)
	_ WritePoolHolder = (*detachedCache)(nil)
	_ WritePoolHolder = (*tieredDetachedCache)(nil)
	_ Tiered          = (*tieredDetachedCache)(nil)
)

// newDetachedCache wraps c so its writes go through pool. If c is composite so
// is the result, or atlas cannot descend past this decorator and every per-tier
// metric silently disappears.
//
// Neither form implements cache.Wrapped, so a repeated SetObservability cannot
// strip the detachment off.
func newDetachedCache(c Interface, pool *WritePool) Interface {
	d := detachedCache{cache: c, pool: pool}

	if _, ok := c.(Tiered); ok {
		return &tieredDetachedCache{detachedCache: d}
	}

	return &d
}

func (d *detachedCache) Get(ctx context.Context, key *Key) ([]byte, bool, error) {
	return d.cache.Get(ctx, key)
}

// Set admits the write to the pool and returns nil. The nil means *admitted*,
// not *written*.
//
// There are three outcomes, and only two of them are in the strict-Set model:
// written, attempted-and-failed, and dropped — where nothing was attempted and
// nil is still returned. A caller that needs the write to have actually
// happened sets WithSynchronousWrites; a caller that needs to know drops
// happened watches tegola_cache_writes_dropped_total.
//
// val is not copied. The write outlives this call, so a caller must not reuse
// or mutate the buffer it passes — which is already true of every backend, all
// of which may hold it for the duration of a network round trip.
func (d *detachedCache) Set(ctx context.Context, key *Key, val []byte) error {
	if SynchronousWrites(ctx) {
		return d.cache.Set(ctx, key, val)
	}

	// The key is small and callers build it per request, but copying it costs
	// nothing and removes the one way a caller could corrupt a write in flight.
	k := *key

	d.pool.Go(ctx, func(ctx context.Context) error {
		return d.cache.Set(ctx, &k, val)
	})

	return nil
}

func (d *detachedCache) Purge(ctx context.Context, key *Key) error {
	return d.cache.Purge(ctx, key)
}

// WritePool exposes the pool so atlas can register a collector over its Stats.
// The drop counter is the sole detection mechanism for pool exhaustion, and
// cache cannot reach a metrics endpoint on its own.
func (d *detachedCache) WritePool() *WritePool { return d.pool }

func (d *tieredDetachedCache) Tiers() []NamedTier {
	return d.cache.(Tiered).Tiers()
}

func (d *tieredDetachedCache) WithTiers(tiers []Interface) Interface {
	// Re-wrap, so a second SetObservability rebuilding the chain through here
	// cannot strip the detachment off.
	return newDetachedCache(d.cache.(Tiered).WithTiers(tiers), d.pool)
}
