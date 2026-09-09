package atlas

import (
	"context"
	"slices"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/provider/test"
	"github.com/MapColonies/shigola/tms"
	"github.com/MapColonies/shigola/tracing"
	"github.com/go-spatial/geom/slippy"
)

// Tier names have to be unique per test here for the same reason they do in
// cache_observability_test.go: the prometheus observer registers against the
// process-wide registry, so a name reused across tests shares a series.

// tierAttrs returns the tier names the given spans carry, sorted.
func tierAttrs(spans []tracetest.SpanStub) []string {
	tiers := make([]string, 0, len(spans))
	for _, span := range spans {
		if tier := faketracer.StringAttr(span, tracing.AttrCacheTier); tier != "" {
			tiers = append(tiers, tier)
		}
	}
	slices.Sort(tiers)

	return tiers
}

// familyNames is every metric family the process publishes right now.
func familyNames(t *testing.T) []string {
	t.Helper()

	families, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.GetName())
	}
	slices.Sort(names)

	return names
}

// TestTierSpansPerTier is the span half of what the layered cache exists to
// expose: a read that walked the chain says so, tier by tier, rather than
// reporting one opaque cache lookup.
func TestTierSpansPerTier(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")
	durable.Seed(obsKey, []byte("tile"))

	tracer, exporter := faketracer.New(t)

	a := &Atlas{}
	a.SetCache(tieredCache(t, "trhot1", "trdurable1", "trhot1", "trdurable1", hot, durable, 0))
	a.SetTracing(tracer)

	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); err != nil || !hit {
		t.Fatalf("Get() = hit %v, err %v; want a hit from the durable tier", hit, err)
	}

	whole := faketracer.SpansNamed(exporter, tracing.SpanCacheGet)
	if len(whole) != 1 {
		t.Fatalf("%d %v spans, want 1", len(whole), tracing.SpanCacheGet)
	}

	tiers := faketracer.SpansNamed(exporter, tracing.SpanTierGet)
	if got, want := tierAttrs(tiers), []string{"trdurable1", "trhot1"}; !slices.Equal(got, want) {
		t.Errorf("tier spans = %v, want %v", got, want)
	}

	// Nesting is what makes the tree readable: a tier read has to hang off the
	// cache read that caused it, or Tempo shows a flat list of lookups with no
	// way to tell which request each belonged to.
	for _, span := range tiers {
		if span.Parent.SpanID() != whole[0].SpanContext.SpanID() {
			t.Errorf("tier span %v is not a child of the %v span", tierAttrs([]tracetest.SpanStub{span}), tracing.SpanCacheGet)
		}
	}
}

// TestMetricsAreUnaffectedByTracing is the acceptance criterion, checked rather
// than asserted in prose.
//
// Tracing runs alongside the prometheus observer and must be invisible to it:
// no metric family gained or lost, and the same counters moving by the same
// amounts for the same work. A tracing backend that registered a collector of
// its own would fail the first; one that displaced the metric wrapper while
// re-instrumenting the chain would fail the second.
//
// It covers the cache path only. The other way tracing could grow a metric
// family is otelhttp publishing HTTP metrics of its own through a global OTEL
// meter provider, and no request is issued here — that half is
// server.TestTracedRequestPublishesNoNewMetrics.
func TestMetricsAreUnaffectedByTracing(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")

	a := &Atlas{}
	a.SetCache(tieredCache(t, "trhot2", "trdurable2", "trhot2", "trdurable2", hot, durable, 0))
	a.SetObservability(newObserver(t))

	const misses = "shigola_cache_tier_misses_total"
	hotLabels := map[string]string{"tier": "trhot2", "sub_command": "get"}

	// One read with metrics alone, before the snapshot. A prometheus *Vec
	// publishes a family only once some label set on it has been observed, so
	// a snapshot taken before this read would show the first read's own
	// families as though tracing had introduced them.
	//nolint:errcheck // a miss on both tiers; the metric is what is asserted
	a.GetCache().Get(context.Background(), obsKey)

	familiesBefore := familyNames(t)
	missesBefore := counter(t, misses, hotLabels)

	tracer, exporter := faketracer.New(t)
	a.SetTracing(tracer)

	//nolint:errcheck // as above
	a.GetCache().Get(context.Background(), obsKey)

	added, lost := familyDiff(familiesBefore, familyNames(t))
	if len(added) > 0 {
		t.Errorf("tracing published new metric families: %v", added)
	}
	if len(lost) > 0 {
		t.Errorf("tracing removed metric families: %v", lost)
	}

	if got := counter(t, misses, hotLabels) - missesBefore; got != 1 {
		t.Errorf("%v moved by %v over one traced read, want 1", misses, got)
	}

	// Without this the test would pass just as happily if tracing had not been
	// wired into the chain at all, which is the failure it is least likely to
	// notice on its own.
	if len(faketracer.SpansNamed(exporter, tracing.SpanTierGet)) == 0 {
		t.Fatal("no tier spans recorded, so this test proved nothing about metrics coexisting with tracing")
	}
}

// familyDiff reports metric families that appeared and disappeared between two
// snapshots.
//
// Both directions, because the claim being checked is that tracing leaves the
// metrics exactly as they were: a family silently *lost* — an observer displaced
// while the chain was re-instrumented — would break every dashboard reading it
// just as thoroughly as a spurious new one.
func familyDiff(before, after []string) (added, lost []string) {
	for _, name := range after {
		if !slices.Contains(before, name) {
			added = append(added, name)
		}
	}
	for _, name := range before {
		if !slices.Contains(after, name) {
			lost = append(lost, name)
		}
	}

	return added, lost
}

