package tracing

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/MapColonies/shigola/cache"
)

// NullTracer is the default: tracing off, and off at zero cost.
//
// Every Instrumented* method returns its argument, so a process with tracing
// disabled runs with no tracing decorator installed anywhere — not with a
// decorator that starts a non-recording span per cache read and per request.
// That is what makes "no measurable overhead when disabled" structural rather
// than a claim about how cheap a dropped span is.
var NullTracer Null

// Null is the no-op tracing backend.
type Null struct{}

// nullTracer is the OTEL API's own no-op tracer, held once rather than
// constructed per call: Tracer() is reached by anything that asks for a tracer
// without checking Enabled first, and it must not allocate.
var nullTracer = noop.NewTracerProvider().Tracer(ScopeName)

func (Null) Tracer() trace.Tracer { return nullTracer }

func (Null) Enabled() bool { return false }

// Install deliberately installs nothing.
//
// Not even a propagator: with tracing off there is no trace context to inject,
// and leaving OTEL's globals untouched is what keeps a disabled build
// indistinguishable from one that never heard of tracing.
func (Null) Install() {}

func (Null) Shutdown(context.Context) error { return nil }

func (Null) InstrumentedCache(c cache.Interface) cache.Interface { return c }

func (Null) InstrumentedTierCache(_ string, c cache.Interface) cache.Interface { return c }

func (Null) InstrumentedAPIHttpHandler(_, _ string, handler http.Handler) http.Handler {
	return handler
}

var _ Interface = Null{}
