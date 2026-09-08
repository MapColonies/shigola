package atlas

import (
	"errors"
	"fmt"
)

var (
	ErrMissingCache = errors.New("atlas: missing cache")
	ErrMissingTile  = errors.New("atlas: missing tile")
	// ErrNilGrid is returned by the seams that cut a tile — Encode, SeedMapTile
	// and PurgeMapTile — when the caller named no tiling scheme. It mirrors
	// cache.ErrNilGrid, and for the same reason: a defaulted grid encodes one
	// scheme's ground under another scheme's key, silently and only for the
	// caller that got it wrong.
	ErrNilGrid = errors.New("atlas: no tile matrix set given")
)

// ErrNoMVTProvider is returned by Encode for a map that has no provider to ask.
//
// Registration cannot build such a map -- selectProvider fails first -- so this
// covers an atlas.Map assembled in code, which is how tests and embedders make
// one. Before MAPCO-11491 that map still encoded, by walking its layers through
// the Go-side path; with that path gone the honest answer is that there is
// nothing to encode from, said here rather than as a nil dereference.
type ErrNoMVTProvider struct {
	Map string
}

func (e ErrNoMVTProvider) Error() string {
	return fmt.Sprintf("atlas: map (%v) has no mvt provider", e.Map)
}

type ErrMapNotFound struct {
	Name string
}

func (e ErrMapNotFound) Error() string {
	return fmt.Sprintf("atlas: map (%v) not found", e.Name)
}