// TestInstrumentationIsIdempotent covers the case the two setters make
// reachable: each re-derives the chain's instrumentation, and either one
// nesting a second wrapper inside the first would double every count and emit
// two spans per read, each claiming to be the whole operation.
func TestInstrumentationIsIdempotent(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")

	tracer, exporter := faketracer.New(t)

	a := &Atlas{}
	a.SetCache(tieredCache(t, "trhot3", "trdurable3", "trhot3", "trdurable3", hot, durable, 0))

	// Every order, twice: tracing then metrics then tracing again.
	a.SetTracing(tracer)
	a.SetObservability(newObserver(t))
	a.SetTracing(tracer)

	const misses = "shigola_cache_tier_misses_total"
	hotLabels := map[string]string{"tier": "trhot3", "sub_command": "get"}
	before := counter(t, misses, hotLabels)

	//nolint:errcheck // a miss; the counts are what is asserted
	a.GetCache().Get(context.Background(), obsKey)

	if got := counter(t, misses, hotLabels) - before; got != 1 {
		t.Errorf("%v moved by %v over one read, want 1 — the metric wrapper is nested", misses, got)
	}
	if got := len(faketracer.SpansNamed(exporter, tracing.SpanCacheGet)); got != 1 {
		t.Errorf("%d %v spans for one read, want 1 — the tracing wrapper is nested", got, tracing.SpanCacheGet)
	}
	if got := len(faketracer.SpansNamed(exporter, tracing.SpanTierGet)); got != 2 {
		t.Errorf("%d %v spans for one read of a two-tier chain, want 2", got, tracing.SpanTierGet)
	}
}

// TestTracingDoesNotStripTheDetachmentDecorator is the assertion its
// observability sibling makes for the other backend.
//
// Stripping walks down through wrappers to re-derive instrumentation, and the
// decorators cache.For applies — the write pool, the read deadlines — must
// survive that. If tracing peeled one off, every cache write would go back on
// the response path and nothing would fail visibly.
func TestTracingDoesNotStripTheDetachmentDecorator(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")

	tracer, _ := faketracer.New(t)

	a := &Atlas{}
	a.SetCache(tieredCache(t, "trhot4", "trdurable4", "trhot4", "trdurable4", hot, durable, 0))

	if a.CacheWritePool() == nil {
		t.Fatal("the chain was built without a write pool, so this test cannot show one surviving")
	}

	a.SetTracing(tracer)
	a.SetObservability(newObserver(t))
	a.SetTracing(tracer)

	if a.CacheWritePool() == nil {
		t.Error("the detachment decorator was stripped while instrumentation was re-derived")
	}
}

// TestEncodeSpanTree is the acceptance criterion about what a tile request
// produces: a span for the encode, with the provider query as a child, so a
// slow tile can be attributed to the database rather than to compression.
func TestEncodeSpanTree(t *testing.T) {
	tracer, exporter := faketracer.New(t)

	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		t.Fatalf("grid: %v", err)
	}

	m := NewWebMercatorMap("traced")
	m.SetMVTProvider("mvt_test", &test.TileProvider{MVTTile: []byte("tile")})

	a := &Atlas{}
	a.AddMap(m)
	a.SetTracing(tracer)

	// Through Atlas.Map, which is where the tracer is attached: a Map
	// registered before tracing was configured still gets it, because it is
	// handed out per lookup rather than captured at AddMap.
	got, err := a.Map("traced")
	if err != nil {
		t.Fatalf("Map(): %v", err)
	}

	if _, err := got.Encode(context.Background(), grid, slippy.Tile{Z: 3, X: 2, Y: 1}, nil); err != nil {
		t.Fatalf("Encode(): %v", err)
	}

	encode := faketracer.SpanNamed(t, exporter, tracing.SpanEncode)
	query := faketracer.SpanNamed(t, exporter, tracing.SpanProviderQuery)

	if query.Parent.SpanID() != encode.SpanContext.SpanID() {
		t.Error("the provider query span is not a child of the encode span")
	}

	want := map[attribute.Key]string{
		tracing.AttrMapName:       "traced",
		tracing.AttrTileMatrixSet: tms.WebMercatorQuad,
	}
	for key, value := range want {
		if got := faketracer.StringAttr(encode, key); got != value {
			t.Errorf("encode span %v = %q, want %q", key, got, value)
		}
	}
	if got := faketracer.Int64Attr(encode, tracing.AttrTileZ); got != 3 {
		t.Errorf("encode span z = %v, want 3", got)
	}
	if got := faketracer.StringAttr(query, tracing.AttrProviderName); got != "mvt_test" {
		t.Errorf("query span provider = %q, want mvt_test", got)
	}
}

// TestEncodeWithoutTracingRecordsNothing covers a Map that never went through
// an Atlas — every Map literal in this tree's tests, and any built by an
// embedding caller. Its tracer is nil, and Encode has to be a normal encode
// rather than a nil dereference.
func TestEncodeWithoutTracingRecordsNothing(t *testing.T) {
	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		t.Fatalf("grid: %v", err)
	}

	m := NewWebMercatorMap("untraced")
	m.SetMVTProvider("mvt_test", &test.TileProvider{MVTTile: []byte("tile")})

	if m.tracer != nil {
		t.Fatal("a Map literal carries a tracer, so this test proves nothing")
	}
	if _, ok := m.tracing().Tracer().(trace.Tracer); !ok {
		t.Error("tracing() on an untraced Map does not yield a usable tracer")
	}

	if _, err := m.Encode(context.Background(), grid, slippy.Tile{Z: 0, X: 0, Y: 0}, nil); err != nil {
		t.Fatalf("Encode(): %v", err)
	}
}
