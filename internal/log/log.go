package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"time"

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

// ErrorKey is the key an error is reported under, as pino's err serialiser
// reports it: an object of type, message and — at ERROR and above — the stack
// of the log site.
const ErrorKey = "err"

// New returns the logger every shigola binary installs as slog's default,
// writing records in the MapColonies format (MAPCO-11544):
//
//	{"time":1756557582774,"level":"info","msg":"...","pid":197,"hostname":"web-01","version":"v1.4.0","rev":"9f3c1ab"}
//
// That is js-logger's output on its default path — pino with a label level
// formatter — so one collector, one dashboard and one set of queries work
// across shigola and the JS services. js-logger switches to pino's numeric
// levels only when its OTLP log transport is on, which shigola does not have.
//
// The fields are produced here and nowhere else: the binaries pass what only
// they know and the rest is read from the process. version and revision are
// parameters rather than read from internal/build because this is the package
// every other package logs through, and importing build from it would point the
// dependency the wrong way round.
//
// The process identity is top-level, where pino's base puts pid and hostname,
// rather than under a group as it used to be: a pino consumer looks for them
// there, and a group attached with WithGroup would also qualify the trace ids
// correlating adds (MAPCO-11494).
//
// exports are further outputs every record is copied to after stderr — the
// OTLP log bridge, when logs are exported (see logexport). They receive the
// record with its err serialised as stderr's is, but not the process identity
// or the trace ids, which an OTLP record carries natively. js-logger keeps its
// local output alongside its OTLP transport in the same way.
func New(w io.Writer, lvl slog.Leveler, version, revision string, exports ...slog.Handler) *slog.Logger {
	var stderr slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       lvl,
		ReplaceAttr: replaceBuiltins,
	})
	stderr = &correlating{handler: stderr.WithAttrs(processAttrs(version, revision))}

	if len(exports) == 0 {
		return slog.New(&serialising{handler: stderr})
	}

	// Errors are serialised once, above the fan-out, so every output reports
	// the same err — one stack, not one captured per output.
	return slog.New(&serialising{handler: &fanout{
		level:    lvl,
		handlers: append([]slog.Handler{stderr}, exports...),
	}})
}

// processAttrs is the identity of the process writing the record.
//
// Bound to stderr alone. An export such as OTLP describes the process in its
// resource instead, and repeating it on every record there would only add
// duplicates to what the backend stores.
func processAttrs(version, revision string) []slog.Attr {
	attrs := []slog.Attr{slog.Int("pid", os.Getpid())}
	// pino reads os.hostname(), which cannot fail; Go's can, and a record
	// without the key says so more honestly than one with an empty string.
	if hostname, err := os.Hostname(); err == nil {
		attrs = append(attrs, slog.String("hostname", hostname))
	}

	return append(attrs, slog.String("version", version), slog.String("rev", revision))
}

// replaceBuiltins rewrites slog's built-in time and level into pino's
// spelling: integer milliseconds since the Unix epoch, and the lowercase
// label. Only at the top level — a caller's own attribute that happens to be
// called "time" inside a group is theirs.
//
// A level between the named ones keeps slog's offset notation ("info+2"), so
// nothing is silently rounded onto a neighbour.
func replaceBuiltins(groups []string, a slog.Attr) slog.Attr {
	if len(groups) != 0 {
		return a
	}

	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.Int64(slog.TimeKey, t.UnixMilli())
		}
	case slog.LevelKey:
		if l, ok := a.Value.Any().(slog.Level); ok {
			return slog.String(slog.LevelKey, strings.ToLower(l.String()))
		}
	}

	return a
}

// NewHandler returns base wrapped in the handlers every shigola record passes
// through: err serialisation, then trace correlation. See serialising and
// correlating.
func NewHandler(base slog.Handler) slog.Handler {
	return &serialising{handler: &correlating{handler: base}}
}

// serialising replaces an error under ErrorKey with pino's err shape before
// the record reaches the handler it wraps.
type serialising struct {
	handler slog.Handler
}

func (h *serialising) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// This used to add debug.Stack() as a top-level "stack" on every ERROR
// record, error or not: a large field on a hot path, in a place no pino
// consumer looks. The stack now lives in err, and only where there is an
// error for it to explain.
func (h *serialising) Handle(ctx context.Context, r slog.Record) error {
	if hasError(r) {
		r = serialiseErrors(r)
	}

	return h.handler.Handle(ctx, r)
}

func (h *serialising) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &serialising{handler: h.handler.WithAttrs(attrs)}
}

func (h *serialising) WithGroup(name string) slog.Handler {
	return &serialising{handler: h.handler.WithGroup(name)}
}

// correlating adds the ids of the trace a record was written in.
//
// Its own handler, rather than part of serialising, because it belongs to
// stderr alone: an OTLP log record carries its trace and span natively, read
// by the bridge from the same context, and the ids as attributes too would be
// duplicates — in Loki, colliding ones, since its OTLP intake already stores
// the native trace id as trace_id.
type correlating struct {
	handler slog.Handler
}

func (h *correlating) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *correlating) Handle(ctx context.Context, r slog.Record) error {
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

func (h *correlating) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &correlating{handler: h.handler.WithAttrs(attrs)}
}

func (h *correlating) WithGroup(name string) slog.Handler {
	return &correlating{handler: h.handler.WithGroup(name)}
}

