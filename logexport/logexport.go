// Package logexport exports shigola's log records over OTLP, alongside the
// JSON it writes to stderr.
//
// It is the counterpart of js-logger's OpenTelemetry transport: the same
// records, delivered to a collector as OTLP log records instead of being
// scraped off the container's output. Off unless [logging.otlp] says
// otherwise, and independent of [tracing] — logs may go to one collector
// (Loki's OTLP intake, say) while spans go to another, or be exported with
// tracing off altogether.
//
// What an OTLP record carries differs from the stderr line in two ways, both
// because OTLP has a native place for the thing: the process is described
// once, by the resource (service.name, service.version, host and process
// attributes), rather than on every record; and the trace and span are the
// record's own trace context rather than trace_id and span_id attributes.
// Everything else — the message, the severity, the caller's attributes and the
// serialised err — is the same record. See internal/log.New, which does the
// fan-out.
package logexport

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/otlp"
)

// Section is the config section this package reads, as errors name it.
const Section = "logging.otlp"

// Exporter names accepted by Config.Exporter.
const (
	ExporterOTLPGRPC = otlp.ExporterGRPC
	ExporterOTLPHTTP = otlp.ExporterHTTP
)

// Config is the [logging.otlp] section of the config file.
//
// The keys are [tracing]'s, less sample_ratio — logs are not sampled — so an
// operator who has configured one has configured the other. They are not
// shared with it: pointing both signals at one collector is two copies of an
// endpoint, and pointing them at different ones has to be possible.
type Config struct {
	// Enabled turns the export on. Absent or false builds nothing.
	Enabled env.Bool `toml:"enabled"`

	// Exporter selects the OTLP transport: "otlp_grpc" (default) or
	// "otlp_http".
	Exporter env.String `toml:"exporter"`

	// Endpoint is the collector, as "host:port" or a full URL. Empty hands the
	// decision to the OTEL SDK, which reads OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
	// and OTEL_EXPORTER_OTLP_ENDPOINT. See otlp.ValidateEndpoint.
	Endpoint env.String `toml:"endpoint"`

	// Insecure sends to a host:port endpoint without TLS.
	Insecure env.Bool `toml:"insecure"`

	// ServiceName is the service.name the records are attributed to.
	ServiceName env.String `toml:"service_name"`

	// TimeoutMS bounds one export attempt, in milliseconds.
	TimeoutMS env.Int `toml:"timeout_ms"`

	// Headers are sent with each export request — an auth header, a tenant id.
	Headers env.Dict `toml:"headers"`
}

// Validate reports a section that cannot be honoured. Like tracing's, it runs
// whether or not the export is enabled, so a typo fails now rather than the
// day someone switches it on.
func (c Config) Validate() error {
	if c.Exporter != "" {
		if _, ok := exporterFor[string(c.Exporter)]; !ok {
			return fmt.Errorf("%v: exporter (%v) must be one of %v", Section, c.Exporter, exporterNames())
		}
	}

	if int(c.TimeoutMS) < 0 {
		return fmt.Errorf("%v: timeout_ms (%v) must not be negative", Section, int(c.TimeoutMS))
	}

	return otlp.ValidateEndpoint(Section, string(c.Endpoint), bool(c.Insecure))
}

func (c Config) exporterName() string {
	if c.Exporter == "" {
		return otlp.DefaultExporter
	}

	return string(c.Exporter)
}

func (c Config) service() string {
	if c.ServiceName == "" {
		return otlp.DefaultServiceName
	}

	return string(c.ServiceName)
}

// Export is a running OTLP log export.
type Export struct {
	provider *sdklog.LoggerProvider
}

