package postgis

import (
	"testing"

	"github.com/MapColonies/shigola/internal/ttools"
)

// TestRespelledQueryBuckets is this package's row in a table the docs keep.
//
// Serving OpenMetrics is what makes exemplars reach a scrape at all
// (MAPCO-11496), and the encoder respells any bucket boundary whose shortest
// rendering contains neither "." nor "e" — so `le="1"` becomes `le="1.0"`, a
// different series as far as Prometheus is concerned, and any dashboard panel
// or recording rule pinning an exact `le` stops matching.
//
// observability/prometheus has the same test over the families declared there,
// and the documented list of what moved was assembled from it alone. These two
// families were missed for exactly that reason: their buckets are declared
// here, so a differ private to that package could not see them while still
// reporting that it derived the list from the bucket sets. Hence a row here.
//
// No database: the boundaries are a package-level var, and what is under test
// is how the encoder writes them, not anything the provider does with them.
// That matters because every other test in this package is gated behind
// RUN_POSTGIS_TESTS and would not run in a normal `go test ./...`.
func TestRespelledQueryBuckets(t *testing.T) {
	// shigola_mvt_provider_sql_query_seconds and
	// shigola_provider_sql_query_seconds share these boundaries, so one row
	// covers both. .1 is untouched: it renders as "0.1", which already has
	// the "." the rule looks for.
	ttools.AssertRespelled(t, ttools.RespelledBuckets(t, queryDurationBuckets),
		[]string{"1 -> 1.0", "5 -> 5.0", "20 -> 20.0"})
}
