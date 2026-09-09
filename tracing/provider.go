package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/cache"
)

// TracerProvider is the OTEL tracer provider a backend is built over, narrowed
// to what this package needs of it: somewhere to get tracers from, and
// somewhere to send the flush at shutdown.
//
// An interface rather than *sdktrace.TracerProvider so that the backend can be
// constructed over a provider wired to something other than a collector — a
// synchronous in-memory exporter, in this package's tests and in atlas's.
type TracerProvider interface {
	trace.TracerProvider

	// Shutdown flushes anything the provider's processors still hold.
	Shutdown(context.Context) error
}

// NewWithProvider returns a tracing backend that records through tp.
//
// New is the normal way in; this is the seam underneath it, separated so that
// choosing an exporter and wiring up instrumentation are not the same decision.
func NewWithProvider(tp TracerProvider) Interface {
	return &provider{
		tp:     tp,
		tracer: tp.Tracer(ScopeName),
		// TraceContext is the W3C header pair every current collector, gateway
		// and Tempo speaks. Baggage rides alongside it so a tenant or request
		// label set upstream survives this hop, even though nothing here reads
		// one.
		prop: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}
}

// provider is a tracing backend over one OTEL tracer provider.
type provider struct {
	tp     TracerProvider
	tracer trace.Tracer
	prop   propagation.TextMapPropagator
}

func (p *provider) Tracer() trace.Tracer {
	if p == nil {
		return nullTracer
	}

	return p.tracer
}

func (p *provider) Enabled() bool { return p != nil }

// Install publishes this backend as OTEL's process-wide tracer provider and
// text-map propagator.
//
// The propagator is the half that matters for outgoing calls: an
// OTEL-instrumented client — an http.RoundTripper, a gRPC dial option — reads
// the global propagator to inject traceparent into what it sends, so
// installing it here is what lets a client instrumented later carry this
// service's trace with no further wiring. The tracer provider goes in for the
// symmetric reason: a library that starts spans of its own finds the real one
// rather than a no-op.
//
// Separate from New so that constructing a backend has no global side effect —
// which is what lets a test hold a live backend without changing the process
// it runs in.
func (p *provider) Install() {
	if p == nil {
		return
	}

	otel.SetTracerProvider(p.tp)
	otel.SetTextMapPropagator(p.prop)
	installDiagnostics()
}

// Shutdown flushes spans the processor still holds and stops it.
//
// Worth calling on the way out rather than leaving to process exit: a batch
// processor holds up to a batch interval's worth of spans, and the traces most
// worth having are usually the ones from just before a shutdown.
func (p *provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}

	return p.tp.Shutdown(ctx)
}

func (p *provider) InstrumentedCache(c cache.Interface) cache.Interface {
	if p == nil {
		return c
	}

	return newTracedCache(p.tracer, c)
}

func (p *provider) InstrumentedTierCache(tier string, c cache.Interface) cache.Interface {
	if p == nil {
		return c
	}

	return newTracedTierCache(p.tracer, tier, c)
}

var _ Interface = (*provider)(nil)
