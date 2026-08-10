package multi

import (
	"errors"
	"fmt"
)

// ErrNoLayers is returned when a multi cache is configured with no tiers. A
// chain over nothing is never what was meant.
var ErrNoLayers = errors.New("cache/multi: no cache.layers configured")

// ErrDuplicateTierName is returned when two tiers resolve to the same name. An
// explicit name exists to be a stable unique label; two of them is a metric
// label collision and an ambiguous --cache-tiers selector.
type ErrDuplicateTierName struct {
	Name string
}

func (e ErrDuplicateTierName) Error() string {
	return fmt.Sprintf("cache/multi: duplicate tier name (%v)", e.Name)
}

// ErrUnknownTier is returned when a tier is named that the chain does not have
// — by --cache-tiers, at startup, rather than as a silent no-op at run time.
type ErrUnknownTier struct {
	Name  string
	Known []string
}

func (e ErrUnknownTier) Error() string {
	return fmt.Sprintf("cache/multi: unknown tier (%v). known tiers: %v", e.Name, e.Known)
}

// ErrTier wraps a failure from one tier so a joined error names which tier
// failed and on which operation.
type ErrTier struct {
	Name string
	Op   string
	Err  error
}

func (e ErrTier) Error() string {
	return fmt.Sprintf("cache/multi: tier (%v) %v: %v", e.Name, e.Op, e.Err)
}

func (e ErrTier) Unwrap() error { return e.Err }
