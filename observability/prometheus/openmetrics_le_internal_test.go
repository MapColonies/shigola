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
	families := map[string][]float64{
		"shigola_cache{,_tier}_duration_seconds":    cacheDurationBuckets,
		"shigola_api_duration_seconds":              httpHandlerDurationBuckets,
		"shigola_cache{,_tier}_response_size_bytes": cacheResponseSizeBuckets,
		"shigola_api_response_size_bytes":           httpHandlerResponseSizeBuckets,
	}

	want := map[string][]string{
		"shigola_cache{,_tier}_duration_seconds":    {"1 -> 1.0", "5 -> 5.0"},
		"shigola_api_duration_seconds":              {"1 -> 1.0", "5 -> 5.0", "10 -> 10.0"},
		"shigola_cache{,_tier}_response_size_bytes": {"1024 -> 1024.0", "5120 -> 5120.0", "25600 -> 25600.0", "102400 -> 102400.0", "256000 -> 256000.0", "512000 -> 512000.0", "1.048576e+06 -> unchanged", "5.24288e+06 -> unchanged"},
		"shigola_api_response_size_bytes":           {"512000 -> 512000.0", "1.048576e+06 -> unchanged", "5.24288e+06 -> unchanged"},
	}

	fn := func(buckets []float64, want []string) func(*testing.T) {
		return func(t *testing.T) {
			var changed []string
			for _, le := range buckets {
				before, after := classicFloat(le), openMetricsFloat(le)
				if before != after {
					changed = append(changed, before+" -> "+after)
				}
			}

			// Only the respellings are compared; the "unchanged" entries above
			// record the two that surprise, and are dropped here.
			var wantChanged []string
			for _, entry := range want {
				if !bytes.Contains([]byte(entry), []byte("unchanged")) {
					wantChanged = append(wantChanged, entry)
				}
			}

			if len(changed) != len(wantChanged) {
				t.Fatalf("respelled boundaries = %v, documented %v", changed, wantChanged)
			}
			for i := range changed {
				if changed[i] != wantChanged[i] {
					t.Errorf("respelled boundary %d = %q, documented %q", i, changed[i], wantChanged[i])
				}
			}
		}
	}

	for name, buckets := range families {
		t.Run(name, fn(buckets, want[name]))
	}
}
