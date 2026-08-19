package cache

import (
	"errors"
	"fmt"
)

type ErrInvalidFileKeyParts struct {
	path          string
	keyPartsCount int
}

func (e ErrInvalidFileKeyParts) Error() string {
	return fmt.Sprintf("cache: invalid fileKey (%v). expecting between three and five parts, got (%v) skipping.", e.path, e.keyPartsCount)
}

type ErrInvalidFileKey struct {
	path string
	key  string
	val  string
}

func (e ErrInvalidFileKey) Error() string {
	return fmt.Sprintf("cache: invalid fileKey (%v). unable to parse (%v) value (%v) into int", e.path, e.key, e.val)
}

type ErrGettingFromCache struct {
	Err       error
	CacheType string
}

func (e ErrGettingFromCache) Error() string {
	return fmt.Sprintf("cache: error getting from (%v) cache: %v", e.CacheType, e.Err)
}

type ErrSettingToCache struct {
	Err       error
	CacheType string
}

func (e ErrSettingToCache) Error() string {
	return fmt.Sprintf("cache: error setting to (%v) cache: %v", e.CacheType, e.Err)
}

type ErrPurgingCache struct {
	Err       error
	CacheType string
}

func (e ErrPurgingCache) Error() string {
	return fmt.Sprintf("cache: error purging (%v) cache: %v", e.CacheType, e.Err)
}

// ErrNilGrid reports a cache key built without a tiling scheme.
//
// It is not defaulted: Key.String reads an empty TileMatrixSetID as
// WebMercatorQuad, so accepting nil would file another scheme's tiles under
// WebMercatorQuad's keys — silently, and only for the caller that got it wrong.
var ErrNilGrid = errors.New("cache: a tile matrix set is required to build a cache key")
