package postgis

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/tracelog"
	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/log"
)

// TestLoggerAdapterCorrelates covers the pgx statement logger, which needs no
// database: pgx hands it the context of the query it is reporting on, so a
// warning or an error about a statement names the request that issued it.
func TestLoggerAdapterCorrelates(t *testing.T) {
	type tcase struct {
		ctx   context.Context
		level tracelog.LogLevel
		want  string // "" means nothing should be logged at all
		trace bool
	}

	traced := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36},
		SpanID:     trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7},
		TraceFlags: trace.FlagsSampled,
	}))

	testCases := map[string]tcase{
		"error in a trace": {ctx: traced, level: tracelog.LogLevelError, want: "ERROR", trace: true},
		"warn in a trace":  {ctx: traced, level: tracelog.LogLevelWarn, want: "WARN", trace: true},
		"error untraced":   {ctx: context.Background(), level: tracelog.LogLevelError, want: "ERROR"},
		// Anything below warn is dropped, which the adapter did before and
		// still does: correlation does not make a silent level speak.
		"info in a trace":  {ctx: traced, level: tracelog.LogLevelInfo},
		"debug in a trace": {ctx: traced, level: tracelog.LogLevelDebug},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			var buf bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(log.NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
			defer slog.SetDefault(previous)

			NewLoggerAdapter().Log(tc.ctx, tc.level, "a statement went wrong", map[string]any{"sql": "SELECT 1"})

			if tc.want == "" {
				if buf.Len() != 0 {
					t.Fatalf("level %v logged %q, want nothing", tc.level, buf.String())
				}

				return
			}

			var record map[string]any
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
			}

			if got := record["level"]; got != tc.want {
				t.Errorf("level = %v, want %v", got, tc.want)
			}

			got, present := record[log.TraceIDKey]
			switch {
			case tc.trace && got != "4bf92f3577b34da6a3ce929d0e0e4736":
				t.Errorf("%v = %v, want the caller's trace", log.TraceIDKey, got)
			case !tc.trace && present:
				t.Errorf("%v present as %v, want the key absent", log.TraceIDKey, got)
			}
		}
	}

	for name, tc := range testCases {
		t.Run(name, fn(tc))
	}
}
