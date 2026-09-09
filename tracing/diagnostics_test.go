package tracing_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/MapColonies/shigola/internal/faketracer"
)

// capturing records the level and message of every log record it handles.
type capturing struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *capturing) Enabled(context.Context, slog.Level) bool { return true }

func (c *capturing) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.records = append(c.records, r.Clone())

	return nil
}

func (c *capturing) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *capturing) WithGroup(string) slog.Handler { return c }

func (c *capturing) find(substr string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range c.records {
		if strings.Contains(r.Message, substr) {
			return r, true
		}
	}

	return slog.Record{}, false
}

// TestInstallRoutesOtelErrorsToErrorLevel is the regression guard for a failure
// that hid in plain sight: tracing enabled, every export failing, and nothing
// above INFO saying so.
//
// Three sensible defaults compose into that. The batch span processor reports
// an export failure with otel.Handle; OTEL's default error handler, absent an
// override, calls the standard library's log.Print; and slog.SetDefault
// redirects the standard library's default logger through the slog handler at
// INFO. A service at --log-level WARN therefore sees nothing at all.
//
// Install now sets an error handler of its own, so this asserts the level as
// much as the routing.
func TestInstallRoutesOtelErrorsToErrorLevel(t *testing.T) {
	beforeTP := otel.GetTracerProvider()
	beforeProp := otel.GetTextMapPropagator()
	beforeLog := slog.Default()
	t.Cleanup(func() {
		otel.SetTracerProvider(beforeTP)
		otel.SetTextMapPropagator(beforeProp)
		slog.SetDefault(beforeLog)
	})

	handler := &capturing{}
	slog.SetDefault(slog.New(handler))

	backend, _ := faketracer.New(t)
	backend.Install()

	otel.Handle(errors.New("traces export: collector unreachable"))

	record, ok := handler.find("collector unreachable")
	if !ok {
		t.Fatal("an OTEL error reached no logger at all")
	}
	if record.Level != slog.LevelError {
		t.Errorf("an OTEL export failure logged at %v, want ERROR — it is invisible above that level", record.Level)
	}
}
