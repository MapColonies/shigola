package cache

import (
	"context"
	"time"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/internal/log"
)

// tracingFlushTimeout bounds the shutdown export, for the reason the server
// command's namesake gives: a collector that has gone away does not fail fast,
// and an unbounded flush would hold a finished seed open on it.
const tracingFlushTimeout = 2 * time.Second

// flushTracing exports the spans the batch processor is still sitting on.
//
// A no-op when tracing is disabled.
func flushTracing() {
	ctx, cancel := context.WithTimeout(context.Background(), tracingFlushTimeout)
	defer cancel()

	if err := atlas.Tracing().Shutdown(ctx); err != nil {
		log.Errorf("flushing traces: %v", err)
	}
}
