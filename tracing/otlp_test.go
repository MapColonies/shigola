package tracing_test

import (
	"context"
	"testing"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/tracing"
)

// TestNewDisabledBuildsNothing is the acceptance criterion "off by default",
// checked at the seam that decides it.
//
// A disabled config must not reach an exporter, and must hand back the backend
// whose Instrumented* methods are the identity — so there is nothing installed
// anywhere to have a cost.
func TestNewDisabledBuildsNothing(t *testing.T) {
	type tcase struct {
		config tracing.Config
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			backend, err := tracing.New(context.Background(), tc.config)
			if err != nil {
				t.Fatalf("tracing.New() = %v", err)
			}

			if backend.Enabled() {
				t.Error("Enabled() = true for a disabled config")
			}
			if backend != tracing.Interface(tracing.NullTracer) {
				t.Errorf("New() = %T for a disabled config, want the null backend", backend)
			}

			inner := faketier.New("inner")
			if backend.InstrumentedCache(inner) != cache.Interface(inner) {
				t.Error("a disabled backend installed a cache decorator")
			}
		}
	}

	tests := map[string]tcase{
		// No [tracing] section in the file at all.
		"absent section": {config: tracing.Config{}},
		// A section that is filled in but switched off: still nothing built,
		// so an operator can leave the endpoint configured and toggle one key.
		"configured but disabled": {
			config: tracing.Config{
				Exporter: tracing.ExporterOTLPGRPC,
				Endpoint: "tempo:4317",
				Insecure: true,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestNewRejectsAnUnusableConfig — an enabled section that cannot be honoured
// is a startup error, not a silent fallback to no tracing. An operator who
// asked for tracing and got none without being told would debug the collector.
func TestNewRejectsAnUnusableConfig(t *testing.T) {
	backend, err := tracing.New(context.Background(), tracing.Config{Enabled: true, Exporter: "jaeger"})
	if err == nil {
		t.Fatal("tracing.New() = nil error for an unknown exporter")
	}
	if backend == nil || backend.Enabled() {
		t.Error("tracing.New() should still return the null backend alongside its error")
	}
}

// TestNewEnabledBuildsAWorkingBackend covers the real construction path —
// resource, sampler and exporter — without a collector to talk to.
//
// The HTTP exporter is used because it establishes no connection until it has
// something to export, so this neither dials nor blocks.
func TestNewEnabledBuildsAWorkingBackend(t *testing.T) {
	cfg := tracing.Config{
		Enabled:     true,
		Exporter:    tracing.ExporterOTLPHTTP,
		Endpoint:    "127.0.0.1:4318",
		Insecure:    true,
		ServiceName: "shigola-test",
	}
	sample := env.Float(1)
	cfg.SampleRatio = &sample

	backend, err := tracing.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("tracing.New() = %v", err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })

	if !backend.Enabled() {
		t.Error("Enabled() = false")
	}

	// A tracer that records: the sampler and the provider are wired, even
	// though the span will never reach a collector from this test.
	_, span := backend.Tracer().Start(context.Background(), "probe")
	if !span.SpanContext().IsValid() {
		t.Error("the backend's tracer produced an invalid span context")
	}
	span.End()
}
