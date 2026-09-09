package atlas

import (
	"context"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/tracing"
)

// Tier names are unique to this file for the reason the neighbouring test files
// give: the observer registers against the process-wide registry.

// TestTierExemplarNamesTheTierSpan is what the wrapping order in
// instrumentCache is for, checked rather than asserted in prose.
//
// Both prior tickets left a comment saying tracing goes outside the metric
// wrapper so that the operation's span is the active one when the duration is
// observed (MAPCO-11497, and the same at server.NewRouter). Nothing depended on
// it until exemplars: with the order inverted, every exemplar on the per-tier
// histogram would name the cache-wide span instead — the same value for every
// tier, on the one histogram whose whole purpose is telling tiers apart.
func TestTierExemplarNamesTheTierSpan(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")
	durable.Seed(obsKey, []byte("tile"))

	tracer, exporter := faketracer.New(t)

	a := &Atlas{}
	a.SetCache(tieredCache(t, "exhot1", "exdurable1", "exhot1", "exdurable1", hot, durable, 0))
	a.SetObservability(newObserver(t))
	a.SetTracing(tracer)

	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); err != nil || !hit {
		t.Fatalf("Get() = hit %v, err %v; want a hit from the durable tier", hit, err)
	}

	whole := faketracer.SpanNamed(t, exporter, tracing.SpanCacheGet)

	hotSpan := spanForTier(t, exporter, "exhot1")

	exemplar := exemplarFor(t, "shigola_cache_tier_duration_seconds",
		map[string]string{"tier": "exhot1", "sub_command": "get"})

	if got, want := exemplar["trace_id"], hotSpan.SpanContext.TraceID().String(); got != want {
		t.Errorf("tier exemplar trace_id = %q, want the request's trace %q", got, want)
	}

	if got, want := exemplar["span_id"], hotSpan.SpanContext.SpanID().String(); got != want {
		t.Errorf("tier exemplar span_id = %q, want the tier's own span %q", got, want)
	}

	// The specific inversion the ordering prevents, named so a failure says
	// which way round it went wrong.
	if exemplar["span_id"] == whole.SpanContext.SpanID().String() {
		t.Error("tier exemplar names the cache-wide span; the metric wrapper is outside the tracing one")
	}
}

// TestWholeCacheExemplarNamesTheCacheSpan is the same property one level up:
// the whole-cache family measures the chain, so its exemplar names the chain's
// span rather than a tier's.
func TestWholeCacheExemplarNamesTheCacheSpan(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")
	durable.Seed(obsKey, []byte("tile"))

	tracer, exporter := faketracer.New(t)

	a := &Atlas{}
	a.SetCache(tieredCache(t, "exhot2", "exdurable2", "exhot2", "exdurable2", hot, durable, 0))
	a.SetObservability(newObserver(t))
	a.SetTracing(tracer)

	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); err != nil || !hit {
		t.Fatalf("Get() = hit %v, err %v; want a hit from the durable tier", hit, err)
	}

	whole := faketracer.SpanNamed(t, exporter, tracing.SpanCacheGet)

	exemplar := exemplarFor(t, "shigola_cache_duration_seconds", map[string]string{"sub_command": "get"})

	if got, want := exemplar["trace_id"], whole.SpanContext.TraceID().String(); got != want {
		t.Errorf("whole-cache exemplar trace_id = %q, want %q", got, want)
	}

	if got, want := exemplar["span_id"], whole.SpanContext.SpanID().String(); got != want {
		t.Errorf("whole-cache exemplar span_id = %q, want the %v span %q", got, tracing.SpanCacheGet, want)
	}
}

// spanForTier returns the tier span carrying the given tier name.
func spanForTier(t *testing.T, exporter *tracetest.InMemoryExporter, tier string) tracetest.SpanStub {
	t.Helper()

	for _, span := range faketracer.SpansNamed(exporter, tracing.SpanTierGet) {
		if faketracer.StringAttr(span, tracing.AttrCacheTier) == tier {
			return span
		}
	}

	t.Fatalf("no %v span carries tier %v", tracing.SpanTierGet, tier)

	return tracetest.SpanStub{}
}

// exemplarFor returns the labels of the exemplar on the named family's first
// bucket that carries one, for the sample matching labels.
//
// Read off the process-wide registry, which is safe for exemplars in a way it
// would not be for counters: an untraced observation stores nothing, so only a
// test that both traces and observes leaves an exemplar behind — and the
// assertions compare against a trace id this test's own exporter produced.
func exemplarFor(t *testing.T, name string, labels map[string]string) map[string]string {
	t.Helper()

	families, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, metric := range family.GetMetric() {
			if !hasLabels(metric.GetLabel(), labels) {
				continue
			}

			for _, bucket := range metric.GetHistogram().GetBucket() {
				exemplar := bucket.GetExemplar()
				if exemplar == nil {
					continue
				}

				got := make(map[string]string, len(exemplar.GetLabel()))
				for _, pair := range exemplar.GetLabel() {
					got[pair.GetName()] = pair.GetValue()
				}

				return got
			}

			t.Fatalf("no bucket of %v%v carries an exemplar", name, labels)
		}
	}

	t.Fatalf("no sample of %v matches %v", name, labels)

	return nil
}

// hasLabels reports whether pairs contain every label in want.
func hasLabels(pairs []*dto.LabelPair, want map[string]string) bool {
	for name, value := range want {
		found := false
		for _, pair := range pairs {
			if pair.GetName() == name && pair.GetValue() == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
