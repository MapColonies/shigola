package log_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/fakelog"
	"github.com/MapColonies/shigola/internal/log"
)

func TestCorrelationIDs(t *testing.T) {
	type tcase struct {
		ctx   context.Context
		level slog.Level
		trace string // "" means the key must be absent entirely
		span  string
	}

	testCases := map[string]tcase{
		"in a sampled trace": {
			ctx:   fakelog.TracedContext(true),
			level: slog.LevelInfo,
			trace: fakelog.TraceIDHex,
			span:  fakelog.SpanIDHex,
		},
		// A valid span context whose trace was not sampled still names the
		// request: the ids group that request's log lines together even when
		// Tempo holds no trace to pivot to.
		"in an unsampled trace": {
			ctx:   fakelog.TracedContext(false),
			level: slog.LevelInfo,
			trace: fakelog.TraceIDHex,
			span:  fakelog.SpanIDHex,
		},
		// AC 2: records outside a trace are unchanged, with no empty fields.
		"outside any trace": {
			ctx:   context.Background(),
			level: slog.LevelInfo,
		},
		// A zero trace id is what an uninitialised or stripped span context
		// looks like; it is not an id and must not be logged as one.
		"with a zero trace id": {
			ctx:   fakelog.ContextWith(trace.TraceID{}, fakelog.SpanID, true),
			level: slog.LevelInfo,
		},
		"with a zero span id": {
			ctx:   fakelog.ContextWith(fakelog.TraceID, trace.SpanID{}, true),
			level: slog.LevelInfo,
		},
		// Errors already carry a stack; correlation is added alongside it.
		"on an error": {
			ctx:   fakelog.TracedContext(true),
			level: slog.LevelError,
			trace: fakelog.TraceIDHex,
			span:  fakelog.SpanIDHex,
		},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			logger, rec := fakelog.New()
			logger.Log(tc.ctx, tc.level, "hello")

			record := rec.One(t)
			fakelog.AssertCorrelation(t, record, tc.trace, tc.span)

			if tc.level >= slog.LevelError {
				if _, ok := record["stack"]; !ok {
					t.Error("stack: absent on an error record")
				}
			}
		}
	}

	for name, tc := range testCases {
		t.Run(name, fn(tc))
	}
}

// TestServiceAttrsKeepCorrelationAtTheTopLevel composes a logger exactly as the
// binaries do, because where the correlation ids land is a property of that
// composition rather than of the handler alone: an open group would qualify
// them, and shigola.trace_id is not the name a log pipeline looks for.
func TestServiceAttrsKeepCorrelationAtTheTopLevel(t *testing.T) {
	logger, rec := fakelog.New()
	logger.With(log.ServiceAttrs("v1.2.3", "cafe123")).
		ErrorContext(fakelog.TracedContext(true), "boom")

	record := rec.One(t)
	fakelog.AssertCorrelation(t, record, fakelog.TraceIDHex, fakelog.SpanIDHex)

	// The stack trace is top-level for the same reason, which is a change from
	// when the group was open and carried it.
	if _, ok := record["stack"]; !ok {
		t.Error("stack: not at the top level of an error record")
	}

	// The process identity keeps the shape it had under WithGroup: the point of
	// the change is that correlation escapes the group, not that anything else
	// moves.
	group, ok := record[log.ServiceGroup].(map[string]any)
	if !ok {
		t.Fatalf("%v: %#v, want an object", log.ServiceGroup, record[log.ServiceGroup])
	}
	if group["version"] != "v1.2.3" {
		t.Errorf("%v.version: %v, want v1.2.3", log.ServiceGroup, group["version"])
	}
	if group["rev"] != "cafe123" {
		t.Errorf("%v.rev: %v, want cafe123", log.ServiceGroup, group["rev"])
	}
	if _, ok := group["pid"]; !ok {
		t.Errorf("%v.pid: absent", log.ServiceGroup)
	}
	for _, key := range []string{log.TraceIDKey, log.SpanIDKey} {
		if _, ok := group[key]; ok {
			t.Errorf("%v.%v: present, want it at the top level only", log.ServiceGroup, key)
		}
	}
}

// TestContextHelpersCorrelate covers the ctx-aware printf helpers the
// request-path call sites use. They exist so a call site can gain correlation
// without its message being rewritten, so the test checks both: the ids are
// there, and the formatted message is untouched.
func TestContextHelpersCorrelate(t *testing.T) {
	type tcase struct {
		emit  func(context.Context, string, ...any)
		level string
	}

	testCases := map[string]tcase{
		"error": {emit: log.ErrorfContext, level: "ERROR"},
		"warn":  {emit: log.WarnfContext, level: "WARN"},
		"info":  {emit: log.InfofContext, level: "INFO"},
		"debug": {emit: log.DebugfContext, level: "DEBUG"},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			rec := fakelog.Default(t)

			tc.emit(fakelog.TracedContext(true), "tier (%v) get: %v", "redis", "refused")

			record := rec.One(t)
			if got, want := record["msg"], "tier (redis) get: refused"; got != want {
				t.Errorf("msg: %v, want %v", got, want)
			}
			if got, want := record["level"], tc.level; got != want {
				t.Errorf("level: %v, want %v", got, want)
			}
			fakelog.AssertCorrelation(t, record, fakelog.TraceIDHex, fakelog.SpanIDHex)
		}
	}

	for name, tc := range testCases {
		t.Run(name, fn(tc))
	}
}

// The three benchmarks below are the evidence for "no measurable overhead when
// tracing is disabled", and are meant to be read together:
//
//   - WithoutTheWrapper is the base JSON handler alone, so the delta from it to
//     WithoutATrace is everything this package's handler costs on a record that
//     is not in a trace: one level comparison and one context lookup.
//   - WithoutATrace is the real handler with nothing in the context, which is
//     every record in a process running with tracing off.
//   - InATrace is what the ids themselves cost, for scale.
//
// io.Discard rather than a buffer: a buffer would grow through b.N and start
// measuring allocation instead of the handler.
func BenchmarkHandleWithoutTheWrapper(b *testing.B) {
	benchmarkHandle(b, slog.NewJSONHandler(io.Discard, benchOpts), context.Background())
}

func BenchmarkHandleWithoutATrace(b *testing.B) {
	benchmarkHandle(b, log.NewHandler(slog.NewJSONHandler(io.Discard, benchOpts)), context.Background())
}

func BenchmarkHandleInATrace(b *testing.B) {
	benchmarkHandle(b, log.NewHandler(slog.NewJSONHandler(io.Discard, benchOpts)), fakelog.TracedContext(true))
}

var benchOpts = &slog.HandlerOptions{Level: slog.LevelDebug}

func benchmarkHandle(b *testing.B, handler slog.Handler, ctx context.Context) {
	logger := slog.New(handler).With(log.ServiceAttrs("v1.2.3", "cafe123"))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		logger.InfoContext(ctx, "a tile was served")
	}
}
