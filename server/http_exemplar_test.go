package server_test

import (
	"net/http"
	"net/url"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/observability/prometheus"
	"github.com/MapColonies/shigola/server"
)

// A zoom of its own, inside testLayer2's 10-15 range.
//
// The `handler` label keeps the tile matrix — :tile_matrix is in the observer's
// default observe-vars — so requesting a zoom no other test in this package
// uses puts this test's observations on a series of their own. That matters
// here and nowhere else in the package: prometheus keeps one exemplar per
// bucket and overwrites it, so two tests sharing a series would each be reading
// whichever ran last.
const exemplarTileURI = "/collections/test-map/tiles/WebMercatorQuad/11/3/2"

// exemplarHandlerLabel is exemplarTileURI as the observer labels it: the route
// variables named in the observer's default observe-vars keep their value, and
// the tile row and column are collapsed back to their names, because a label
// per tile is a cardinality explosion rather than a metric.
const exemplarHandlerLabel = "/collections/test-map/tiles/WebMercatorQuad/11/:tile_row/:tile_col"

// TestRequestExemplarNamesTheRequestSpan is the HTTP half of MAPCO-11496, and
// the regression test for the middleware order server.NewRouter documents.
//
// Inverting that order leaves the span tree exactly as it is — the request span
// is still the root and everything still hangs off it — so
// TestTileRequestProducesOneSpanTree would keep passing. What it changes is
// that the metrics middleware would then run *outside* the tracing one, take
// its duration observation off a request whose context has no span yet, and
// attach no exemplar at all.
func TestRequestExemplarNamesTheRequestSpan(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer2)
	c, _, _ := twoTierCache(t, "exemplarhot", "exemplardurable")
	a.SetCache(c)

	observer, err := prometheus.New(dict.Dict{})
	if err != nil {
		t.Fatalf("prometheus observer: %v", err)
	}
	a.SetObservability(observer)

	tracer, exporter := faketracer.New(t)
	tracer.Install()
	a.SetTracing(tracer)

	if _, _, err := doRequest(t, a, http.MethodGet, exemplarTileURI, nil); err != nil {
		t.Fatalf("traced request: %v", err)
	}

	root := rootSpan(t, exporter)

	exemplar := ttools.ExemplarLabels(t, promclient.DefaultGatherer, "shigola_api_duration_seconds",
		map[string]string{"handler": exemplarHandlerLabel})

	if got, want := exemplar["trace_id"], root.SpanContext.TraceID().String(); got != want {
		t.Errorf("request exemplar trace_id = %q, want %q", got, want)
	}

	if got, want := exemplar["span_id"], root.SpanContext.SpanID().String(); got != want {
		t.Errorf("request exemplar span_id = %q, want the request span %q", got, want)
	}
}

// rootSpan returns the one recorded span with no parent.
func rootSpan(t *testing.T, exporter *tracetest.InMemoryExporter) tracetest.SpanStub {
	t.Helper()

	var roots []tracetest.SpanStub
	for _, span := range exporter.GetSpans() {
		if !span.Parent.IsValid() {
			roots = append(roots, span)
		}
	}

	if len(roots) != 1 {
		t.Fatalf("%d root spans, want exactly 1 — the request", len(roots))
	}

	return roots[0]
}