// fanout hands each record to every output: stderr first, then the exports.
//
// The level is decided once, here, so every output honours the one
// --log-level. An output that fails does not stop the ones after it — stderr
// is written before any export is tried, so a collector that is down cannot
// cost the local log — and the failures are joined for slog, which discards
// them; an export reports its own delivery failures through OTEL's error
// handler.
type fanout struct {
	level    slog.Leveler
	handlers []slog.Handler
}

func (h *fanout) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, r.Level) {
			continue
		}
		// Cloned because a handler may add attributes to the record it is
		// given, as correlating does, and the copies share storage.
		if err := handler.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (h *fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.derive(func(handler slog.Handler) slog.Handler { return handler.WithAttrs(attrs) })
}

func (h *fanout) WithGroup(name string) slog.Handler {
	return h.derive(func(handler slog.Handler) slog.Handler { return handler.WithGroup(name) })
}

func (h *fanout) derive(with func(slog.Handler) slog.Handler) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = with(handler)
	}

	return &fanout{level: h.level, handlers: handlers}
}

// hasError reports whether r carries an error value under ErrorKey. It is the
// cheap half of serialiseErrors, so the common record — no error — is not
// copied.
func hasError(r slog.Record) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		found = isError(a)
		return !found
	})

	return found
}

func isError(a slog.Attr) bool {
	if a.Key != ErrorKey || a.Value.Kind() != slog.KindAny {
		return false
	}
	_, ok := a.Value.Any().(error)

	return ok
}

// serialiseErrors returns a copy of r with each error under ErrorKey replaced
// by pino's err shape. A copy because slog.Record has no way to replace an
// attribute in place.
//
// Only the record's own attributes are seen here, not ones bound earlier with
// Logger.With: those were formatted when they were bound, and a stack captured
// then would be the wrong one anyway.
func serialiseErrors(r slog.Record) slog.Record {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if isError(a) {
			a = errorAttr(a.Value.Any().(error), r.Level)
		}
		out.AddAttrs(a)
		return true
	})

	return out
}

// errorAttr is pino's err serialiser in Go: the error's dynamic type in place
// of a JS constructor name, and its message.
//
// The stack is the log site's, captured here — Go errors carry none of their
// own — and only at ERROR and above. Below that, an error is usually a
// degradation the caller has already handled (a cache tier that failed a read
// is a miss), and a stack on every one would put back the hot-path cost this
// replaced.
//
// fmt.Sprint rather than err.Error(): a typed nil behind a non-nil error
// interface panics in Error(), and fmt recovers that into "<nil>".
func errorAttr(err error, level slog.Level) slog.Attr {
	fields := []any{
		slog.String("type", fmt.Sprintf("%T", err)),
		slog.String("message", fmt.Sprint(err)),
	}
	if level >= slog.LevelError {
		fields = append(fields, slog.String("stack", string(debug.Stack())))
	}

	return slog.Group(ErrorKey, fields...)
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
// re-taught for the sake of two new fields.
//
// Every helper below attaches the first error among its arguments under
// ErrorKey, which is how an error reaches the err property without its call
// site being rewritten: almost every one reports it through a %v.
func ErrorfContext(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelError, format, args)
}

func WarnfContext(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelWarn, format, args)
}

func InfofContext(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelInfo, format, args)
}

func DebugfContext(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelDebug, format, args)
}

func Errorf(format string, args ...any) {
	logf(context.Background(), slog.LevelError, format, args)
}

func Warnf(format string, args ...any) {
	logf(context.Background(), slog.LevelWarn, format, args)
}

func Infof(format string, args ...any) {
	logf(context.Background(), slog.LevelInfo, format, args)
}

func Debugf(format string, args ...any) {
	logf(context.Background(), slog.LevelDebug, format, args)
}

// Error, Warn, Info and Debug format their operands as fmt.Sprintln does,
// without the newline: the call sites were written against that
// ("zoom list: ", zooms), and against an error on its own, which Println
// renders as its message.
//
// They used to pass args[0].(string) to slog as the message and all of args as
// attributes, so a non-string first argument — log.Error(err) — panicked, and
// every other call repeated its message as an attribute key.
func Error(args ...any) {
	logln(slog.LevelError, args)
}

func Warn(args ...any) {
	logln(slog.LevelWarn, args)
}

func Info(args ...any) {
	logln(slog.LevelInfo, args)
}

func Debug(args ...any) {
	logln(slog.LevelDebug, args)
}

func logf(ctx context.Context, level slog.Level, format string, args []any) {
	emit(ctx, level, func() string { return fmt.Sprintf(format, args...) }, args)
}

func logln(level slog.Level, args []any) {
	emit(context.Background(), level, func() string {
		return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
	}, args)
}

// emit takes the message as a func so that a disabled level returns before
// paying for the formatting.
func emit(ctx context.Context, level slog.Level, msg func() string, args []any) {
	logger := slog.Default()
	if !logger.Enabled(ctx, level) {
		return
	}

	if err := firstError(args); err != nil {
		logger.Log(ctx, level, msg(), slog.Any(ErrorKey, err))
		return
	}
	logger.Log(ctx, level, msg())
}

func firstError(args []any) error {
	for _, arg := range args {
		if err, ok := arg.(error); ok && err != nil {
			return err
		}
	}

	return nil
}
