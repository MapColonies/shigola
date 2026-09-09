package register

import (
	"context"

	"github.com/MapColonies/shigola/tracing"
)

// Tracing builds the tracing backend a config describes.
//
// Thinner than its Observer sibling because there is nothing to look up: the
// observer section names a registered backend by type, whereas tracing has one
// destination — OTLP — and the section only chooses its transport. A registry
// for a single entry would be indirection without a decision in it.
//
// A config with tracing disabled, or with no [tracing] section, yields the
// no-op backend and dials nothing.
func Tracing(ctx context.Context, config tracing.Config) (tracing.Interface, error) {
	return tracing.New(ctx, config)
}
