package tracing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/tracing"
)

const (
	upstreamTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	upstreamSpanID  = "00f067aa0ba902b7"
)

// TestInstrumentedAPIHttpHandlerJoinsAnIncomingTrace is the propagation half of
// the feature.
//
// A gateway that already started a trace sends traceparent; this service has to
// continue that trace rather than root a sibling one, or a request's spans end
// up in two unrelated traces and neither shows the whole path.
func TestInstrumentedAPIHttpHandlerJoinsAnIncomingTrace(t *testing.T) {
	backend, exporter := faketracer.New(t)

	handler := backend.InstrumentedAPIHttpHandler(
		http.MethodGet, "/collections/:collection_id/tiles",
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)

	req := httptest.NewRequest(http.MethodGet, "/collections/osm/tiles", nil)
	req.Header.Set("traceparent", "00-"+upstreamTraceID+"-"+upstreamSpanID+"-01")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1: %v", len(spans), faketracer.Names(exporter.GetSpans()))
	}
	span := spans[0]

	if got := span.SpanContext.TraceID().String(); got != upstreamTraceID {
		t.Errorf("trace id = %v, want the caller's %v", got, upstreamTraceID)
	}
	if got := span.Parent.SpanID().String(); got != upstreamSpanID {
		t.Errorf("parent span id = %v, want the caller's %v", got, upstreamSpanID)
	}
}

// TestInstrumentedAPIHttpHandlerNamesSpansForTheRoute keeps span names bounded.
//
// A tile server's request paths are unbounded by construction — one per tile —
// so naming spans after them would give Tempo a new operation name per tile and
// make the service's own latency unreadable.
func TestInstrumentedAPIHttpHandlerNamesSpansForTheRoute(t *testing.T) {
	backend, exporter := faketracer.New(t)

	const route = "/collections/:collection_id/tiles/:tile_matrix_set_id/:tile_matrix/:tile_row/:tile_col"

	handler := backend.InstrumentedAPIHttpHandler(http.MethodGet, route,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)

	for _, path := range []string{
		"/collections/osm/tiles/WebMercatorQuad/12/2048/1024",
		"/collections/osm/tiles/WebMercatorQuad/12/2049/1025",
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	want := http.MethodGet + " " + route
	for _, span := range exporter.GetSpans() {
		if span.Name != want {
			t.Errorf("span name = %q, want %q", span.Name, want)
		}
	}
}

// TestInstrumentedAPIHttpHandlerPutsTheSpanInTheRequestContext is what
// everything downstream depends on.
//
// The encode, the provider query and every cache tier hang off this span only
// because it is reachable from the request's context — and it is the same
// property trace exemplars will read the active trace id out of
// (MAPCO-11496).
func TestInstrumentedAPIHttpHandlerPutsTheSpanInTheRequestContext(t *testing.T) {
	backend, _ := faketracer.New(t)

	var seen trace.SpanContext
	handler := backend.InstrumentedAPIHttpHandler(http.MethodGet, "/",
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = trace.SpanContextFromContext(r.Context())
		}),
	)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !seen.IsValid() {
		t.Fatal("the handler's context carries no span")
	}
	if !seen.IsSampled() {
		t.Error("the handler's span is not sampled, so nothing downstream will be recorded")
	}
}

// TestNewDoesNotTouchOtelGlobals pins Install being a separate step.
//
// Constructing a backend has to be free of global side effects, or a test that
// builds one changes the process it runs in — and, more to the point, a caller
// gets to decide whether this backend is the process's backend.
func TestNewDoesNotTouchOtelGlobals(t *testing.T) {
	// Compared by identity rather than by type. otel.GetTracerProvider does
	// not hand back the no-op provider itself but a delegating wrapper around
	// whatever has been installed, so asserting on the type here would only
	// ever describe the SDK's internals — and an earlier version of this test
	// that did exactly that skipped itself on every run.
	before := otel.GetTracerProvider()
	beforeProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(before)
		otel.SetTextMapPropagator(beforeProp)
	})

	backend, exporter := faketracer.New(t)

	if got := otel.GetTracerProvider(); got != before {
		t.Errorf("constructing a backend replaced the global tracer provider with %T", got)
	}

	backend.Install()

	// Asserted behaviourally, not by comparing the installed provider against
	// the backend's own: what a caller actually depends on is that a span
	// started from OTEL's global lands in this backend, which is the whole
	// point of Install and is what an instrumented client library will do.
	_, span := otel.Tracer("probe").Start(context.Background(), "probe")
	span.End()

	if got := faketracer.SpansNamed(exporter, "probe"); len(got) != 1 {
		t.Errorf("a span started from the global tracer provider did not reach the installed backend; recorded %v",
			faketracer.Names(exporter.GetSpans()))
	}

	// The propagator is the half that makes an instrumented outgoing client
	// carry this service's trace context without being handed anything.
	//
	// Asserted on the header names it injects rather than on the propagator
	// value: a composite propagator is a slice underneath, so comparing two of
	// them panics. The header names are the actual contract anyway — the W3C
	// pair is what Tempo, every gateway and every other service read.
	fields := otel.GetTextMapPropagator().Fields()
	for _, want := range []string{"traceparent", "baggage"} {
		if !slices.Contains(fields, want) {
			t.Errorf("the installed propagator does not inject %q; injects %v", want, fields)
		}
	}
}

// TestInstalledPropagatorReachesAnOutgoingClient is the mechanism the ticket's
// "outgoing calls carry it" rests on, pinned.
//
// shigola instruments no client of its own; what it does is publish the global
// propagator, and a client library that reads it then injects trace context
// with no further wiring. That indirection is load-bearing and subtle — the GCS
// cache's transport is built by google.golang.org/api *before* Install runs, and
// works anyway only because OTEL's global propagator is a delegating wrapper
// rather than a value captured at construction.
//
// So: build an otelhttp round-tripper before Install, the way that transport is
// built, and check that a request through it afterwards carries traceparent.
func TestInstalledPropagatorReachesAnOutgoingClient(t *testing.T) {
	before := otel.GetTracerProvider()
	beforeProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(before)
		otel.SetTextMapPropagator(beforeProp)
	})

	// Constructed first, deliberately: this is the ordering the GCS client has.
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

	backend, _ := faketracer.New(t)
	backend.Install()

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("traceparent")
	}))
	t.Cleanup(srv.Close)

	// Under a span, since traceparent describes one.
	ctx, span := backend.Tracer().Start(context.Background(), "outgoing")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	span.End()

	if got == "" {
		t.Fatal("an outgoing request carried no traceparent, so nothing downstream can join this trace")
	}
	if want := span.SpanContext().TraceID().String(); !strings.Contains(got, want) {
		t.Errorf("traceparent %q does not carry this trace's id %v", got, want)
	}
}

// TestNullTracerHandlerIsTheHandlerItWasGiven — the disabled case again, at the
// HTTP seam: no wrapper, so no per-request cost and no propagator installed.
func TestNullTracerHandlerIsTheHandlerItWasGiven(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	got := tracing.NullTracer.InstrumentedAPIHttpHandler(http.MethodGet, "/", handler)

	if _, ok := got.(http.HandlerFunc); !ok {
		t.Errorf("the handler was wrapped: %T", got)
	}
	if err := tracing.NullTracer.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}
