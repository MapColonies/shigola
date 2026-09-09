package server_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tracing"
)

// tracedTileURI is a zoom the test map actually serves.
//
// testLayer2 covers 10-15; a request outside a layer's range is answered with
// the empty tile before the handler reaches the cache or the encode, so an
// out-of-range zoom here would record only the request span and look exactly
// like tracing being unwired.
const tracedTileURI = "/collections/test-map/tiles/WebMercatorQuad/10/3/2"

func tracedBackend(t *testing.T) (tracing.Interface, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return tracing.NewWithProvider(tp, "test"), exporter
}

// twoTierCache builds hot → durable through cache.For, so the request walks the
// same decorator stack it would in production.
func twoTierCache(t *testing.T, hotType, durableType string) cache.Interface {
	t.Helper()

	for cacheType, tier := range map[string]*faketier.Tier{
		hotType:     faketier.New(hotType),
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

// TestTileRequestProducesOneSpanTree is the acceptance criterion end to end: a
// tile request produces a usable span tree, meaning one trace whose root is the
// request and which contains the cache lookup per tier, the encode and the
// provider query underneath it.
//
// Worth having as well as the per-seam tests, because everything it checks
// beyond their sum is a wiring property none of them can see: that the tracing
// middleware sits outside the metrics middleware and so gets the span into the
// context before anything downstream looks for one, and that the handler's
// context is the one the cache and the encode are actually given.
func TestTileRequestProducesOneSpanTree(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	tracer, exporter := tracedBackend(t)

	a := newTestMapWithLayers(testLayer2)
	a.SetCache(twoTierCache(t, "tracedhot", "traceddurable"))
	a.SetTracing(tracer)

	w, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("a tile request recorded no spans at all")
	}

	byName := map[string]tracetest.SpanStub{}
	traceIDs := map[string]int{}
	var roots []tracetest.SpanStub
	for _, span := range spans {
		byName[span.Name] = span
		traceIDs[span.SpanContext.TraceID().String()]++
		if !span.Parent.IsValid() {
			roots = append(roots, span)
		}
	}

	// One trace, not several. Spans scattered across trace ids are the
	// signature of a context that was dropped somewhere on the way down, and
	// each fragment on its own explains nothing.
	if len(traceIDs) != 1 {
		t.Errorf("the request produced %d traces, want 1: %v", len(traceIDs), traceIDs)
	}

	// The request is the root. If the HTTP span were nested inside something —
	// or missing — the tree would not be attributable to a request.
	if len(roots) != 1 {
		t.Errorf("%d root spans, want 1", len(roots))
	} else if want := http.MethodGet + " /collections/:collection_id/tiles/:tile_matrix_set_id/:tile_matrix/:tile_row/:tile_col"; roots[0].Name != want {
		t.Errorf("root span = %q, want %q", roots[0].Name, want)
	}

	for _, want := range []string{
		tracing.SpanCacheGet,
		tracing.SpanTierGet,
		tracing.SpanEncode,
		tracing.SpanProviderQuery,
	} {
		if _, ok := byName[want]; !ok {
			t.Errorf("no %v span in the tree; recorded %v", want, names(spans))
		}
	}

	// The encode hangs off the request rather than off the cache read that
	// missed, and the provider query hangs off the encode.
	if encode, ok := byName[tracing.SpanEncode]; ok && len(roots) == 1 {
		if encode.Parent.SpanID() != roots[0].SpanContext.SpanID() {
			t.Error("the encode span is not a child of the request span")
		}
	}
	if query, ok := byName[tracing.SpanProviderQuery]; ok {
		if encode, ok := byName[tracing.SpanEncode]; ok {
			if query.Parent.SpanID() != encode.SpanContext.SpanID() {
				t.Error("the provider query span is not a child of the encode span")
			}
		}
	}
}

// TestTileRequestWithoutTracingRecordsNothing is the default path: an atlas
// with no tracing backend serves the same tile and installs no instrumentation.
func TestTileRequestWithoutTracingRecordsNothing(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	_, exporter := tracedBackend(t)

	a := newTestMapWithLayers(testLayer2)
	a.SetCache(twoTierCache(t, "untracedhot", "untraceddurable"))

	w, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if got := exporter.GetSpans(); len(got) != 0 {
		t.Errorf("recorded %d spans with tracing disabled: %v", len(got), names(got))
	}
	if a.Tracing().Enabled() {
		t.Error("Tracing().Enabled() = true on an atlas that was never given a backend")
	}
}

func names(spans tracetest.SpanStubs) []string {
	out := make([]string, len(spans))
	for i := range spans {
		out[i] = spans[i].Name
	}

	return out
}
