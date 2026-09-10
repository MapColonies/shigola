package prometheus

import (
	"bytes"
	"strconv"
	"testing"
)

// This file is the evidence for a claim the docs make and an operator acts on:
// which bucket boundaries are respelled by serving OpenMetrics, and therefore
// which `le` series change identity. Getting that list wrong understates an
// upgrade break, so it is derived here rather than worked out by hand.
//
// openMetricsFloat reproduces expfmt.writeOpenMetricsFloat, which is
// unexported: shortest 'g' formatting, with ".0" appended when the result
// contains neither "." nor "e".
func openMetricsFloat(f float64) string {
	switch {
	case f == 1:
		return "1.0"
	case f == 0:
		return "0.0"
	case f == -1:
		return "-1.0"
	}

	b := strconv.AppendFloat(nil, f, 'g', -1, 64)
	if !bytes.ContainsAny(b, "e.") {
		b = append(b, '.', '0')
	}

	return string(b)
}

// classicFloat is expfmt.writeFloat, the format served before OpenMetrics.
func classicFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// TestRespelledBucketBoundaries lists every boundary whose `le` label changed,
// so the set the docs name can be compared against the set that exists.
//
// The rule catches integer-looking values only, which is narrower than it
// first appears: 2.5 already contains a ".", and 5242880 renders as
// "5.24288e+06" and already contains an "e". It is also *not* confined to the
// duration families — the response-size boundaries are whole numbers of bytes
// and nearly all of them are respelled.
func TestRespelledBucketBoundaries(t *testing.T) {
	type tcase struct {
		buckets []float64
		// respelled is every boundary whose le label changed, as
		// "before -> after". unchanged records the boundaries that surprise by
		// *not* changing, so the reason is written down next to the list it is
		// absent from.
		respelled []string
		unchanged []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			var changed []string
			for _, le := range tc.buckets {
				before, after := classicFloat(le), openMetricsFloat(le)
				if before != after {
					changed = append(changed, before+" -> "+after)
				}
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
				if got := openMetricsFloat(mustParse(t, le)); got != le {
					t.Errorf("%v was documented as unchanged but is written %q", le, got)
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

// mustParse reads a boundary back out of its rendered form, so the unchanged
// lists above can be written the way the exposition writes them.
func mustParse(t *testing.T, s string) float64 {
	t.Helper()

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}

	return f
}
