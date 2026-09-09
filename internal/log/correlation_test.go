package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/log"
)

// The IDs are fixed rather than generated: an assertion that quotes the exact
// hex it expects says more on failure than one comparing two values the test
// itself just made up.
var (
	testTraceID = trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36}
	testSpanID  = trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
)

// spanCtx returns a context carrying a span context built from the given
// fields, without an SDK: the handler reads the span context off the context
// and nothing else, so a real tracer would only add moving parts.
func spanCtx(traceID trace.TraceID, spanID trace.SpanID, sampled bool) context.Context {
	cfg := trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}
	if sampled {
		cfg.TraceFlags = trace.FlagsSampled
	}

	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(cfg))
}

// logRecord emits one record through the handler under test and returns the
// JSON object it produced.
func logRecord(t *testing.T, ctx context.Context, level slog.Level, msg string) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(log.NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.Log(ctx, level, msg)

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}

	return record
}

func TestCorrelationIDs(t *testing.T) {
	type tcase struct {
		ctx   context.Context
		level slog.Level
		trace string // "" means the key must be absent entirely
		span  string
	}

	testCases := map[string]tcase{
		"in a sampled trace": {
			ctx:   spanCtx(testTraceID, testSpanID, true),
			level: slog.LevelInfo,
			trace: "4bf92f3577b34da6a3ce929d0e0e4736",
			span:  "00f067aa0ba902b7",
		},
		// A valid span context whose trace was not sampled still names the
		// request: the IDs group that request's log lines together even when
		// Tempo holds no trace to pivot to.
		"in an unsampled trace": {
			ctx:   spanCtx(testTraceID, testSpanID, false),
			level: slog.LevelInfo,
			trace: "4bf92f3577b34da6a3ce929d0e0e4736",
			span:  "00f067aa0ba902b7",
		},
		// AC 2: records outside a trace are unchanged, with no empty fields.
		"outside any trace": {
			ctx:   context.Background(),
			level: slog.LevelInfo,
		},
		// A zero trace ID is what an uninitialised or stripped span context
		// looks like; it is not an ID and must not be logged as one.
		"with a zero trace id": {
			ctx:   spanCtx(trace.TraceID{}, testSpanID, true),
			level: slog.LevelInfo,
		},
		"with a zero span id": {
			ctx:   spanCtx(testTraceID, trace.SpanID{}, true),
			level: slog.LevelInfo,
		},
		// Errors already carry a stack; correlation is added alongside it.
		"on an error": {
			ctx:   spanCtx(testTraceID, testSpanID, true),
			level: slog.LevelError,
			trace: "4bf92f3577b34da6a3ce929d0e0e4736",
			span:  "00f067aa0ba902b7",
		},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			record := logRecord(t, tc.ctx, tc.level, "hello")

			for key, want := range map[string]string{
				log.TraceIDKey: tc.trace,
				log.SpanIDKey:  tc.span,
			} {
				got, present := record[key]
				switch {
				case want == "" && present:
					t.Errorf("%v: %q present as %v, want the key absent", key, key, got)
				case want != "" && !present:
					t.Errorf("%v: absent, want %v", key, want)
				case want != "" && got != want:
					t.Errorf("%v: %v, want %v", key, got, want)
				}
			}

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
	var buf bytes.Buffer
	logger := slog.New(log.NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))).
		With(log.ServiceAttrs("v1.2.3", "cafe123"))

	logger.ErrorContext(spanCtx(testTraceID, testSpanID, true), "boom")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}

	for key, want := range map[string]string{
		log.TraceIDKey: "4bf92f3577b34da6a3ce929d0e0e4736",
		log.SpanIDKey:  "00f067aa0ba902b7",
	} {
		if got := record[key]; got != want {
			t.Errorf("top-level %v: %v, want %v", key, got, want)
		}
	}

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

// BenchmarkHandleWithoutATrace covers the requirement that tracing cost nothing
// when it is off: with no span in the context the handler does one context
// lookup and adds nothing.
func BenchmarkHandleWithoutATrace(b *testing.B) {
	benchmarkHandle(b, context.Background())
}

func BenchmarkHandleInATrace(b *testing.B) {
	benchmarkHandle(b, spanCtx(testTraceID, testSpanID, true))
}

func benchmarkHandle(b *testing.B, ctx context.Context) {
	logger := slog.New(log.NewHandler(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))).
		With(log.ServiceAttrs("v1.2.3", "cafe123"))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		logger.InfoContext(ctx, "a tile was served")
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
			var buf bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(log.NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
			defer slog.SetDefault(previous)

			tc.emit(spanCtx(testTraceID, testSpanID, true), "tier (%v) get: %v", "redis", "refused")

			var record map[string]any
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
			}

			if got, want := record["msg"], "tier (redis) get: refused"; got != want {
				t.Errorf("msg: %v, want %v", got, want)
			}
			if got, want := record["level"], tc.level; got != want {
				t.Errorf("level: %v, want %v", got, want)
			}
			if got, want := record[log.TraceIDKey], "4bf92f3577b34da6a3ce929d0e0e4736"; got != want {
				t.Errorf("%v: %v, want %v", log.TraceIDKey, got, want)
			}
			if got, want := record[log.SpanIDKey], "00f067aa0ba902b7"; got != want {
				t.Errorf("%v: %v, want %v", log.SpanIDKey, got, want)
			}
		}
	}

	for name, tc := range testCases {
		t.Run(name, fn(tc))
	}
}
