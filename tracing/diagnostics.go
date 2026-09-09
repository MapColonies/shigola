package tracing

import (
	"log/slog"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"

	"github.com/MapColonies/shigola/internal/log"
)

// installDiagnostics routes OTEL's own errors and diagnostics through
// shigola's logger, at levels that mean what they say.
//
// Without it, a failed export is reported at **INFO**. The chain is worth
// spelling out because every link is a sensible default on its own:
//
//  1. the batch span processor reports an export failure with otel.Handle
//  2. OTEL's default error handler, absent an override, calls the standard
//     library's log.Print
//  3. slog.SetDefault — which cmd/shigola/cmd/root.go calls — redirects the
//     standard library's *default* logger through the slog handler at INFO
//
// So a collector that has gone away, or an endpoint the exporter cannot parse,
// announces itself at INFO and is invisible to a service running at
// --log-level WARN. That is how a deployment ran with tracing enabled and
// every single export failing: nothing above INFO ever said so.
//
// The error handler fixes the level. The logger bridge fixes the shape: OTEL's
// default internal logger writes plain text straight to stderr, which in a
// deployment that parses structured logs is a line nobody sees either. logr's
// V-levels map onto slog's, so OTEL's errors arrive as errors and its chatter
// arrives as debug.
//
// Called from Install, since like everything else there it is process-wide
// state and only means anything once this backend is the process's backend.
func installDiagnostics() {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Errorf("tracing: %v", err)
	}))

	otel.SetLogger(logr.FromSlogHandler(slog.Default().Handler()))
}
