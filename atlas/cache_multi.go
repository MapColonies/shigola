package atlas

// The point of this file is to load and register the multi (layered) cache
// backend.
//
// There is no build tag, unlike the other backends. A tag exists to exclude a
// backend's external dependencies from the binary, and the chain has none — it
// is composed entirely of caches that are themselves already registered.
import (
	_ "github.com/MapColonies/shigola/cache/multi"
)
