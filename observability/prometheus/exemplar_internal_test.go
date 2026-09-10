package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	tegolaCache "github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/fakelog"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/ttools"
)

// The trace fixture comes from internal/fakelog rather than internal/faketracer
// on purpose. What these tests need is a bare span context with a recognisable
// id, which is what fakelog holds — faketracer builds a whole SDK tracer
// provider, which is more than an exemplar reads and would put a fourth
// spelling of the same fixed id in the tree. See MAPCO-11494.

var exemplarKey = &tegolaCache.Key{MapName: "osm", Z: 6, X: 5, Y: 4}

// The label set a cache built with no observe-vars records a read under.
var getLabels = map[string]string{"sub_command": "get"}

// TestExemplarFromContext covers the three ways there is nothing to point at
// and the one way there is.
func TestExemplarFromContext(t *testing.T) {
	type tcase struct {
		ctx  context.Context
		want string // the trace id expected in the labels; empty means no exemplar
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got := exemplarFrom(tc.ctx)

			if tc.want == "" {
				if got != nil {
					t.Fatalf("exemplarFrom() = %v, want nil so the observation is recorded without one", got)
				}
				return
			}

			if got[exemplarTraceIDKey] != tc.want {
				t.Fatalf("exemplarFrom()[%q] = %q, want %q", exemplarTraceIDKey, got[exemplarTraceIDKey], tc.want)
			}
			if got[exemplarSpanIDKey] != fakelog.SpanIDHex {
				t.Errorf("exemplarFrom()[%q] = %q, want %q", exemplarSpanIDKey, got[exemplarSpanIDKey], fakelog.SpanIDHex)
			}
			if len(got) != 2 {
				t.Errorf("exemplarFrom() = %v, want the trace and span ids alone", got)
			}
		}
	}

	tests := map[string]tcase{
		"outside any trace": {ctx: context.Background()},
		// The zero trace id is what an uninitialised or stripped span context
		// looks like; pointing an exemplar at it would link nowhere.
		"invalid span context": {ctx: fakelog.ContextWith(trace.TraceID{}, fakelog.SpanID, true)},
		// The one that is a judgement rather than a validity check: the trace
		// is real but was never exported, so a link to it resolves to nothing.
		"valid but unsampled": {ctx: fakelog.TracedContext(false)},
		"sampled":             {ctx: fakelog.TracedContext(true), want: fakelog.TraceIDHex},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestExemplarFitsTheRuneLimit is the acceptance criterion that the label set
// stays inside what the client allows, checked the way the client checks it.
//
// ObserveWithExemplar panics past ExemplarMaxRunes rather than returning an
// error, so the failure mode this guards is a tile server that dies on its
// first traced cache read. A real observation, not just a rune count: the
// limit counts label names and values together, which is easy to get wrong by
// hand and impossible to get wrong this way.
func TestExemplarFitsTheRuneLimit(t *testing.T) {
	labels := exemplarFrom(fakelog.TracedContext(true))

	runes := 0
	for name, value := range labels {
		runes += len([]rune(name)) + len([]rune(value))
	}
	if runes > prometheus.ExemplarMaxRunes {
		t.Fatalf("exemplar labels are %d runes, over the limit of %d", runes, prometheus.ExemplarMaxRunes)
	}
	t.Logf("exemplar labels are %d of the %d runes allowed", runes, prometheus.ExemplarMaxRunes)

	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "test_exemplar_limit_seconds",
		Buckets: cacheDurationBuckets,
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ObserveWithExemplar panicked on %v: %v", labels, r)
		}
	}()

	histogram.(prometheus.ExemplarObserver).ObserveWithExemplar(0.002, labels)
}

// TestCacheDurationCarriesTheExemplar is the cache half of the ticket: a tier
// read observed inside a sampled trace names it.
func TestCacheDurationCarriesTheExemplar(t *testing.T) {
	registry := prometheus.NewRegistry()
	tier := faketier.New("hot")
	c := newCache(registry, "test_exemplar_cache", nil, tier)

	//nolint:errcheck // a miss; the exemplar on the duration observation is the subject
	c.Get(fakelog.TracedContext(true), exemplarKey)

	exemplar := ttools.ExemplarLabels(t, registry, "test_exemplar_cache_duration_seconds", getLabels)
	if got := exemplar[exemplarTraceIDKey]; got != fakelog.TraceIDHex {
		t.Fatalf("cache duration exemplar names trace %q, want %q", got, fakelog.TraceIDHex)
	}
}

// TestCacheDurationOutsideATraceHasNoExemplar is the other acceptance
// criterion: an untraced observation records normally, with nothing attached.
func TestCacheDurationOutsideATraceHasNoExemplar(t *testing.T) {
	registry := prometheus.NewRegistry()
	tier := faketier.New("hot")
	c := newCache(registry, "test_plain_cache", nil, tier)

	//nolint:errcheck // as above
	c.Get(context.Background(), exemplarKey)

	assertRecordedWithoutExemplar(t, registry, "test_plain_cache_duration_seconds", getLabels)
}

