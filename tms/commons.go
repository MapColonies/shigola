package tms

// Ported from morecantile/commons.py (MIT, Development Seed).

import "fmt"

// Tile is a TileMatrixSet X,Y,Z tile index.
//
// X is the column index, Y the row index, and Z the zoom level — that is, the
// identifier of the TileMatrix within the TileMatrixSet.
//
// Note that OGC API - Tiles orders tile paths as {tileMatrix}/{tileRow}/{tileCol},
// i.e. z/y/x, whereas this struct — like morecantile and tegola's native
// routes — is written x, y, z. Construct tiles by field name at request
// boundaries to avoid transposing rows and columns.
type Tile struct {
	X int64
	Y int64
	Z int
}

// String renders the tile as "Tile(x=1, y=2, z=3)", matching the repr
// morecantile's tests assert on.
func (t Tile) String() string {
	return fmt.Sprintf("Tile(x=%d, y=%d, z=%d)", t.X, t.Y, t.Z)
}

// Coords is an X,Y coordinate pair in some coordinate reference system. Which
// CRS is determined by the function returning it: the XY-prefixed methods of
// TileMatrixSet work in the grid's own CRS, the rest in geographic coordinates.
type Coords struct {
	X float64
	Y float64
}

// BoundingBox is a min/max coordinate pair, in the axis order (left, bottom,
// right, top) — that is, (xmin, ymin, xmax, ymax).
type BoundingBox struct {
	Left   float64
	Bottom float64
	Right  float64
	Top    float64
}

// validateTile rejects a tile whose zoom could not name a TileMatrix.
//
// It stands in for morecantile's _parse_tile_arg, which mostly exists to accept
// either a Tile or three loose ints — an arity Go's type system already
// guarantees. What remains is the zoom check, so this validates rather than
// converting.
func validateTile(t Tile) error {
	if t.Z < 0 {
		return TileArgParsingError{
			Message: fmt.Sprintf("tile zoom must not be negative, got %d", t.Z),
		}
	}

	return nil
}
