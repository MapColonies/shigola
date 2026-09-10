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

// lePattern pulls the le label out of an exposition line.
var lePattern = regexp.MustCompile(`le="([^"]+)"`)

// scrapeLE serves a registry in one exposition format and returns the le label
// values it wrote, in bucket order. openMetrics picks the encoder; false is the
// classic text format this route served before exemplars.
func scrapeLE(t *testing.T, registry *prometheus.Registry, openMetrics bool) []string {
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

	var labels []string
	for _, match := range lePattern.FindAllStringSubmatch(recorder.Body.String(), -1) {
		labels = append(labels, match[1])
	}

	return labels
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

			classic := scrapeLE(t, registry, false)
			openMetrics := scrapeLE(t, registry, true)

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
