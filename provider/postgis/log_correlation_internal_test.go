package postgis

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/tracelog"

	"github.com/MapColonies/shigola/internal/fakelog"
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

	testCases := map[string]tcase{
		"error in a trace": {ctx: fakelog.TracedContext(true), level: tracelog.LogLevelError, want: "ERROR", trace: true},
		"warn in a trace":  {ctx: fakelog.TracedContext(true), level: tracelog.LogLevelWarn, want: "WARN", trace: true},
		"error untraced":   {ctx: context.Background(), level: tracelog.LogLevelError, want: "ERROR"},
		// Anything below warn is dropped, which the adapter did before and
		// still does: correlation does not make a silent level speak.
		"info in a trace":  {ctx: fakelog.TracedContext(true), level: tracelog.LogLevelInfo},
		"debug in a trace": {ctx: fakelog.TracedContext(true), level: tracelog.LogLevelDebug},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			rec := fakelog.Default(t)

			NewLoggerAdapter().Log(tc.ctx, tc.level, "a statement went wrong", map[string]any{"sql": "SELECT 1"})

			if tc.want == "" {
				if !rec.Empty() {
					t.Fatalf("level %v logged %q, want nothing", tc.level, rec.Raw())
				}

				return
			}

			record := rec.One(t)
			if got := record["level"]; got != tc.want {
				t.Errorf("level = %v, want %v", got, tc.want)
			}

			if tc.trace {
				fakelog.AssertCorrelation(t, record, fakelog.TraceIDHex, fakelog.SpanIDHex)
			} else {
				fakelog.AssertCorrelation(t, record, "", "")
			}
		}
	}

	for name, tc := range testCases {
		t.Run(name, fn(tc))
	}
}
