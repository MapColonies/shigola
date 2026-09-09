package tracing

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/cache"
)

// Cache is a cache that has been wrapped for tracing.
//
// It mirrors observability.Cache field for field, and for the same reason:
// atlas re-derives instrumentation from the original cache rather than wrapping
// whatever is installed, so it has to be able to recognise its own wrapper and
// peel it off. Original also satisfies cache.Wrapped, which is what keeps
// cache.WritePoolOf, cache.TieredOf and cache.ChainStatsOf able to reach
// through a traced cache to the chain underneath.
type Cache interface {
	cache.Interface
	cache.Wrapped

	// IsTracer distinguishes this wrapper from any other cache that happens to
	// implement Original.
	IsTracer() bool
}

// tracedCache turns one cache's operations into spans.
//
// The tier field is empty for the cache as a whole and set for one tier of a
// chain, which is the only difference between the two: a whole-cache read is
// one tile fetched from somewhere in the chain and a tier read is one lookup,
// so they take different span names as well as different attributes. A span
// tree that conflated them could not answer which tier was slow, which is the
// question the layered cache exists to raise.
type tracedCache struct {
	cache  cache.Interface
	tracer trace.Tracer
	// attrs are the span attributes that do not vary per call — just the tier
	// name today. Held rather than rebuilt so an untraced call costs no
	// allocation.
	attrs []attribute.KeyValue
	// Span names, resolved at construction so Get does not branch on tier.
	getName, setName, purgeName string
}

func newTracedCache(tracer trace.Tracer, c cache.Interface) *tracedCache {
	return &tracedCache{
		cache:     c,
		tracer:    tracer,
		getName:   SpanCacheGet,
		setName:   SpanCacheSet,
		purgeName: SpanCachePurge,
	}
}

func newTracedTierCache(tracer trace.Tracer, tier string, c cache.Interface) *tracedCache {
	return &tracedCache{
		cache:     c,
		tracer:    tracer,
		attrs:     []attribute.KeyValue{AttrCacheTier.String(tier)},
		getName:   SpanTierGet,
		setName:   SpanTierSet,
		purgeName: SpanTierPurge,
	}
}

// Get traces a read, and returns exactly what the wrapped cache returned.
//
// Which is the whole contract: a layered cache's read failures are misses, not
// errors, and a decorator that turned a failed read into a different result
// would break the property that keeps tiles serving when a tier dies.
func (tc *tracedCache) Get(ctx context.Context, key *cache.Key) ([]byte, bool, error) {
	ctx, span := tc.tracer.Start(ctx, tc.getName, trace.WithAttributes(tc.attrs...))
	defer span.End()

	tc.describe(span, key)

	body, hit, err := tc.cache.Get(ctx, key)

	if span.IsRecording() {
		span.SetAttributes(AttrCacheHit.Bool(hit))
	}
	RecordError(ctx, span, err)

	return body, hit, err
}

func (tc *tracedCache) Set(ctx context.Context, key *cache.Key, body []byte) error {
	ctx, span := tc.tracer.Start(ctx, tc.setName, trace.WithAttributes(tc.attrs...))
	defer span.End()

	tc.describe(span, key)

	err := tc.cache.Set(ctx, key, body)
	RecordError(ctx, span, err)

	return err
}

func (tc *tracedCache) Purge(ctx context.Context, key *cache.Key) error {
	ctx, span := tc.tracer.Start(ctx, tc.purgeName, trace.WithAttributes(tc.attrs...))
	defer span.End()

	tc.describe(span, key)

	err := tc.cache.Purge(ctx, key)
	RecordError(ctx, span, err)

	return err
}

// describe adds the attributes that cost something to compute, and only to a
// span that will be kept.
//
// Key.String allocates, and this decorator is installed on every call while
// head sampling keeps a small fraction of them, so building the attribute
// unconditionally would put the cost on the 99% of reads whose span is
// discarded.
func (tc *tracedCache) describe(span trace.Span, key *cache.Key) {
	if key == nil || !span.IsRecording() {
		return
	}

	span.SetAttributes(AttrCacheKey.String(key.String()))
}

// Original returns the cache this wrapper traces.
func (tc *tracedCache) Original() cache.Interface { return tc.cache }

// IsTracer identifies this as a tracing wrapper, so atlas strips it rather than
// nesting a second one inside it.
func (tc *tracedCache) IsTracer() bool { return true }

var _ Cache = (*tracedCache)(nil)
