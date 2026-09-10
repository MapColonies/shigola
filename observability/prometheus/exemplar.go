package prometheus

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/log"
)

// The exemplar labels. exemplarTraceIDKey is the one Grafana's Prometheus
// datasource is pointed at to reach Tempo; exemplarSpanIDKey narrows the
// landing to the operation that was actually measured.
//
// Deliberately the same constants the log records carry rather than a second
// spelling of "trace_id": the two surfaces are configured separately in Grafana
// — a derived field on the Loki datasource, an exemplar link on the Prometheus
// one — and both fail *silently* when the name is wrong. One pair of constants
// makes it impossible for a rename to fix one surface and quietly break the
// other.
const (
	exemplarTraceIDKey = log.TraceIDKey
	exemplarSpanIDKey  = log.SpanIDKey
)

// exemplarFrom returns the exemplar labels naming the trace active in ctx, or
// nil when there is no trace worth pointing at.
//
// nil is the client's own "no exemplar" signal, so an untraced observation is
// recorded exactly as a plain Observe would record it, with no empty label
// attached and nothing for the exposition to carry.
//
// Sampling *is* consulted here, which is the opposite of what the log handler
// does with the same span context (MAPCO-11494) — and for the reason the two
// differ in purpose. A trace id on a log line still groups that request's
// lines together whether or not Tempo received the trace. An exemplar has no
// such consolation use: its only job is to be a link, and Prometheus keeps one
// exemplar per bucket, overwritten by the next observation that lands there. At
// the default sample_ratio of 0.01 an unfiltered exemplar would therefore be a
// dead link roughly 99 times out of 100 — the stored one is almost always the
// most recent observation, and the most recent observation is almost never
// sampled. Filtering on IsSampled costs the feature nothing it could have had
// and is what makes clicking a bucket actually open a trace.
//
// The span is carried alongside the trace because the observation is made
// *inside* the operation's span, not merely inside the request: the tracing
// wrappers are installed outside the metric ones at both seams
// (atlas.instrumentCache, server.NewRouter). So a slow bucket on the per-tier
// histogram names the tier read that was slow, and a slow bucket on the HTTP
// histogram names the request — rather than both resolving to whatever span
// happened to be open. Without the span id that ordering would make no
// observable difference, which is why it is asserted rather than assumed:
// atlas.TestExemplarNamesTheSpanThatMeasuredIt.
func exemplarFrom(ctx context.Context) prometheus.Labels {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() || !sc.IsSampled() {
		return nil
	}

	return prometheus.Labels{
		exemplarTraceIDKey: sc.TraceID().String(),
		exemplarSpanIDKey:  sc.SpanID().String(),
	}
}

// observeWithExemplar records seconds on obs, attaching exemplar when there is one.
//
// The ExemplarObserver assertion cannot fail for a HistogramVec's observers,
// which are histograms. It is an assertion rather than a cast so that a family
// later changed to a summary — an Observer that is not an ExemplarObserver —
// loses its exemplars instead of panicking on the first traced request.
//
// The nil check is not redundant with ObserveWithExemplar's own handling of nil
// labels. It keeps "an untraced observation is a plain Observe" true at this
// level rather than resting on a documented detail of the client's internals.
func observeWithExemplar(obs prometheus.Observer, seconds float64, exemplar prometheus.Labels) {
	if exemplar != nil {
		if withExemplar, ok := obs.(prometheus.ExemplarObserver); ok {
			withExemplar.ObserveWithExemplar(seconds, exemplar)
			return
		}
	}

	obs.Observe(seconds)
}
