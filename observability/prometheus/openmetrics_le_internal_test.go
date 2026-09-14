package prometheus

import (
	"net/http"
	"net/http/httptest"
	"regexp"
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

// scrapeLabel serves a registry in one exposition format and returns the values
// pattern captures, in the order they were written. openMetrics picks the
// encoder; false is the classic text format this route served before exemplars.
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

			classic := scrapeLabel(t, registry, lePattern, false)
			openMetrics := scrapeLabel(t, registry, lePattern, true)

			if len(classic) != len(openMetrics) {
				t.Fatalf("%d le labels classic, %d under OpenMetrics; the two are not comparable",
					len(classic), len(openMetrics))
			}

			var changed []string
			unchanged := map[string]bool{}
			for i := range classic {
				if classic[i] != openMetrics[i] {
					changed = append(changed, classic[i]+" -> "+openMetrics[i])
					continue
				}
				unchanged[classic[i]] = true
			}

			if len(changed) != len(tc.respelled) {
				t.Fatalf("respelled boundaries = %v, documented %v", changed, tc.respelled)
			}
			for i := range changed {
				if changed[i] != tc.respelled[i] {
					t.Errorf("respelled boundary %d = %q, documented %q", i, changed[i], tc.respelled[i])
				}
			}

			for _, le := range tc.unchanged {
				if !unchanged[le] {
					t.Errorf("%v was documented as unchanged but the two formats spell it differently", le)
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
// the format on also respells series this package never touches — including
// go_gc_duration_seconds, which every Go process publishes and which plenty of
// dashboards pin a quantile on. The docs said "le" and named only shigola's
// families until this test was written.
func TestRespelledQuantileBoundaries(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector())

	classic := scrapeLabel(t, registry, quantilePattern, false)
	openMetrics := scrapeLabel(t, registry, quantilePattern, true)

	if len(classic) == 0 {
		t.Fatal("the Go collector published no quantile labels; this test proved nothing")
	}
	if len(classic) != len(openMetrics) {
		t.Fatalf("%d quantile labels classic, %d under OpenMetrics", len(classic), len(openMetrics))
	}

	var changed []string
	for i := range classic {
		if classic[i] != openMetrics[i] {
			changed = append(changed, classic[i]+" -> "+openMetrics[i])
		}
	}

	want := []string{"0 -> 0.0", "1 -> 1.0"}
	if len(changed) != len(want) {
		t.Fatalf("respelled quantiles = %v, documented %v", changed, want)
	}
	for i := range changed {
		if changed[i] != want[i] {
			t.Errorf("respelled quantile %d = %q, documented %q", i, changed[i], want[i])
		}
	}
}
