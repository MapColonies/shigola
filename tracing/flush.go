package tracing

import (
	"context"
	"time"

	"github.com/MapColonies/shigola/internal/log"
)

// FlushTimeout bounds the shutdown export.
//
// Short on purpose. A collector that has gone away does not fail fast — the
// exporter's own per-export timeout is measured in seconds and it retries — so
// without a bound here a dead Tempo would hold the process open through its
// whole termination grace period, turning a missing trace into a failed
// rolling deploy.
const FlushTimeout = 2 * time.Second

// Flush exports the spans t's processor is still sitting on.
//
// The traces worth having are usually the ones from just before a shutdown,
// and a batch processor holds up to a batch interval's worth of them. A no-op
// when tracing is disabled, since the null backend has nothing to flush.
//
// Lives here rather than beside each caller because both `shigola serve` and
// `shigola cache seed|purge` need it and they are in different packages, so
// the alternative was the same function and the same timeout constant written
// out twice.
func Flush(t Interface) {
	if t == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), FlushTimeout)
	defer cancel()

	if err := t.Shutdown(ctx); err != nil {
		log.Errorf("flushing traces: %v", err)
	}
}