// New returns the export cfg describes, or nil when it is disabled — in which
// case nothing is built and nothing is dialled.
func New(ctx context.Context, cfg Config) (*Export, error) {
	if !bool(cfg.Enabled) {
		return nil, nil
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	exporter, err := exporterFor[cfg.exporterName()](ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", Section, err)
	}

	res, err := otlp.Resource(Section, cfg.service())
	if err != nil {
		return nil, err
	}

	return &Export{provider: sdklog.NewLoggerProvider(
		// Batched, not synchronous: an export per record would put the
		// collector's latency on every call site that logs, request path
		// included.
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)}, nil
}

// Handler returns the slog.Handler that turns records into OTLP log records,
// for internal/log.New to fan out to.
//
// The instrumentation scope is the module path, the convention for a bridge
// that covers a whole program rather than one library.
func (e *Export) Handler() slog.Handler {
	return otelslog.NewHandler("github.com/MapColonies/shigola", otelslog.WithLoggerProvider(e.provider))
}

// Shutdown exports what the batch processor still holds and stops it.
func (e *Export) Shutdown(ctx context.Context) error {
	return e.provider.Shutdown(ctx)
}

var installed atomic.Pointer[Export]

// Install records e as the process's export, for the shutdown paths to flush.
// They live in different packages — serve, cache seed|purge, and main's
// error exit — and have no other way to reach it.
func Install(e *Export) {
	installed.Store(e)
}

// Installed returns the export Install recorded, or nil.
func Installed() *Export {
	return installed.Load()
}

// FlushTimeout bounds the shutdown export, for the same reason as
// tracing.FlushTimeout: a collector that has gone away does not fail fast, and
// an unbounded flush would turn missing log lines into a failed rolling
// deploy.
const FlushTimeout = 2 * time.Second

// Flush exports what e still holds and stops it. A no-op for a nil export, so
// the shutdown paths can call it unconditionally.
//
// Errors go to stderr through slog like any other record — the export itself
// is shut down by then, so they do not try to report themselves over it.
func Flush(e *Export) {
	if e == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), FlushTimeout)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		slog.Error(fmt.Sprintf("flushing logs: %v", err))
	}
}

// exporterFor maps each accepted exporter name to its constructor, read by
// both Validate and New so the two cannot disagree — the same table tracing
// keeps for spans.
var exporterFor = map[string]func(context.Context, Config) (sdklog.Exporter, error){
	ExporterOTLPGRPC: newGRPCExporter,
	ExporterOTLPHTTP: newHTTPExporter,
}

func exporterNames() []string {
	names := make([]string, 0, len(exporterFor))
	for name := range exporterFor {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

// The two constructors below build the same options twice, as tracing's do and
// for the same reason: otlploggrpc.Option and otlploghttp.Option are unrelated
// types. What they must get right is the endpoint option — WithEndpointURL for
// a URL, WithEndpoint for host:port — which otlp.ValidateEndpoint has already
// made unambiguous.
func newGRPCExporter(ctx context.Context, cfg Config) (sdklog.Exporter, error) {
	opts := []otlploggrpc.Option{otlploggrpc.WithTimeout(otlp.Timeout(cfg.TimeoutMS))}

	if otlp.IsEndpointURL(string(cfg.Endpoint)) {
		opts = append(opts, otlploggrpc.WithEndpointURL(string(cfg.Endpoint)))
	} else {
		if cfg.Endpoint != "" {
			opts = append(opts, otlploggrpc.WithEndpoint(string(cfg.Endpoint)))
		}
		if bool(cfg.Insecure) {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
	}

	if headers := otlp.HeaderMap(cfg.Headers); headers != nil {
		opts = append(opts, otlploggrpc.WithHeaders(headers))
	}

	return otlploggrpc.New(ctx, opts...)
}

func newHTTPExporter(ctx context.Context, cfg Config) (sdklog.Exporter, error) {
	opts := []otlploghttp.Option{otlploghttp.WithTimeout(otlp.Timeout(cfg.TimeoutMS))}

	if otlp.IsEndpointURL(string(cfg.Endpoint)) {
		opts = append(opts, otlploghttp.WithEndpointURL(string(cfg.Endpoint)))
	} else {
		if cfg.Endpoint != "" {
			opts = append(opts, otlploghttp.WithEndpoint(string(cfg.Endpoint)))
		}
		if bool(cfg.Insecure) {
			opts = append(opts, otlploghttp.WithInsecure())
		}
	}

	if headers := otlp.HeaderMap(cfg.Headers); headers != nil {
		opts = append(opts, otlploghttp.WithHeaders(headers))
	}

	return otlploghttp.New(ctx, opts...)
}
