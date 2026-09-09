package fakelog_test

import (
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/MapColonies/shigola/internal/fakelog"
	"github.com/MapColonies/shigola/internal/log"
)

// A test helper that silently found the wrong record, or no record, would
// weaken three packages' assertions without failing anything, so its own
// searching is worth pinning.

func TestRecorderReadsWhatWasWritten(t *testing.T) {
	logger, rec := fakelog.New()

	if !rec.Empty() {
		t.Error("a fresh recorder is not empty")
	}

	logger.Info("first")
	logger.WarnContext(context.Background(), "second: a literal % stays literal")

	records := rec.Records(t)
	if len(records) != 2 {
		t.Fatalf("%d records, want 2", len(records))
	}
	if got, want := fakelog.Messages(records), []string{"first", "second: a literal % stays literal"}; got[0] != want[0] || got[1] != want[1] {
		t.Errorf("messages = %v, want %v", got, want)
	}
	if rec.Empty() {
		t.Error("a written-to recorder reports empty")
	}

	if got := rec.Containing(t, "second")["level"]; got != "WARN" {
		t.Errorf("level = %v, want WARN", got)
	}
}

// Default has to reach the package-level helpers, which is the only way the
// converted request-path call sites can be observed at all.
func TestDefaultCapturesThePackageHelpers(t *testing.T) {
	previous := slog.Default()

	rec := fakelog.Default(t)
	log.ErrorfContext(fakelog.TracedContext(true), "tier (%v) get: down", "redis")

	record := rec.One(t)
	if got, want := record["msg"], "tier (redis) get: down"; got != want {
		t.Errorf("msg = %v, want %v", got, want)
	}
	fakelog.AssertCorrelation(t, record, fakelog.TraceIDHex, fakelog.SpanIDHex)

	if slog.Default() == previous {
		t.Error("Default did not install its logger")
	}
}

// The hex constants and the byte arrays have to agree, or an assertion written
// against one would pass against a record built from the other.
func TestFixedIDsAgreeWithTheirHex(t *testing.T) {
	if got := fakelog.TraceID.String(); got != fakelog.TraceIDHex {
		t.Errorf("TraceID = %v, want %v", got, fakelog.TraceIDHex)
	}
	if got := fakelog.SpanID.String(); got != fakelog.SpanIDHex {
		t.Errorf("SpanID = %v, want %v", got, fakelog.SpanIDHex)
	}
}

func TestTracedContextSamplingFlag(t *testing.T) {
	for _, sampled := range []bool{true, false} {
		ctx := fakelog.TracedContext(sampled)
		sc := trace.SpanContextFromContext(ctx)
		if !sc.IsValid() {
			t.Fatalf("sampled=%v: the span context is not valid", sampled)
		}
		if sc.IsSampled() != sampled {
			t.Errorf("sampled=%v: IsSampled() = %v", sampled, sc.IsSampled())
		}
	}
}
