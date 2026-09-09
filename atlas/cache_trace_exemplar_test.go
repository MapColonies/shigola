package atlas

import (
	"context"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/tracing"
)

// Tier names are unique to this file for the reason the neighbouring test files
// give: the observer registers against the process-wide registry.

// TestExemplarNamesTheSpanThatMeasuredIt is what the wrapping order in
// instrumentCache is for, checked rather than asserted in prose.
//
// Both prior tickets left a comment saying tracing goes outside the metric
// wrapper so that the operation's span is the active one when the duration is
// observed (MAPCO-11497, and the same at server.NewRouter). Nothing depended on
// it until exemplars: with the order inverted every exemplar on the per-tier
// histogram would name the cache-wide span instead — the same value for every
// tier, on the one histogram whose whole purpose is telling tiers apart.
//
// The two rows are the two levels the claim has to hold at, and they fail
// differently, which is why the per-tier row also names the inversion
// explicitly: a whole-cache exemplar naming the cache span is right, and a tier
// exemplar naming it is the bug.
func TestExemplarNamesTheSpanThatMeasuredIt(t *testing.T) {
	type tcase struct {
		hotType, durableType string
		family               string
		labels               map[string]string
		// wantSpan picks the span the exemplar must name out of the exporter.
		wantSpan func(*testing.T, *tracetest.InMemoryExporter) tracetest.SpanStub
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			hot := faketier.New("hot")
			durable := faketier.New("durable")
			durable.Seed(obsKey, []byte("tile"))

			tracer, exporter := faketracer.New(t)

			a := &Atlas{}
			a.SetCache(tieredCache(t, tc.hotType, tc.durableType, tc.hotType, tc.durableType, hot, durable, 0))
			a.SetObservability(newObserver(t))
			a.SetTracing(tracer)

			if _, hit, err := a.GetCache().Get(context.Background(), obsKey); err != nil || !hit {
				t.Fatalf("Get() = hit %v, err %v; want a hit from the durable tier", hit, err)
			}

			measured := tc.wantSpan(t, exporter)
			exemplar := ttools.ExemplarLabels(t, promclient.DefaultGatherer, tc.family, tc.labels)

			if got, want := exemplar["trace_id"], measured.SpanContext.TraceID().String(); got != want {
				t.Errorf("exemplar trace_id = %q, want the request's trace %q", got, want)
			}

			if got, want := exemplar["span_id"], measured.SpanContext.SpanID().String(); got != want {
				t.Errorf("exemplar span_id = %q, want %q", got, want)
			}

			// The specific inversion the ordering prevents, named so a failure
			// says which way round it went wrong.
			whole := faketracer.SpanNamed(t, exporter, tracing.SpanCacheGet)
			if tc.family == "shigola_cache_tier_duration_seconds" &&
				exemplar["span_id"] == whole.SpanContext.SpanID().String() {
				t.Error("tier exemplar names the cache-wide span; the metric wrapper is outside the tracing one")
			}
		}
	}

	tests := map[string]tcase{
		"the tier read": {
			hotType: "exhot1", durableType: "exdurable1",
			family: "shigola_cache_tier_duration_seconds",
			labels: map[string]string{"tier": "exhot1", "sub_command": "get"},
			wantSpan: func(t *testing.T, exporter *tracetest.InMemoryExporter) tracetest.SpanStub {
				return spanForTier(t, exporter, "exhot1")
			},
		},
		"the chain as a whole": {
			hotType: "exhot2", durableType: "exdurable2",
			family: "shigola_cache_duration_seconds",
			labels: map[string]string{"sub_command": "get"},
			wantSpan: func(t *testing.T, exporter *tracetest.InMemoryExporter) tracetest.SpanStub {
				return faketracer.SpanNamed(t, exporter, tracing.SpanCacheGet)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
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
