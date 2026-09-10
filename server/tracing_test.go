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
	"github.com/MapColonies/shigola/internal/ttools"
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

// tracedTileKey is tracedTileURI as the cache addresses it — the whole map, so
// no layer name, and tileRow/tileCol the other way round from X/Y.
//
// assertSeededKeyWasRead checks it against the key a tier is actually asked for
// rather than trusting it: seeding the wrong key would leave the tile
// unreadable and silently reintroduce the race it exists to remove.
var tracedTileKey = &cache.Key{
	TileMatrixSetID: "WebMercatorQuad",
	MapName:         "test-map",
	Z:               10,
	X:               2,
	Y:               3,
}

// twoTierCache builds hot → durable through cache.For, so the request walks the
// same decorator stack it would in production. The tiers come back so a caller
// can seed one directly, which is how TestTracedRequestPublishesNoNewMetrics
// gets a warm cache without waiting on a detached write.
func twoTierCache(t *testing.T, hotType, durableType string) (cache.Interface, *faketier.Tier, *faketier.Tier) {
	t.Helper()

	hot, durable := faketier.New(hotType), faketier.New(durableType)

	for cacheType, tier := range map[string]*faketier.Tier{hotType: hot, durableType: durable} {
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

	return c, hot, durable
}

// assertSeededKeyWasRead fails if the tile a request looked for is not the one
// the caller seeded, which is the only way seeding could quietly stop working.
//
// tier names the tier whose calls these are, so a failure says where to look.
func assertSeededKeyWasRead(t *testing.T, tier string, calls []faketier.Call) {
	t.Helper()

	for _, call := range calls {
		if call.Op == faketier.OpGet && call.Key == tracedTileKey.String() {
			return
		}
	}

	t.Fatalf("the %v tier was never asked for %v; the seeded key is wrong and this test is racing a detached write again",
		tier, tracedTileKey.String())
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
	c, _, _ := twoTierCache(t, "tracedhot", "traceddurable")
	a.SetCache(c)
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
	c, _, _ := twoTierCache(t, "untracedhot", "untraceddurable")
	a.SetCache(c)

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
	c, hot, durable := twoTierCache(t, "metrichot", "metricdurable")
	durable.Seed(tracedTileKey, []byte("tile"))
	a.SetCache(c)

	observer, err := prometheus.New(dict.Dict{})
	if err != nil {
		t.Fatalf("prometheus observer: %v", err)
	}
	a.SetObservability(observer)

	// One untraced request before the snapshot, against a cache that is
	// already warm.
	//
	// A prometheus *Vec publishes a family only once a label set on it has
	// been observed, so the snapshot has to be taken in a state where every
	// family the traced request will touch already exists — otherwise the
	// cache warming up is blamed on tracing. Against a seeded durable tier one
	// request does it: the hot tier misses and the durable one hits, so the
	// hit and miss families are both published, and the traced request has the
	// same shape.
	//
	// This used to be two requests against an empty cache, relying on the
	// first request's write making the second a hit. Writes are detached
	// through the bounded pool, so that was a race, and it lost often enough
	// to fail this test roughly half the time on the trunk.
	if _, _, err := doRequest(t, a, http.MethodGet, tracedTileURI, nil); err != nil {
		t.Fatalf("untraced request: %v", err)
	}

	assertSeededKeyWasRead(t, "metrichot", hot.Calls())

	before := ttools.MetricFamilyNames(t)
	httpBefore := map[string]float64{}
	for _, family := range []string{
		"shigola_api_requests_total",
		"shigola_api_duration_seconds",
		"shigola_api_response_size_bytes",
	} {
		httpBefore[family] = sampleCount(t, family)
	}

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

	after := ttools.MetricFamilyNames(t)
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

	// Values, not just family names.
	//
	// The names would look identical if the tracing middleware had displaced
	// the metrics one rather than wrapping outside it — the families would
	// still exist from the untraced requests, and simply stop moving. These
	// four are the whole set the HTTP observer maintains, and the middleware
	// reorder in NewRouter is exactly what could disturb them.
	for _, family := range []string{
		"shigola_api_requests_total",
		"shigola_api_duration_seconds",
		"shigola_api_response_size_bytes",
	} {
		if got := sampleCount(t, family) - httpBefore[family]; got != 1 {
			t.Errorf("%v moved by %v over one traced request, want 1", family, got)
		}
	}
}

// sampleCount totals a metric family across every label set: counter values,
// or histogram observation counts.
//
// Summed rather than read per label set because the point here is only whether
// the observer still saw the request, and the HTTP families carry route and
// status labels this test has no reason to know.
func sampleCount(t *testing.T, name string) float64 {
	t.Helper()

	families, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var total float64
	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, m := range family.GetMetric() {
			if c := m.GetCounter(); c != nil {
				total += c.GetValue()
			}
			if h := m.GetHistogram(); h != nil {
				total += float64(h.GetSampleCount())
			}
		}
	}

	return total
}
