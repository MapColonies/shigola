package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/fakelog"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tracing"
)

// servedWithABrokenTier serves one tile request through hot → durable where the
// hot tier fails every read, and returns what was logged and what was traced.
//
// The tier names have to differ between tests: cache.Register is process-wide,
// so two tests registering the same type would collide on whichever ran second.
func servedWithABrokenTier(t *testing.T, hotType, durableType string, traced bool) (*fakelog.Recorder, tracetest.SpanStubs) {
	t.Helper()

	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer2)
	a.SetCache(failingChain(t, hotType, durableType, errors.New("tier is down")))

	var exporter *tracetest.InMemoryExporter
	if traced {
		var tracer tracing.Interface
		tracer, exporter = faketracer.New(t)
		a.SetTracing(tracer)
	}

	rec := fakelog.Default(t)

	w, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// The read failure is a miss, never an error: the tile is still served.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if exporter == nil {
		return rec, nil
	}

	return rec, exporter.GetSpans()
}

// failingChain builds hot → durable where hot fails every read, so a request
// through it produces the one log line a broken tier produces in production.
func failingChain(t *testing.T, hotType, durableType string, readErr error) cache.Interface {
	t.Helper()

	hot := faketier.New(hotType)
	hot.FailOn(faketier.OpGet, readErr)

	for cacheType, tier := range map[string]*faketier.Tier{
		hotType:     hot,
		durableType: faketier.New(durableType),
	} {
		c := tier
		if err := cache.Register(cacheType, func(dict.Dicter) (cache.Interface, error) { return c, nil }); err != nil {
			t.Fatalf("register %v: %v", cacheType, err)
		}
	}

	c, err := cache.For("multi", dict.Dict{
		"layers": []map[string]interface{}{
			{"type": hotType, "name": hotType},
			{"type": durableType, "name": durableType},
		},
	})
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}

	return c
}

// TestRequestLogsCarryTheRequestsTraceIDs is the acceptance criterion end to
// end: a log line emitted while serving a traced request names that request's
// trace, so Tempo and Loki reach each other.
//
// Worth having as well as the handler's own unit tests, because what it checks
// beyond their sum is the plumbing: that the span the HTTP middleware starts
// reaches a context four decorators down in the cache chain, and that the ids
// on the record are the same ones the exporter recorded rather than merely
// well-formed. A tier read failure is the line to prove it on — per the cache
// invariant it is the only evidence a tier is broken, so it is the line an
// operator most needs to reach from a trace.
func TestRequestLogsCarryTheRequestsTraceIDs(t *testing.T) {
	rec, spans := servedWithABrokenTier(t, "corrhot", "corrdurable", true)
	if len(spans) == 0 {
		t.Fatal("the request recorded no spans at all")
	}

	record := rec.Containing(t, "cache/multi: tier (corrhot) get:")

	// The record belongs to the span whose context the chain was called with,
	// not merely to the right trace: the whole-cache read is what wraps the
	// chain, so that is the span the line hangs off in Tempo.
	cacheGet := spanNamed(t, spans, tracing.SpanCacheGet)
	fakelog.AssertCorrelation(t, record,
		spans[0].SpanContext.TraceID().String(),
		cacheGet.SpanContext.SpanID().String(),
	)
}

// TestRequestLogsWithoutTracingCarryNoCorrelation is the default: with no
// tracing backend the same failure logs the same message and adds no fields.
func TestRequestLogsWithoutTracingCarryNoCorrelation(t *testing.T) {
	rec, _ := servedWithABrokenTier(t, "uncorrhot", "uncorrdurable", false)

	fakelog.AssertCorrelation(t, rec.Containing(t, "cache/multi: tier (uncorrhot) get:"), "", "")
}

// spanNamed is faketracer.SpanNamed over spans already read off the exporter.
func spanNamed(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()

	var found []tracetest.SpanStub
	for _, span := range spans {
		if span.Name == name {
			found = append(found, span)
		}
	}

	if len(found) != 1 {
		t.Fatalf("%d spans named %q, want 1; recorded %v", len(found), name, faketracer.Names(spans))
	}

	return found[0]
}
