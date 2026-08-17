package env

import (
	"fmt"
	"os"
	"strings"
)

// LegacyPrefix is the environment-variable prefix this project used before it
// was renamed to shigola. Prefix is the one it uses now.
const (
	Prefix       = "SHIGOLA_"
	LegacyPrefix = "TEGOLA_"
)

// Getenv reads a SHIGOLA_-prefixed environment variable, falling back to the
// TEGOLA_-prefixed name the same setting used before the rename.
//
// The fallback exists because an unset environment variable is not an error:
// dropping the old names outright would leave a deployment that still sets
// TEGOLA_OPTIONS running silently on defaults, with no log line, no start-up
// failure and no way to tell from the outside. A rename is not worth a silent
// change in behaviour, so the old name keeps working and says so.
//
// name is the unprefixed suffix — Getenv("OPTIONS") reads SHIGOLA_OPTIONS, then
// TEGOLA_OPTIONS.
func Getenv(name string) string {
	name = strings.TrimPrefix(strings.TrimPrefix(name, Prefix), LegacyPrefix)

	if v := os.Getenv(Prefix + name); v != "" {
		return v
	}

	v := os.Getenv(LegacyPrefix + name)
	if v != "" {
		// Not log.Warnf: this is read during package init, before the logger is
		// configured, so anything routed through the logging package would be
		// swallowed on the paths that need it most.
		fmt.Fprintf(os.Stderr,
			"warning: %s%s is deprecated and will be removed; use %s%s\n",
			LegacyPrefix, name, Prefix, name)
	}

	return v
}
