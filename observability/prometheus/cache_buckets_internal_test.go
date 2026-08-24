package prometheus

import "testing"

// bucketIndex returns the index of the first bucket whose upper bound holds v,
// or len(buckets) for the implicit +Inf bucket. Two observations that share an
// index are indistinguishable to histogram_quantile, which interpolates within
// a bucket using the observation count alone.
func bucketIndex(buckets []float64, v float64) int {
	for i, le := range buckets {
		if v <= le {
			return i
		}
	}

	return len(buckets)
}

// representative read latencies, one per kind of tier a chain can hold.
var tierLatencies = []struct {
	name    string
	seconds float64
}{
	{"memory", 0.000001},
	{"file", 0.0004},
	{"redis", 0.0015},
	{"s3 same region", 0.06},
	{"s3 further away", 0.2},
}

// TestCacheDurationBucketsSeparateTierKinds is the regression this bucket set
// exists for: a layered cache is only diagnosable if a quantile can tell a hot
// tier from a cold one.
func TestCacheDurationBucketsSeparateTierKinds(t *testing.T) {
	seen := make(map[int]string, len(tierLatencies))

	for _, tier := range tierLatencies {
		t.Run(tier.name, func(t *testing.T) {
			i := bucketIndex(cacheDurationBuckets, tier.seconds)

			if other, clash := seen[i]; clash {
				t.Errorf("%q (%vs) shares bucket %d with %q; a quantile cannot tell them apart",
					tier.name, tier.seconds, i, other)
				return
			}

			seen[i] = tier.name
		})
	}
}

// TestHTTPDurationBucketsCollapseEveryTier pins the defect these buckets
// replaced, so that "simplify by reusing the HTTP set" reintroduces a failing
// test rather than a silent regression. Every tier kind lands in bucket 0.
func TestHTTPDurationBucketsCollapseEveryTier(t *testing.T) {
	for _, tier := range tierLatencies {
		t.Run(tier.name, func(t *testing.T) {
			if i := bucketIndex(httpHandlerDurationBuckets, tier.seconds); i != 0 {
				t.Fatalf("expected the HTTP buckets to collapse %vs into bucket 0, got %d", tier.seconds, i)
			}
		})
	}
}

// TestCacheResponseSizeBucketsSeparateTileSizes covers the same defect on the
// size histogram, whose HTTP-derived floor of 500KB sat above almost every
// vector tile.
func TestCacheResponseSizeBucketsSeparateTileSizes(t *testing.T) {
	tiles := []struct {
		name  string
		bytes float64
	}{
		{"sparse tile", 8 * 1024},
		{"typical tile", 120 * 1024},
		{"dense tile", 2 * 1024 * 1024},
	}

	seen := make(map[int]string, len(tiles))

	for _, tile := range tiles {
		t.Run(tile.name, func(t *testing.T) {
			i := bucketIndex(cacheResponseSizeBuckets, tile.bytes)

			if other, clash := seen[i]; clash {
				t.Errorf("%q (%v bytes) shares bucket %d with %q", tile.name, tile.bytes, i, other)
				return
			}

			seen[i] = tile.name
		})
	}
}

// TestCacheBucketsAreOrdered guards the boundaries themselves: prometheus
// requires strictly increasing buckets and panics at registration otherwise,
// which would take the process down at startup rather than in a test.
func TestCacheBucketsAreOrdered(t *testing.T) {
	for name, buckets := range map[string][]float64{
		"duration":      cacheDurationBuckets,
		"response size": cacheResponseSizeBuckets,
	} {
		t.Run(name, func(t *testing.T) {
			for i := 1; i < len(buckets); i++ {
				if buckets[i] <= buckets[i-1] {
					t.Fatalf("bucket %d (%v) does not exceed bucket %d (%v)",
						i, buckets[i], i-1, buckets[i-1])
				}
			}
		})
	}
}