// TestHTTPDurationCarriesTheExemplar is the request half. The span context
// reaches the middleware on the request, which is how it arrives in
// production: the tracing handler is installed outside the metrics one and has
// already replaced the request by the time this runs.
func TestHTTPDurationCarriesTheExemplar(t *testing.T) {
	registry := prometheus.NewRegistry()
	handler := newHttpHandler(registry, "test_exemplar_api", "", nil)

	instrumented := handler.InstrumentedHttpHandler(http.MethodGet, "/collections/osm/tiles",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	request := httptest.NewRequest(http.MethodGet, "/collections/osm/tiles", nil).
		WithContext(fakelog.TracedContext(true))
	instrumented.ServeHTTP(httptest.NewRecorder(), request)

	exemplar := ttools.ExemplarLabels(t, registry, "test_exemplar_api_duration_seconds",
		map[string]string{"handler": "/collections/osm/tiles"})
	if got := exemplar[exemplarTraceIDKey]; got != fakelog.TraceIDHex {
		t.Fatalf("http duration exemplar names trace %q, want %q", got, fakelog.TraceIDHex)
	}
}

// TestHTTPDurationOutsideATraceHasNoExemplar is the request half of the same
// acceptance criterion the cache half above covers. Worth both: the two paths
// attach their exemplar through entirely different machinery — one calls
// ObserveWithExemplar itself, the other hands the hook to promhttp — so
// neither's behaviour outside a trace tells you the other's.
func TestHTTPDurationOutsideATraceHasNoExemplar(t *testing.T) {
	registry := prometheus.NewRegistry()
	handler := newHttpHandler(registry, "test_plain_api", "", nil)

	instrumented := handler.InstrumentedHttpHandler(http.MethodGet, "/collections/osm/tiles",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	instrumented.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/collections/osm/tiles", nil))

	assertRecordedWithoutExemplar(t, registry, "test_plain_api_duration_seconds",
		map[string]string{"handler": "/collections/osm/tiles"})
}

// TestExemplarReachesTheExposition is the one that would have made every other
// test in this file worthless.
//
// Exemplars are recorded in the client either way, but only the OpenMetrics
// encoding transmits them — the classic text encoder has no syntax for them and
// drops them silently. So this scrapes the handler the metrics route actually
// serves, with the Accept header Prometheus sends, and looks for the exemplar
// in the bytes.
func TestExemplarReachesTheExposition(t *testing.T) {
	registry := prometheus.NewRegistry()
	tier := faketier.New("hot")
	c := newCache(registry, "test_scraped_cache", nil, tier)

	//nolint:errcheck // as above
	c.Get(fakelog.TracedContext(true), exemplarKey)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	// Prometheus's own scrape header, verbatim. The version list matters: the
	// vendored expfmt negotiates OpenMetrics 0.0.1 only, so a header offering
	// 1.0.0 alone falls back to the classic text format and the exemplars
	// vanish. Prometheus offers both, which is why this works in production.
	request.Header.Set("Accept", "application/openmetrics-text;version=1.0.0,"+
		"application/openmetrics-text;version=0.0.1;q=0.75,"+
		"text/plain;version=0.0.4;q=0.5,*/*;q=0.1")
	recorder := httptest.NewRecorder()
	metricsHandler(registry, registry).ServeHTTP(recorder, request)

	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "openmetrics-text") {
		t.Fatalf("Content-Type = %q, want the OpenMetrics encoding that carries exemplars", contentType)
	}

	want := exemplarTraceIDKey + `="` + fakelog.TraceIDHex + `"`
	if body := recorder.Body.String(); !strings.Contains(body, want) {
		t.Fatalf("the exposition does not contain %q; exemplars are recorded but never scraped", want)
	}
}

// assertRecordedWithoutExemplar is the acceptance criterion for an observation
// made outside a trace: recorded exactly as it would have been, carrying
// nothing. Both halves matter — a guard that dropped the observation entirely
// would satisfy "no exemplar" while losing the measurement.
//
// Shared by the cache and HTTP cases, which assert the same thing about two
// entirely different mechanisms: one calls ObserveWithExemplar itself, the
// other hands the hook to promhttp.
func assertRecordedWithoutExemplar(t *testing.T, gatherer prometheus.Gatherer, name string, labels map[string]string) {
	t.Helper()

	histogram := ttools.HistogramSample(t, gatherer, name, labels)

	if histogram.GetSampleCount() != 1 {
		t.Fatalf("sample count = %d, want the observation to have been recorded anyway", histogram.GetSampleCount())
	}

	for _, bucket := range histogram.GetBucket() {
		if bucket.GetExemplar() != nil {
			t.Fatalf("bucket le=%v carries an exemplar outside a trace", bucket.GetUpperBound())
		}
	}
}
