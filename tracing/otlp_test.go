package tracing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

// TestEnabledExporterExportsToAFullURLEndpoint is the end-to-end regression
// test for the deployment failure: an endpoint given as a full URL must
// actually reach the collector.
//
// It failed before because WithEndpoint takes a host and port, so the exporter
// treated the whole URL as a host, percent-encoded it, and produced
// "http://https:%2F%2F…/v1/traces" — rejected by url.Parse on every export.
// A URL now goes to WithEndpointURL, which is what this checks by being the
// collector.
func TestEnabledExporterExportsToAFullURLEndpoint(t *testing.T) {
	var (
		mu     sync.Mutex
		method string
		path   string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		method, path = r.Method, r.URL.Path
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// Sampled at 1, not left at the 1% default: the span below is a root, so
	// the default ratio drops it about 99 times in 100 and this test would
	// pass only occasionally. (It did, until run with -count=20.)
	always := env.Float(1)

	backend, err := tracing.New(context.Background(), tracing.Config{
		Enabled:  true,
		Exporter: tracing.ExporterOTLPHTTP,
		// srv.URL is "http://127.0.0.1:PORT" — a full URL, the shape a
		// platform hands an operator, and the shape that used to break.
		Endpoint:    env.String(srv.URL + "/v1/traces"),
		SampleRatio: &always,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	_, span := backend.Tracer().Start(context.Background(), "probe")
	span.End()

	// Shutdown flushes the batch processor synchronously, so the export has
	// happened by the time it returns.
	if err := backend.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if path != "/v1/traces" {
		t.Errorf("collector saw path %q, want /v1/traces — the endpoint URL was not honoured", path)
	}
	if method != http.MethodPost {
		t.Errorf("collector saw method %q, want POST", method)
	}
}
