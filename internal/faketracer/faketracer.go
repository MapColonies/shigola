// Package faketracer provides an in-memory tracing backend for tests, plus the
// span assertions that go with it.
//
// It exists because atlas and server both need to assert on the spans a real
// request produces, and building the backend is otherwise the same ten lines in
// each — an exporter, a synchronous processor, an always-on sampler and a
// cleanup — followed by the same span-lookup helpers.
//
// It lives under internal/ so those tests can share it without it becoming
// public API, alongside faketier, which exists for the same reason on the cache
// side.
//
// The tracing package's own tests cannot use this: faketracer imports tracing,
// so tracing's in-package test importing faketracer would be an import cycle.
// Those tests keep a local copy of New, and say so.
package faketracer

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/MapColonies/shigola/tracing"
)

// New returns a tracing backend whose spans land in memory, and the exporter
// holding them.
//
// Synchronous, via WithSyncer rather than WithBatcher: a test that had to wait
// out a batch interval before it could read what it just recorded would be
// either slow or flaky, and batching is the exporter's concern rather than the
// instrumentation's. Every span is sampled, so an assertion about attributes is
// never lost to the head sampler.
//
// The backend is not installed as OTEL's global — see tracing.Interface.Install
// — so a test holding one does not change the process it runs in.
func New(t *testing.T) (tracing.Interface, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return tracing.NewWithProvider(tp), exporter
}

// SpansNamed returns every recorded span with the given name.
func SpansNamed(exporter *tracetest.InMemoryExporter, name string) []tracetest.SpanStub {
	var found []tracetest.SpanStub
	for _, span := range exporter.GetSpans() {
		if span.Name == name {
			found = append(found, span)
		}
	}

	return found
}

// SpanNamed returns the one recorded span with the given name, failing the test
// if there is not exactly one.
func SpanNamed(t *testing.T, exporter *tracetest.InMemoryExporter, name string) tracetest.SpanStub {
	t.Helper()

	found := SpansNamed(exporter, name)
	if len(found) != 1 {
		t.Fatalf("%d spans named %q, want 1; recorded %v", len(found), name, Names(exporter.GetSpans()))
	}

	return found[0]
}

// Names renders spans as their names, which is usually enough to say what a
// failing assertion actually saw.
func Names(spans tracetest.SpanStubs) []string {
	out := make([]string, len(spans))
	for i := range spans {
		out[i] = spans[i].Name
	}

	return out
}

// StringAttr reads one string attribute off a span, or "" if it carries none.
func StringAttr(span tracetest.SpanStub, key attribute.Key) string {
	for _, kv := range span.Attributes {
		if kv.Key == key {
			return kv.Value.AsString()
		}
	}

	return ""
}

// BoolAttr reads one boolean attribute off a span, or false if it carries none.
//
// False for absent as well as for false, which is fine for every flag shigola
// records: they are set only when true.
func BoolAttr(span tracetest.SpanStub, key attribute.Key) bool {
	for _, kv := range span.Attributes {
		if kv.Key == key {
			return kv.Value.AsBool()
		}
	}

	return false
}

// Int64Attr reads one integer attribute off a span, or -1 if it carries none.
//
// -1 rather than 0 because 0 is a legitimate value for every integer attribute
// shigola records — a tile's z, x and y all start there — so a zero return
// could not be told from an absent attribute.
func Int64Attr(span tracetest.SpanStub, key attribute.Key) int64 {
	for _, kv := range span.Attributes {
		if kv.Key == key {
			return kv.Value.AsInt64()
		}
	}

	return -1
}
