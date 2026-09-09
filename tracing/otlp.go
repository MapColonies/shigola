package tracing

import (
	"context"
	"fmt"
	"slices"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/MapColonies/shigola/internal/build"
	"github.com/MapColonies/shigola/internal/log"
)

// New returns the tracing backend cfg describes.
//
// A disabled config returns NullTracer, and does so before anything is built:
// no exporter is dialled, no batch processor goroutine is started, and no
// decorator is installed anywhere. Tracing being off therefore costs a boolean
// read at startup and nothing at all per request.
//
// It does not touch OTEL's global state — see Install for that — so a test can
// hold a live backend without changing the process it runs in.
func New(ctx context.Context, cfg Config) (Interface, error) {
	if !bool(cfg.Enabled) {
		return NullTracer, nil
	}

	if err := cfg.Validate(); err != nil {
		return NullTracer, err
	}

	exporter, err := newExporter(ctx, cfg)
	if err != nil {
		return NullTracer, err
	}

	res, err := newResource(cfg)
	if err != nil {
		return NullTracer, err
	}

	tp := sdktrace.NewTracerProvider(
		// Batched, not synchronous: an export on the response path would put
		// the collector's latency in front of the client's tile.
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(newSampler(cfg.Ratio())),
	)

	log.Infof(
		"setting up tracing: %v exporter, service %q, sampling %.4g of locally-started traces",
		cfg.ExporterName(), cfg.Service(), cfg.Ratio(),
	)

	return NewWithProvider(tp), nil
}

// newSampler wraps the ratio sampler in a parent-based one.
//
// The distinction is the whole point of head sampling in a service that sits
// behind other services: ratio alone re-rolls the dice per trace, so a gateway
// that decided to sample a request would hand it to a shigola that dropped its
// half of it 99 times out of 100, leaving a trace with a hole where the tile
// was served. ParentBased follows an upstream decision — sampled or not — and
// applies the ratio only to traces this service roots itself.
func newSampler(ratio float64) sdktrace.Sampler {
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
}

// newResource describes this process to Tempo.
//
// Merged onto resource.Default() rather than replacing it, so the host,
// process and telemetry.sdk attributes an operator expects to filter on are
// still there. The schema URL is pinned to the same semconv version the SDK's
// own resource package uses; a different one makes Merge report a schema
// conflict instead of merging.
func newResource(cfg Config) (*resource.Resource, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.Service()),
			semconv.ServiceVersion(build.Version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: describing this process: %w", err)
	}

	return res, nil
}

// newExporter dials the collector.
//
// An empty endpoint is passed through rather than defaulted here: the OTLP
// exporters read OTEL_EXPORTER_OTLP_ENDPOINT and its per-signal and per-header
// siblings themselves, and an in-cluster collector is normally configured that
// way for every service at once. Substituting a shigola-specific default would
// take that away.
// exporterFor maps each accepted exporter name to its constructor.
//
// One vocabulary, read by both Config.Validate and newExporter. Those switched
// on the same string separately until MAPCO-11497 review, and newExporter's
// default branch existed only to catch a name added to one switch and not the
// other — a hazard a single table cannot have.
var exporterFor = map[string]func(context.Context, Config) (sdktrace.SpanExporter, error){
	ExporterOTLPGRPC: newGRPCExporter,
	ExporterOTLPHTTP: newHTTPExporter,
}

// exporterNames lists the accepted exporters in a stable order, for error
// messages. Derived from the table rather than written out, so a new exporter
// cannot be added without the error message learning about it.
func exporterNames() []string {
	names := make([]string, 0, len(exporterFor))
	for name := range exporterFor {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

// newExporter dials the collector.
func newExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	newer, ok := exporterFor[cfg.ExporterName()]
	if !ok {
		// Unreachable in practice: New validates first, against this same
		// table. Reported rather than panicked because an unreachable branch
		// that becomes reachable should fail at startup, loudly.
		return nil, fmt.Errorf("tracing: unknown exporter (%v)", cfg.ExporterName())
	}

	return newer(ctx, cfg)
}

// The two constructors below build the same options twice.
// otlptracegrpc.Option and otlptracehttp.Option are unrelated types with no
// common interface, so factoring the shared shape out would mean either a
// generic helper per option — one closure pair each — or reflection, both
// longer and harder to read than the repetition.
//
// What they must not get wrong is which endpoint option to use.
// WithEndpoint takes a host and port and nothing else; WithEndpointURL takes a
// full URL. Passing a URL to the former is accepted and then fails on every
// export, because it treats the whole string as a host — see
// Config.validateEndpoint, which is what now stops that reaching an exporter
// at all.
//
// An empty endpoint is passed through rather than defaulted here: the OTLP
// exporters read OTEL_EXPORTER_OTLP_ENDPOINT and its per-signal and per-header
// siblings themselves, and an in-cluster collector is normally configured that
// way for every service at once. Substituting a shigola-specific default would
// take that away.
//
// WithInsecure is skipped for a URL endpoint, whose scheme has already decided
// — and where the two disagree Validate has already refused the config, rather
// than letting option order pick a winner.
func newGRPCExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{otlptracegrpc.WithTimeout(cfg.Timeout())}

	if cfg.IsEndpointURL() {
		opts = append(opts, otlptracegrpc.WithEndpointURL(string(cfg.Endpoint)))
	} else {
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracegrpc.WithEndpoint(string(cfg.Endpoint)))
		}
		if bool(cfg.Insecure) {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
	}

	if headers := cfg.HeaderMap(); headers != nil {
		opts = append(opts, otlptracegrpc.WithHeaders(headers))
	}

	return otlptracegrpc.New(ctx, opts...)
}

func newHTTPExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{otlptracehttp.WithTimeout(cfg.Timeout())}

	if cfg.IsEndpointURL() {
		opts = append(opts, otlptracehttp.WithEndpointURL(string(cfg.Endpoint)))
	} else {
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpoint(string(cfg.Endpoint)))
		}
		if bool(cfg.Insecure) {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
	}

	if headers := cfg.HeaderMap(); headers != nil {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	return otlptracehttp.New(ctx, opts...)
}
