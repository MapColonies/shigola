package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// LevelSilent is a custom log level that will not
// generate any logs.
const LevelSilent = -8

// TraceIDKey and SpanIDKey are the record keys that carry OpenTelemetry
// correlation, so a trace in Tempo reaches the log lines of the request that
// produced it and back again (MAPCO-11494).
//
// The names are the de-facto convention Grafana's Loki datasource and the OTel
// ecosystem both assume, in snake_case rather than the OTLP log model's
// TraceId/SpanId: what reads these is a Loki derived field or a `| json` label,
// not an OTLP consumer. They are exported because the pipeline that greps for
// them cannot be tested, so the docs and the tests must at least agree with the
// code on the spelling.
const (
	TraceIDKey = "trace_id"
	SpanIDKey  = "span_id"
)

// NewLogger returns a new tegola JSON logger.
func NewLogger(lvl slog.Level, options ...func(opts *slog.HandlerOptions)) *slog.Logger {
	handlerOptions := &slog.HandlerOptions{
		Level: lvl,
		// TODO: enable once we switch to slog.Default
		// instead of internal/log methods
		AddSource: false,
	}

	for _, opt := range options {
		opt(handlerOptions)
	}

	// Create a base handler that outputs to stderr.
	// The AddSource option includes file and line info in each log record.
	baseHandler := slog.NewJSONHandler(os.Stderr, handlerOptions)

	// Wrap the base handler with our custom handler to add stack traces for errors.
	handler := NewHandler(baseHandler)
	logger := slog.New(handler)

	return logger
}

// ServiceGroup is the attribute group under which every record carries the
// identity of the process that wrote it.
const ServiceGroup = "shigola"

// ServiceAttrs returns that process identity, for the binaries to hang on the
// logger they install as slog's default.
//
// One grouped attribute rather than a logger-wide WithGroup, which is what this
// used to be: an open group qualifies everything that follows it, including the
// attributes Handle adds per record, so correlation would be logged as
// shigola.trace_id — not a name any log pipeline looks for. As a single
// attribute the group nests only its own contents, and what it writes is
// otherwise byte-for-byte what WithGroup wrote.
//
// Returning an slog.Attr rather than a whole logger is deliberate too: an
// slog.Attr can only reach a logger through With, so the placement cannot be
// undone by a caller who reaches for WithGroup out of habit.
//
// version and revision are parameters while pid is read here because the first
// two come from internal/build's ldflag targets: importing that from the
// package every other package logs through would point the dependency the wrong
// way round. The pid is the running process's own and needs nothing.
func ServiceAttrs(version, revision string) slog.Attr {
	return slog.Group(ServiceGroup,
		"version", version,
		"pid", os.Getpid(),
		"rev", revision,
	)
}

// NewHandler returns a new custom slog.Handler that wraps the provided baseHandler.
// The returned handler augments error-level logs by appending a stack trace.
func NewHandler(baseHandler slog.Handler) slog.Handler {
	return &Handler{
		handler: baseHandler,
	}
}

// Handler is a custom slog.Handler wrapper that adds a stack trace to error logs.
// It wraps an underlying slog.Handler and delegates all log handling, augmenting
// the log record when the log level is error or higher.
type Handler struct {
	handler slog.Handler
}

// Enabled reports whether the underlying handler is enabled for the provided log level.
// It delegates the check to the wrapped handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle processes the log record r. If the log level is error or higher,
// it adds a "stack" attribute containing the current stack trace to the record.
// Records emitted inside a trace also carry that trace's ids. The modified
// record is then passed to the underlying handler for output.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	// For errors and more severe logs, include the current stack trace.
	if r.Level >= slog.LevelError {
		r.Add("stack", string(debug.Stack()))
	}

	// Correlation is added here, on the record, rather than by the caller:
	// every log line in a request should carry it, and the context is the only
	// thing every logging call site has in common.
	//
	// IsValid covers both halves of the requirement to add nothing outside a
	// trace — a context with no span at all yields the zero span context, and
	// so does one whose ids were dropped in transit. Sampling is deliberately
	// not consulted: the ids name the request whether or not a trace was
	// exported for it, and dropping them for the 99% a ratio sampler declines
	// would leave most requests' logs uncorrelated with each other.
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String(TraceIDKey, sc.TraceID().String()),
			slog.String(SpanIDKey, sc.SpanID().String()),
		)
	}

	return h.handler.Handle(ctx, r)
}

// WithAttrs returns a new Handler that includes the specified attributes with every log record.
// It derives a new underlying handler with the extra attributes.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{handler: h.handler.WithAttrs(attrs)}
}

// WithGroup returns a new Handler that associates log records with the specified group name.
// It derives a new underlying handler with the group context applied.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{handler: h.handler.WithGroup(name)}
}

// ParseLogLevel converts the provided log level string to the corresponding slog.Level.
// Supported values are "debug", "info", "warn", "error" and "silent". If the input does not match
// any supported level, the function defaults to slog.LevelInfo.
func ParseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "silent":
		return LevelSilent
	default:
		return slog.LevelInfo
	}
}

// The Context variants carry the trace and span ids of the request they were
// called in (see Handle). They take a format string rather than attributes so
// that a request-path call site can gain correlation without its message
// changing — nothing that greps today's logs for a message should have to be
// re-taught for the sake of two new fields. They go the same way as their
// context-free siblings when the TODO above is done.
func ErrorfContext(ctx context.Context, format string, args ...any) {
	slog.ErrorContext(ctx, fmt.Sprintf(format, args...))
}

func WarnfContext(ctx context.Context, format string, args ...any) {
	slog.WarnContext(ctx, fmt.Sprintf(format, args...))
}

func InfofContext(ctx context.Context, format string, args ...any) {
	slog.InfoContext(ctx, fmt.Sprintf(format, args...))
}

func DebugfContext(ctx context.Context, format string, args ...any) {
	slog.DebugContext(ctx, fmt.Sprintf(format, args...))
}

// TODO: remove those methods and use slog straight up
func Errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Error(msg)
}

func Error(args ...any) {
	slog.Error(args[0].(string), args...)
}

func Warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Warn(msg)
}

func Warn(args ...any) {
	slog.Warn(args[0].(string), args...)
}

func Infof(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Info(msg)
}

func Info(args ...any) {
	slog.Info(args[0].(string), args...)
}

func Debugf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Debug(msg)
}

func Debug(args ...any) {
	slog.Debug(args[0].(string), args...)
}
