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
var readLabels = map[string]string{"sub_command": "get"}

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

	// Pinned, not just logged: the exact figure is quoted in tracing/README.md
	// and in the PR, and a quoted number nothing asserts is a number that goes
	// stale. Change it here and there together — a rise is only a problem at
	// the limit, but it should be a deliberate edit either way.
	const documented = 63
	if runes != documented {
		t.Errorf("exemplar labels are %d runes; tracing/README.md says %d", runes, documented)
	}

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

// TestDurationExemplars is the ticket's two halves against its two states —
// the cache path and the HTTP path, each inside a sampled trace and outside any
// trace.
//
// One table rather than four functions because the four rows assert one claim
// between them, and the interesting comparison is down the columns: the two
// paths attach their exemplar through entirely different machinery — the cache
// calls ObserveWithExemplar itself, the HTTP side hands the hook to promhttp —
// so neither's behaviour tells you the other's, in either state.
func TestDurationExemplars(t *testing.T) {
	type tcase struct {
		// observe makes one duration observation against the registry, in the
		// context the row is about, and returns the family and label set it
		// landed on.
		observe func(*testing.T, *prometheus.Registry) (string, map[string]string)
		traced  bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			registry := prometheus.NewRegistry()

			family, labels := tc.observe(t, registry)

			if !tc.traced {
				assertRecordedWithoutExemplar(t, registry, family, labels)

				return
			}

			exemplar := ttools.ExemplarLabels(t, registry, family, labels)
			if got := exemplar[exemplarTraceIDKey]; got != fakelog.TraceIDHex {
				t.Errorf("exemplar names trace %q, want %q", got, fakelog.TraceIDHex)
			}
			if got := exemplar[exemplarSpanIDKey]; got != fakelog.SpanIDHex {
				t.Errorf("exemplar names span %q, want %q", got, fakelog.SpanIDHex)
			}
		}
	}

	// cacheRead observes one tier read through the metric wrapper.
	cacheRead := func(prefix string, ctx context.Context) func(*testing.T, *prometheus.Registry) (string, map[string]string) {
		return func(t *testing.T, registry *prometheus.Registry) (string, map[string]string) {
			c := newCache(registry, prefix, nil, faketier.New("hot"))

			//nolint:errcheck // a miss; the exemplar on the duration observation is the subject
			c.Get(ctx, exemplarKey)

			return prefix + "_duration_seconds", readLabels
		}
	}

	// request serves one request through the metrics middleware. The span
	// context arrives on the request, which is how it arrives in production:
	// the tracing handler is installed outside the metrics one and has already
	// replaced the request by the time this runs.
	request := func(prefix string, ctx context.Context) func(*testing.T, *prometheus.Registry) (string, map[string]string) {
		return func(t *testing.T, registry *prometheus.Registry) (string, map[string]string) {
			const route = "/collections/osm/tiles"

			handler := newHttpHandler(registry, prefix, "", nil)
			instrumented := handler.InstrumentedHttpHandler(http.MethodGet, route,
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

			r := httptest.NewRequest(http.MethodGet, route, nil)
			if ctx != nil {
				r = r.WithContext(ctx)
			}
			instrumented.ServeHTTP(httptest.NewRecorder(), r)

			return prefix + "_duration_seconds", map[string]string{"handler": route}
		}
	}

	tests := map[string]tcase{
		"a cache read in a sampled trace": {
			observe: cacheRead("test_exemplar_cache", fakelog.TracedContext(true)),
			traced:  true,
		},
		"a cache read outside any trace": {
			observe: cacheRead("test_plain_cache", context.Background()),
		},
		"a request in a sampled trace": {
			observe: request("test_exemplar_api", fakelog.TracedContext(true)),
			traced:  true,
		},
		"a request outside any trace": {
			observe: request("test_plain_api", nil),
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
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
