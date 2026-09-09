package server_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/observability/prometheus"
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

	tracer, exporter := faketracer.New(t)

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
			t.Errorf("no %v span in the tree; recorded %v", want, faketracer.Names(spans))
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

	_, exporter := faketracer.New(t)

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
		t.Errorf("recorded %d spans with tracing disabled: %v", len(got), faketracer.Names(got))
	}
	if a.Tracing().Enabled() {
		t.Error("Tracing().Enabled() = true on an atlas that was never given a backend")
	}
}

// TestTracedRequestPublishesNoNewMetrics is the half of "Prometheus metrics are
// unaffected" that atlas's own test cannot reach.
//
// otelhttp records HTTP metrics of its own from the *global* OTEL meter
// provider. Nothing in shigola installs one, and the HTTP decorator pins it to
// a no-op explicitly — but both of those are invariants about the program
// rather than about this request, so the check that matters is whether a real
// traced request adds a metric family. atlas's test issues no request and so
// could never see this.
func TestTracedRequestPublishesNoNewMetrics(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer2)
	a.SetCache(twoTierCache(t, "metrichot", "metricdurable"))

	observer, err := prometheus.New(dict.Dict{})
	if err != nil {
		t.Fatalf("prometheus observer: %v", err)
	}
	a.SetObservability(observer)

	// Two untraced requests before the snapshot, not one.
	//
	// A prometheus *Vec publishes a family only once a label set on it has
	// been observed, so the snapshot has to be taken in a state where every
	// family the traced request will touch already exists. One request is not
	// enough: the first is a miss on an empty cache and populates only the
	// misses families, and it then writes the tile — so the second is a hit
	// and publishes shigola_cache_hits_total and its per-tier sibling for the
	// first time. Snapshotting after one request blames tracing for the cache
	// warming up.
	for range 2 {
		if _, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil); err != nil {
			t.Fatalf("untraced request: %v", err)
		}
	}

	before := familyNames(t)

	tracer, exporter := faketracer.New(t)
	tracer.Install()
	a.SetTracing(tracer)

	if _, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil); err != nil {
		t.Fatalf("traced request: %v", err)
	}

	// Without this the test would pass just as happily if tracing had not been
	// wired into the request at all.
	if len(exporter.GetSpans()) == 0 {
		t.Fatal("the traced request recorded no spans, so this proved nothing about metrics")
	}

	after := familyNames(t)
	for _, name := range after {
		if !slices.Contains(before, name) {
			t.Errorf("a traced request published a new metric family: %v", name)
		}
	}
	for _, name := range before {
		if !slices.Contains(after, name) {
			t.Errorf("a traced request removed a metric family: %v", name)
		}
	}
}

// familyNames is every metric family the process publishes right now.
func familyNames(t *testing.T) []string {
	t.Helper()

	families, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.GetName())
	}
	slices.Sort(names)

	return names
}
