package prometheus

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MapColonies/shigola/internal/ttools"
)

// The differ these tests use lives in internal/ttools, with the reasoning for
// deriving the list rather than restating the rule. It is shared because the
// families the docs cover are not all declared in this package: see
// provider/postgis's own row.

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
			changed := ttools.RespelledBuckets(t, tc.buckets)

			// Before AssertRespelled, which fails fatally on a length
			// mismatch. A documented-unchanged boundary that starts moving
			// *is* a length mismatch, so checking afterwards would report the
			// generic list difference and never the specific boundary — the
			// one thing this loop exists to name.
			for _, le := range tc.unchanged {
				for _, entry := range changed {
					if strings.HasPrefix(entry, le+" -> ") {
						t.Errorf("%v was documented as unchanged but moved: %v", le, entry)
					}
				}
			}

			ttools.AssertRespelled(t, changed, tc.respelled)
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
// The fixture publishes the quantiles the Go collector publishes — 0, 0.25,
// 0.5, 0.75 and 1 — rather than registering the collector itself, whose
// constructor is deprecated in the vendored client and whose replacement lives
// in a package this tree does not vendor: registering one to prove a fact about
// the encoder would have meant vendor churn on a ticket whose acceptance
// criteria turn on vendor/ being untouched.
//
// A summary is the only way to publish a quantile label from this side, so the
// quantiles arrive here as Objectives. The Go collector reaches the same labels
// by another route — MustNewConstSummary over runtime.MemStats.PauseQuantiles,
// which has no objectives at all — and the epsilons below are therefore
// arbitrary. Only the label spelling is under test, and that is written by the
// encoder from the map keys.
func TestRespelledQuantileBoundaries(t *testing.T) {
	registry := prometheus.NewRegistry()
	summary := prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "test_quantile_seconds",
		Objectives: map[float64]float64{0: 0, 0.25: 0.25, 0.5: 0.05, 0.75: 0.02, 1: 0},
	})
	registry.MustRegister(summary)
	summary.Observe(0)

	ttools.AssertRespelled(t, ttools.Respelled(t, registry, "quantile"),
		[]string{"0 -> 0.0", "1 -> 1.0"})
}
