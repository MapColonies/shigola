package tms

// Ported from morecantile/errors.py (MIT, Development Seed), plus
// ErrNoTransformBackend, which this port needs because it has no PROJ backend.

import (
	"errors"
	"fmt"
)

// ErrNoTransformBackend is returned when an operation needs to convert between
// a grid's CRS and geographic coordinates but this package has no arithmetic
// Transformer for that CRS.
//
// This is what gates the projected grids: their definitions, model and tests
// are all present, but their registry factories report this error until a
// transform backend is wired in. It is not a defect — callers are expected to
// treat it as "this grid is known but not available in this build".
var ErrNoTransformBackend = errors.New("tms: no coordinate transform backend for this CRS")

// ErrTMSVersion1 is returned when parsing a TileMatrixSet document that uses
// OGC TMS 1.0 keywords (supportedCRS / topLeftCorner). Only TMS 2.0 documents
// are supported, matching morecantile's DeprecationError.
var ErrTMSVersion1 = errors.New("tms: tileMatrixSet document must be version 2.0, found the 1.0 keywords supportedCRS or topLeftCorner")

// ErrGridNotActivated reports a grid this build is capable of serving but does
// not list in its activation set.
//
// It exists so that the activation set stays the authority on which grids are
// offered: a grid that gains a transform backend does not become available until
// it is deliberately activated, and until then it says so accurately rather than
// borrowing ErrNoTransformBackend's reason.
var ErrGridNotActivated = errors.New("tms: tileMatrixSet is not in this build's activation set")

// ErrInvalidIdentifier reports a tileMatrixSetId that is not registered.
type ErrInvalidIdentifier struct {
	Identifier string
}

func (e ErrInvalidIdentifier) Error() string {
	return fmt.Sprintf("tms: invalid TileMatrixSet identifier %q", e.Identifier)
}

// ErrInvalidZoom reports a zoom level that cannot be resolved to a
// TileMatrix.
type ErrInvalidZoom struct {
	Message string
}

func (e ErrInvalidZoom) Error() string {
	return "tms: invalid zoom: " + e.Message
}

// ErrTileArgParsing reports a malformed tile argument.
type ErrTileArgParsing struct {
	Message string
}

func (e ErrTileArgParsing) Error() string {
	return "tms: invalid tile argument: " + e.Message
}

// ErrNoQuadkeySupport reports that a TileMatrixSet is not a 2x2 quadtree and
// so has no quadkeys.
type ErrNoQuadkeySupport struct {
	Identifier string
}

func (e ErrNoQuadkeySupport) Error() string {
	return fmt.Sprintf("tms: TileMatrixSet %q does not support 2 x 2 quadkeys", e.Identifier)
}

// ErrQuadKey reports a malformed quadkey.
type ErrQuadKey struct {
	Message string
}

func (e ErrQuadKey) Error() string {
	return "tms: invalid quadkey: " + e.Message
}

// ErrUnsupportedCRS reports a CRS this package has no metadata for. Because
// the port carries no PROJ backend, CRS knowledge comes from the table in
// crs.go; a CRS outside it cannot have its metersPerUnit or axis order
// resolved.
type ErrUnsupportedCRS struct {
	CRS    string
	Reason string
}

func (e ErrUnsupportedCRS) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("tms: unsupported CRS %q", e.CRS)
	}

	return fmt.Sprintf("tms: unsupported CRS %q: %s", e.CRS, e.Reason)
}

// Unwrap lets errors.Is find ErrNoTransformBackend through a CRS error raised by
// a grid that has no arithmetic transform.
func (e ErrUnsupportedCRS) Unwrap() error {
	if e.Reason == ErrNoTransformBackend.Error() {
		return ErrNoTransformBackend
	}

	return nil
}

// ErrPointOutsideBounds reports a point outside a grid's bounds.
//
// morecantile raises this as a Python *warning* and carries on returning
// coordinates, so the ported algorithms do not fail on it. It is exported for
// callers that want to check explicitly — see TileMatrixSet.PointInBounds.
type ErrPointOutsideBounds struct {
	Point  Coords
	Bounds BoundingBox
}

func (e ErrPointOutsideBounds) Error() string {
	return fmt.Sprintf("tms: point (%v, %v) is outside bounds [%v %v %v %v]",
		e.Point.X, e.Point.Y,
		e.Bounds.Left, e.Bounds.Bottom, e.Bounds.Right, e.Bounds.Top)
}

// ErrVariableWidthUnsupported reports a grid whose tile matrices coalesce
// columns. The document model and tile arithmetic handle these grids, but
// tegola's tile pipeline assumes a tile's column index maps to one column of
// the matrix, so they are not activated.
var ErrVariableWidthUnsupported = errors.New("tms: variable-width tile matrices are not supported by the tile pipeline")

// ErrGridUnavailable reports a registered grid that this build cannot serve.
//
// It wraps the underlying reason, so errors.Is(err, ErrNoTransformBackend) and
// errors.Is(err, ErrVariableWidthUnsupported) both work.
type ErrGridUnavailable struct {
	ID     string
	Reason error
}

func (e ErrGridUnavailable) Error() string {
	return fmt.Sprintf("tms: TileMatrixSet %q is not available in this build: %v", e.ID, e.Reason)
}

func (e ErrGridUnavailable) Unwrap() error { return e.Reason }

// Axis names one of the two independently bounded tile axes.
const (
	AxisX = "x"
	AxisY = "y"
)

// ErrTileOutsideMatrix reports a tile index outside its scheme's matrix at a
// zoom level.
//
// Axis says which of the two bounds was broken, and Cols/Rows carry the matrix
// it was checked against, so a caller can phrase the failure in its own
// vocabulary — "X"/"Y" on the native routes, "tileCol"/"tileRow" on the OGC
// ones — without repeating the arithmetic.
type ErrTileOutsideMatrix struct {
	GridID     string
	Z          int
	X, Y       int64
	Cols, Rows int64
	Axis       string
}

func (e ErrTileOutsideMatrix) Error() string {
	return fmt.Sprintf(
		"tms: tile %v/%v/%v is outside tile matrix set %v, whose matrix at zoom %v is %d columns by %d rows",
		e.Z, e.X, e.Y, e.GridID, e.Z, e.Cols, e.Rows)
}
