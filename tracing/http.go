package tracing

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// InstrumentedAPIHttpHandler turns a request into a server span, adopting
// whatever trace context the caller sent as its parent.
//
// This is where an incoming trace is joined: otelhttp extracts W3C
// traceparent/tracestate and baggage through the propagator installed below,
// so a request arriving from an upstream service continues that trace instead
// of starting a sibling one. Everything downstream — the encode, the provider
// query, each cache tier — hangs off this span because ctx is threaded all the
// way through.
//
// The meter provider is pinned to a no-op explicitly. otelhttp records HTTP
// metrics of its own from the *global* meter provider, and nothing in shigola
// installs one — but "nothing installs one" is an invariant about the whole
// program, and one that a later change could break without anyone noticing
// that it also silently added a second HTTP metrics family alongside the
// prometheus observer's. Passing the no-op makes it a property of this call
// instead. Metrics stay the prometheus observer's job entirely (MAPCO-11497).
func (p *provider) InstrumentedAPIHttpHandler(method, route string, handler http.Handler) http.Handler {
	if p == nil {
		return handler
	}

	// The route pattern, not the request path: a span name has to be bounded,
	// and "/collections/foo/tiles/WebMercatorQuad/12/2048/1024" would make one
	// name per tile.
	name := method + " " + route

	return otelhttp.NewHandler(handler, name,
		otelhttp.WithTracerProvider(p.tp),
		otelhttp.WithPropagators(p.prop),
		otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
		// Without this every span is named for the operation string above,
		// which is already the route — the formatter exists to stop otelhttp
		// substituting its own default.
		otelhttp.WithSpanNameFormatter(func(operation string, _ *http.Request) string {
			return operation
		}),
	)
}
