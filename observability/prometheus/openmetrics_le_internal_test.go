package prometheus

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// This file is the evidence for a claim the docs make and an operator acts on:
// which bucket boundaries are respelled by serving OpenMetrics, and therefore
// which `le` series change identity. Getting that list wrong understates an
// upgrade break — the first version of these docs named a boundary that is not
// affected at all — so it is derived rather than worked out by hand.
//
// Derived by *scraping both encoders* rather than by reimplementing the rule
// they apply. An earlier version of this test copied expfmt's unexported
// writeOpenMetricsFloat, which put the docs one vendored change away from being
// wrong with the test still green; the copy also silently dropped that
// function's NaN and ±Inf cases. Nothing here knows the rule, so a change to it
// shows up as a failure naming the boundary that moved.

// lePattern and quantilePattern pull a bucket or quantile label out of an
// exposition line.
var (
	lePattern       = regexp.MustCompile(`le="([^"]+)"`)
	quantilePattern = regexp.MustCompile(`quantile="([^"]+)"`)
)

// The two exposition formats, named so a call site says which it means rather
// than passing a bare true or false.
const (
	openMetricsText = true
	classicText     = false
)

// scrapeLabel serves a registry in one exposition format and returns the values
// pattern captures, in the order they were written.
func scrapeLabel(t *testing.T, registry *prometheus.Registry, pattern *regexp.Regexp, openMetrics bool) []string {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if openMetrics {
		// The one Accept header that reaches the OpenMetrics encoder in this
		// vendored expfmt, which negotiates version 0.0.1 only.
		request.Header.Set("Accept", "application/openmetrics-text;version=0.0.1")
	}

	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{EnableOpenMetrics: openMetrics}).
		ServeHTTP(recorder, request)

	return matches(pattern, recorder.Body.String())
}

// matches returns the first capture group of every match, in order.
func matches(pattern *regexp.Regexp, body string) []string {
	var found []string
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		found = append(found, match[1])
	}

	return found
}

// respelled serves registry under both exposition formats, pairs the label
// values written by each, and returns the ones the two spell differently as
// "before -> after".
//
// The pairing is positional, which is why the lengths are checked first: the
// two encoders write the same samples in the same order, and a length mismatch
// means they no longer do, which would make every comparison below meaningless
// rather than merely wrong.
func respelled(t *testing.T, registry *prometheus.Registry, pattern *regexp.Regexp) []string {
	t.Helper()

	classic := scrapeLabel(t, registry, pattern, classicText)
	openMetrics := scrapeLabel(t, registry, pattern, openMetricsText)

	if len(classic) == 0 {
		t.Fatal("no matching labels in the exposition; this test would prove nothing")
	}
	if len(classic) != len(openMetrics) {
		t.Fatalf("%d labels classic, %d under OpenMetrics; the two are not comparable",
			len(classic), len(openMetrics))
	}

	var changed []string
	for i := range classic {
		if classic[i] != openMetrics[i] {
			changed = append(changed, classic[i]+" -> "+openMetrics[i])
		}
	}

	return changed
}

// assertRespelled compares what actually moved against what the docs say did.
func assertRespelled(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("respelled = %v, documented %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("respelled %d = %q, documented %q", i, got[i], want[i])
		}
	}
}

// TestRespelledBucketBoundaries scrapes each bucket set under both exposition
// formats and reports every boundary the two spell differently.
func TestRespelledBucketBoundaries(t *testing.T) {
	type tcase struct {
		buckets []float64
		// respelled is every boundary whose le label changed, as
		// "before -> after". unchanged records the boundaries that surprise by
		// *not* changing, so the reason sits next to the list they are absent
		// from.
		respelled []string
		unchanged []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			registry := prometheus.NewRegistry()
			histogram := prometheus.NewHistogram(prometheus.HistogramOpts{
				Name:    "test_le_seconds",
				Buckets: tc.buckets,
			})
			registry.MustRegister(histogram)
			// One observation, so every bucket is written out.
			histogram.Observe(0)

			changed := respelled(t, registry, lePattern)
			assertRespelled(t, changed, tc.respelled)

			for _, le := range tc.unchanged {
				for _, entry := range changed {
					if strings.HasPrefix(entry, le+" -> ") {
						t.Errorf("%v was documented as unchanged but moved: %v", le, entry)
					}
				}
			}
		}
	}

	tests := map[string]tcase{
		"shigola_cache{,_tier}_duration_seconds": {
			buckets:   cacheDurationBuckets,
			respelled: []string{"1 -> 1.0", "5 -> 5.0"},
			// 2.5 already contains a ".", which is the whole rule.
			unchanged: []string{"2.5"},
		},
		"shigola_api_duration_seconds": {
			buckets:   httpHandlerDurationBuckets,
			respelled: []string{"1 -> 1.0", "5 -> 5.0", "10 -> 10.0"},
			unchanged: []string{"2.5"},
		},
		"shigola_cache{,_tier}_response_size_bytes": {
			buckets: cacheResponseSizeBuckets,
			respelled: []string{
				"1024 -> 1024.0", "5120 -> 5120.0", "25600 -> 25600.0",
				"102400 -> 102400.0", "256000 -> 256000.0", "512000 -> 512000.0",
			},
			// The megabyte boundaries render in exponent form, so they already
			// contain an "e". This is why the list stops at 512000.
			unchanged: []string{"1.048576e+06", "5.24288e+06"},
		},
		"shigola_api_response_size_bytes": {
			buckets:   httpHandlerResponseSizeBuckets,
			respelled: []string{"512000 -> 512000.0"},
			unchanged: []string{"1.048576e+06", "5.24288e+06"},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestRespelledQuantileBoundaries is the half the bucket table misses.
//
// The respelling is a property of the OpenMetrics *encoder*, not of histograms:
// it writes summary quantile labels through the same float writer. So turning
// the format on also respells series this package never touches — most visibly
// go_gc_duration_seconds, which every Go process publishes and which plenty of
// dashboards pin a quantile on. The docs said "le" and named only shigola's
// families until this test was written.
//
// The summary here carries the Go collector's own objectives rather than
// registering the collector itself, whose constructor is deprecated in the
// vendored client and whose replacement lives in a package this tree does not
// vendor. Registering one to prove a fact about the encoder would have meant
// vendor churn on a ticket whose acceptance criteria turn on vendor/ being
// untouched.
func TestRespelledQuantileBoundaries(t *testing.T) {
	registry := prometheus.NewRegistry()
	summary := prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "test_quantile_seconds",
		Objectives: map[float64]float64{0: 0, 0.25: 0.25, 0.5: 0.05, 0.75: 0.02, 1: 0},
	})
	registry.MustRegister(summary)
	summary.Observe(0)

	assertRespelled(t, respelled(t, registry, quantilePattern),
		[]string{"0 -> 0.0", "1 -> 1.0"})
}
