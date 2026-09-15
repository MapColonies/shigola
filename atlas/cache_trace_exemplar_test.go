package atlas

import (
	"context"
	"testing"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/internal/log"
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
		// rejectCacheSpan is the inversion this row can catch: a tier exemplar
		// naming the cache-wide span is the bug, and a whole-cache exemplar
		// naming it is correct, so only one row can look for it.
		rejectCacheSpan bool
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

			ttools.AssertExemplar(t, promclient.DefaultGatherer, tc.family, tc.labels,
				measured.SpanContext.TraceID().String(), measured.SpanContext.SpanID().String())

			if !tc.rejectCacheSpan {
				return
			}

			// Named so a failure says which way round it went wrong.
			whole := faketracer.SpanNamed(t, exporter, tracing.SpanCacheGet)
			exemplar := ttools.ExemplarLabels(t, promclient.DefaultGatherer, tc.family, tc.labels)
			if exemplar[log.SpanIDKey] == whole.SpanContext.SpanID().String() {
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
				return faketracer.TierSpan(t, exporter, tracing.SpanTierGet, "exhot1")
			},
			rejectCacheSpan: true,
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

// TestDetachedWriteExemplarNamesItsRequest is the property the docs claim for
// writes and nothing asserted: a cache write runs on the pool goroutine, after
// the response it belongs to has gone, and its exemplar still names the trace
// that caused it.
//
// It holds only because WritePool derives the write's context with
// context.WithoutCancel, which drops cancellation and keeps values — so the
// span context survives into a goroutine whose parent request is over. Swap
// that for context.Background() and the write still happens, the metric is
// still observed, and the exemplar silently becomes nothing: a latency spike on
// cache.Set would stop being clickable with no test failing.
func TestDetachedWriteExemplarNamesItsRequest(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")

	tracer, exporter := faketracer.New(t)

	a := &Atlas{}
	a.SetCache(tieredCache(t, "exwhot", "exwdurable", "exwhot", "exwdurable", hot, durable, 0))
	a.SetObservability(newObserver(t))
	a.SetTracing(tracer)

	ctx, cancel := context.WithCancel(context.Background())
	if err := a.GetCache().Set(ctx, obsKey, []byte("tile")); err != nil {
		t.Fatalf("Set() = %v", err)
	}

	// The request ends here, which is the whole point: everything the write
	// still needs has to have been carried by value rather than borrowed.
	cancel()

	pool := cache.WritePoolOf(a.GetCache())
	if pool == nil {
		t.Fatal("no write pool behind the chain; this test would prove nothing about detached writes")
	}
	pool.Drain(5 * time.Second)

	// The *per-tier* family, not the whole-cache one. The detachment decorator
	// sits inside the observability wrapper, so the whole-cache Set is observed
	// synchronously on the response path, where the live request context is
	// still in hand and WithoutCancel has nothing to do. Only the tier write
	// runs on the pool goroutine, which is the observation this property is
	// about — asserting the whole-cache family instead passes with the
	// derivation replaced by context.Background(), and so proves nothing.
	tier := faketracer.TierSpan(t, exporter, tracing.SpanTierSet, "exwdurable")

	// The trace compared against is the *request's*, taken from the whole-cache
	// span, which is created on the response path while the original context is
	// still live. Comparing the tier exemplar against the tier span's own trace
	// would prove nothing: with the derivation replaced by context.Background()
	// the tier write still gets a span, it is simply the root of a new and
	// orphaned trace — and an exemplar naming that span would still match it.
	whole := faketracer.SpanNamed(t, exporter, tracing.SpanCacheSet)

	if tier.SpanContext.TraceID() != whole.SpanContext.TraceID() {
		t.Fatalf("the detached write ran in trace %v, not the request's %v; it lost the span context on the way to the pool",
			tier.SpanContext.TraceID(), whole.SpanContext.TraceID())
	}

	ttools.AssertExemplar(t, promclient.DefaultGatherer, "shigola_cache_tier_duration_seconds",
		map[string]string{"tier": "exwdurable", "sub_command": "set"},
		whole.SpanContext.TraceID().String(), tier.SpanContext.SpanID().String())
}
