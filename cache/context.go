package cache

import "context"

// Caller intent crosses the cache seam as context values, because the serve
// path and the CLI path reach the identical cache.Interface and a composite
// cache has to distinguish them. The alternative is threading flags through
// cache.Interface, which changes the seam for every backend and every
// out-of-tree implementer.
//
// Behaviour control through context values is a pattern Go style guides treat
// with suspicion, and there are four of them here. Each is justified
// individually; four is a lot of invisible caller state at one seam.
//
// Four is the ceiling. A fifth concern should become a single CallerIntent
// value carrying all of them, not a fifth independent key.

type contextKey int

const (
	ctxKeyNoPromotion contextKey = iota
	ctxKeyWriteTiers
	ctxKeySynchronousWrites
	ctxKeyInvalidateUnwritten
)

// WithoutPromotion suppresses read-through promotion for reads made with the
// returned context.
//
// `tegola cache seed` without --overwrite reads every tile through the cache
// before deciding whether to generate it. With promotion on, a seed over a
// large area would promote every durable-tier tile into the hot tier, in seed
// order, at seeding throughput — overwriting the live working set with cold
// tiles, which is the exact harm the layered cache exists to avoid.
func WithoutPromotion(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyNoPromotion, true)
}

// PromotionDisabled reports whether promotion was suppressed for this context.
func PromotionDisabled(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyNoPromotion).(bool)
	return v
}

// WithWriteTiers restricts writes to the named tiers. Names are tier names as
// they appear in the tier metric label, path-qualified for a nested chain
// ("multi/s3").
//
// When set it bounds promotion as well as Set, so --cache-tiers=s3 means one
// thing and means it completely: these are the tiers I may write, by either
// route. When absent, promote_on_hit governs promotion alone.
func WithWriteTiers(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, ctxKeyWriteTiers, names)
}

// WithoutWriteTiers clears a restriction for everything below this point. A
// composite cache uses it when a target names one of its tiers outright: the
// whole of that tier is selected, including all of a nested chain's own tiers.
func WithoutWriteTiers(ctx context.Context) context.Context {
	if _, ok := WriteTiers(ctx); !ok {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyWriteTiers, []string(nil))
}

// WriteTiers returns the write-tier restriction, and whether one is in force.
func WriteTiers(ctx context.Context) (names []string, ok bool) {
	names, _ = ctx.Value(ctxKeyWriteTiers).([]string)
	return names, len(names) > 0
}

// WithSynchronousWrites makes Set write inline and return the result, instead
// of handing the write to the detached pool.
//
// Set by every entry point that has no response to protect and would otherwise
// lose writes at process exit or execution freeze: the CLI seed/purge worker
// and the Lambda entrypoint. Seed correctness must not depend on a clean
// shutdown, so this is not interchangeable with draining the pool at exit.
func WithSynchronousWrites(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeySynchronousWrites, true)
}

// SynchronousWrites reports whether writes on this context must complete before
// Set returns.
func SynchronousWrites(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeySynchronousWrites).(bool)
	return v
}

// WithInvalidateUnwritten makes a composite Set purge the tiers it did not
// write, after writing the ones it did.
//
// `seed --overwrite` states that the content changed, which is precisely when a
// stale hot tier must stop serving. The purge cannot be composed above the seam
// — atlas.PurgeMapTile purges every tier, including the one seed just wrote —
// so the chain issues it, which is also what guarantees the write-then-purge
// ordering rather than trusting a caller to sequence the two calls.
func WithInvalidateUnwritten(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyInvalidateUnwritten, true)
}

// InvalidateUnwritten reports whether unwritten tiers must be purged after the
// write.
func InvalidateUnwritten(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyInvalidateUnwritten).(bool)
	return v
}
