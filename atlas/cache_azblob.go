//go:build !noAzblobCache
// +build !noAzblobCache

package atlas

// The point of this file is to load and register the azblob cache backend.
// the azblob cache can be excluded during the build with the `noAzblobCache` build flag
// for example from the cmd/shigola directory:
//
// go build -tags 'noAzblobCache'
import (
	_ "github.com/MapColonies/shigola/cache/azblob"
)
