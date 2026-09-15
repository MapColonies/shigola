package shigola

import (
	"github.com/go-spatial/geom/slippy"
)

const (
	DefaultExtent     = 4096
	DefaultTileBuffer = 64.0
	MaxZ              = 22
)

// MediaTypeMVT is the media type of a Mapbox Vector Tile.
//
// Declared here rather than taken from a library. It used to come from
// go-spatial/geom's encoding/mvt, which nothing in this tree used for anything
// else and which was one of the two packages dragging in the deprecated
// github.com/golang/protobuf (MAPCO-11516).
//
// server/ogc declares the same string, under the same name, among the media
// types it negotiates. That list is the OGC surface's vocabulary rather than a
// fact about tiles, so the duplication is deliberate: aliasing one of its four
// entries to here would read as though the other three were also ours to
// define. The names match so that grepping either one finds both.
const MediaTypeMVT = "application/vnd.mapbox-vector-tile"

// Tile is a slippy map tilename.
// http://wiki.openstreetmap.org/wiki/Slippy_map_tilenames
//
// MAPCO-11492 reduced this to the three coordinates its callers read. It used
// to carry the lat/long of its origin, a tolerance, an extent, a buffer and a
// cached bounding box, along with the projection helpers that computed them.
// All of that served the Go-side encode path: nothing outside this file ever
// read a field other than Z, X and Y. A tiling scheme's geometry now lives in
// tms.TileMatrixSet, and a provider takes its extent from provider.Tile, so
// there is no second place for a tile to describe its own bounds.
type Tile struct {
	Z uint
	X uint
	Y uint
}

// NewTile returns a non-nil tile at the given coordinates.
func NewTile(z, x, y uint) (t *Tile) {
	return &Tile{Z: z, X: x, Y: y}
}

func TileFromSlippyTile(t slippy.Tile) *Tile { return NewTile(uint(t.Z), t.X, t.Y) }
