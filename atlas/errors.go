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

type ErrMapNotFound struct {
	Name string
}

func (e ErrMapNotFound) Error() string {
	return fmt.Sprintf("atlas: map (%v) not found", e.Name)
}
