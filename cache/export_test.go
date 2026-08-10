package cache

import "time"

// Test-only handles on things that are deliberately not public API.
//
// The two decorators are internal because For applies them and nothing else
// should construct them. Tests need to, so they can build a pool of one or two
// slots and reach exhaustion in two lines instead of a 256-goroutine stress
// test. export_test.go is compiled only into the test binary, so none of this
// widens the package's surface.
//
// They cannot simply be internal tests: internal/faketier imports cache,
// so a test file in package cache that used it would be an import cycle.
var (
	NewDetachedCache = newDetachedCache
	NewDeadlineCache = newDeadlineCache
	ParseOptions     = parseOptions
)

// WriteOptions returns the parsed TEGOLA_OPTIONS write-path switches.
func WriteOptions() (slots int, timeout, drain time.Duration) {
	return detachedWriteSlots, detachedWriteTimeout, detachedWriteDrain
}

// ResetWriteOptions restores the compiled-in defaults, for a test that has just
// parsed something else into the package globals.
func ResetWriteOptions() {
	detachedWriteSlots = defaultDetachedWriteSlots
	detachedWriteTimeout = time.Duration(defaultDetachedWriteTimeoutMs) * time.Millisecond
	detachedWriteDrain = time.Duration(defaultDetachedWriteDrainMs) * time.Millisecond
}
