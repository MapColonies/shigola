// Package tracing is shigola's distributed-tracing seam: OpenTelemetry spans
// exported over OTLP, to Tempo or to anything else that speaks it.
//
// It is deliberately *not* part of observability. That package is a metrics
// abstraction which Mimir consumes unchanged, so OTEL metrics are never
// wanted here: nothing in this package registers a prometheus collector, and
// nothing installs a global OTEL MeterProvider — which is what keeps a build
// with tracing enabled emitting exactly the metrics it emitted before
// (MAPCO-11497). metrics_test.go asserts that rather than trusting it.
//
// The dependency rule that stops cache importing observability holds here for
// the same reason — this package imports cache, so cache cannot import it — so
// tier spans are wired from atlas through the decorators below, mirroring how
// tier metrics already work.
package tracing

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/cache"
)

// ScopeName is the instrumentation scope every span shigola records about
// itself is attributed to. One scope, not one per package: these spans describe
// a single service instrumenting itself, and a reader filtering by scope wants
// "shigola's own spans" rather than "the spans atlas happened to start".
const ScopeName = "github.com/MapColonies/shigola"

// Span names. Named constants rather than literals at the call sites because
// they are the dashboard's and the alert's view of this service — a renamed
// span silently empties whatever queried the old name.
const (
	SpanEncode        = "atlas.Encode"
	SpanProviderQuery = "provider.MVTForLayers"
	SpanCacheGet      = "cache.Get"
	SpanCacheSet      = "cache.Set"
	SpanCachePurge    = "cache.Purge"
	SpanTierGet       = "cache.tier.Get"
	SpanTierSet       = "cache.tier.Set"
	SpanTierPurge     = "cache.tier.Purge"
)

// Span attribute keys, in shigola's own namespace: the OTEL semantic
// conventions have nothing for a tile or a cache tier, and inventing an
// unprefixed key risks colliding with a convention that later does.
const (
	AttrMapName       = attribute.Key("shigola.map")
	AttrLayerName     = attribute.Key("shigola.layer")
	AttrTileMatrixSet = attribute.Key("shigola.tile_matrix_set")
	AttrTileZ         = attribute.Key("shigola.tile.z")
	AttrTileX         = attribute.Key("shigola.tile.x")
	AttrTileY         = attribute.Key("shigola.tile.y")
	AttrProviderName  = attribute.Key("shigola.provider")
	AttrLayerCount    = attribute.Key("shigola.layer_count")
	AttrCacheTier     = attribute.Key("shigola.cache.tier")
	AttrCacheHit      = attribute.Key("shigola.cache.hit")
	AttrCacheKey      = attribute.Key("shigola.cache.key")
)

// Interface is what a tracing backend provides.
//
// The zero-cost case is a distinct implementation rather than a flag read
// inside each method: Null returns its argument from every Instrumented*
// method, so a process with tracing off carries no decorator at all — not a
// decorator that starts a non-recording span per cache read. "No measurable
// overhead when disabled" is then a property of the wiring rather than a claim
// about how cheap a no-op span is.
type Interface interface {
	// Tracer returns the tracer instrumentation starts spans from.
	Tracer() trace.Tracer

	// Enabled reports whether this backend records and exports spans.
	Enabled() bool

	// Name reports the configured exporter, for startup logging.
	Name() string

	// Install publishes this backend as OTEL's process-wide tracer provider
	// and text-map propagator, which is what lets an instrumented client
	// library find them without being handed either.
	Install()

	// Shutdown flushes spans the exporter still holds and releases it. It is
	// separate from observability's Shutdown, and from provider.Cleanup,
	// because the flush has to happen at a specific point in the shutdown
	// order — see cmd/shigola/cmd/server.go.
	Shutdown(ctx context.Context) error

	CacheTracer
	APITracer
}

// CacheTracer wraps a cache so its operations become spans.
//
// Two methods for the same reason observability has two: a whole-cache read is
// one tile fetched from somewhere in the chain, a tier read is one lookup, and
// a span tree that cannot tell them apart cannot answer which tier was slow.
type CacheTracer interface {
	// InstrumentedCache traces the cache as a whole.
	InstrumentedCache(cache.Interface) cache.Interface

	// InstrumentedTierCache traces one tier, labelled by tier name.
	InstrumentedTierCache(tier string, c cache.Interface) cache.Interface
}

// APITracer wraps an http.Handler so a request becomes a server span, with any
// trace context the caller sent adopted as its parent.
type APITracer interface {
	InstrumentedAPIHttpHandler(method, route string, handler http.Handler) http.Handler
}

// RecordError attributes a failure to span.
//
// The error is always recorded as an event, and the span's *status* is set only
// when the failure is this service's own. A caller that walked away — a client
// that disconnected mid-request — fails everything still in flight with its own
// context error, and marking those spans failed would paint a busy service red
// for doing exactly the right thing.
//
// This is the same line the prometheus observer's countReadError draws, and the
// two have to agree: a trace saying a tier failed while
// shigola_cache_tier_errors_total says it did not is worse than either signal
// on its own. One function so that agreement is structural, and so a third call
// site cannot quietly pick a different rule.
func RecordError(ctx context.Context, span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)

	if ctx.Err() != nil {
		return
	}

	span.SetStatus(codes.Error, err.Error())
}

// InstrumentAPIHandler adapts an APITracer to httptreemux's Handler
// registration triple, the way observability.InstrumentAPIHandler does, so the
// two read alike at the one call site that uses both.
//
// A nil tracer returns the handler untouched.
func InstrumentAPIHandler(method, route string, t APITracer, handler http.Handler) (string, string, http.Handler) {
	if t == nil {
		return method, route, handler
	}

	return method, route, t.InstrumentedAPIHttpHandler(method, route, handler)
}
