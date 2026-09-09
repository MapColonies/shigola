package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// recording returns a backend whose spans land in memory, synchronously.
//
// WithSyncer, not WithBatcher: a test that had to wait for a batch interval
// before it could read what it just recorded would either be slow or flaky, and
// batching is the exporter's concern rather than the instrumentation's. Every
// span is sampled, so an assertion about attributes is never lost to the head
// sampler.
func recording(t *testing.T) (Interface, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return NewWithProvider(tp, "test"), exporter
}

// spanNamed returns the one recorded span with the given name.
func spanNamed(t *testing.T, exporter *tracetest.InMemoryExporter, name string) tracetest.SpanStub {
	t.Helper()

	var found []tracetest.SpanStub
	for _, span := range exporter.GetSpans() {
		if span.Name == name {
			found = append(found, span)
		}
	}

	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("no span named %q; recorded %v", name, spanNames(exporter))
	default:
		t.Fatalf("%d spans named %q, expected 1", len(found), name)
	}

	return tracetest.SpanStub{}
}

func spanNames(exporter *tracetest.InMemoryExporter) []string {
	spans := exporter.GetSpans()

	names := make([]string, len(spans))
	for i := range spans {
		names[i] = spans[i].Name
	}

	return names
}

// attrOf reads one attribute off a span.
func attrOf(t *testing.T, span tracetest.SpanStub, key attribute.Key) attribute.Value {
	t.Helper()

	for _, kv := range span.Attributes {
		if kv.Key == key {
			return kv.Value
		}
	}

	t.Fatalf("span %q has no %v attribute; has %v", span.Name, key, span.Attributes)

	return attribute.Value{}
}
