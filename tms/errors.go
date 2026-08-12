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

// InvalidIdentifierError reports a tileMatrixSetId that is not registered.
type InvalidIdentifierError struct {
	Identifier string
}

func (e InvalidIdentifierError) Error() string {
	return fmt.Sprintf("tms: invalid TileMatrixSet identifier %q", e.Identifier)
}

// InvalidZoomError reports a zoom level that cannot be resolved to a
// TileMatrix.
type InvalidZoomError struct {
	Message string
}

func (e InvalidZoomError) Error() string {
	return "tms: invalid zoom: " + e.Message
}

// TileArgParsingError reports a malformed tile argument.
type TileArgParsingError struct {
	Message string
}

func (e TileArgParsingError) Error() string {
	return "tms: invalid tile argument: " + e.Message
}

// NoQuadkeySupportError reports that a TileMatrixSet is not a 2x2 quadtree and
// so has no quadkeys.
type NoQuadkeySupportError struct {
	Identifier string
}

func (e NoQuadkeySupportError) Error() string {
	return fmt.Sprintf("tms: TileMatrixSet %q does not support 2 x 2 quadkeys", e.Identifier)
}

// QuadKeyError reports a malformed quadkey.
type QuadKeyError struct {
	Message string
}

func (e QuadKeyError) Error() string {
	return "tms: invalid quadkey: " + e.Message
}

// UnsupportedCRSError reports a CRS this package has no metadata for. Because
// the port carries no PROJ backend, CRS knowledge comes from the table in
// crs.go; a CRS outside it cannot have its metersPerUnit or axis order
// resolved.
type UnsupportedCRSError struct {
	CRS    string
	Reason string
}

func (e UnsupportedCRSError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("tms: unsupported CRS %q", e.CRS)
	}

	return fmt.Sprintf("tms: unsupported CRS %q: %s", e.CRS, e.Reason)
}

// Unwrap lets errors.Is find ErrNoTransformBackend through a CRS error raised by
// a grid that has no arithmetic transform.
func (e UnsupportedCRSError) Unwrap() error {
	if e.Reason == ErrNoTransformBackend.Error() {
		return ErrNoTransformBackend
	}

	return nil
}

// PointOutsideBoundsError reports a point outside a grid's bounds.
//
// morecantile raises this as a Python *warning* and carries on returning
// coordinates, so the ported algorithms do not fail on it. It is exported for
// callers that want to check explicitly — see TileMatrixSet.PointInBounds.
type PointOutsideBoundsError struct {
	Point  Coords
	Bounds BoundingBox
}

func (e PointOutsideBoundsError) Error() string {
	return fmt.Sprintf("tms: point (%v, %v) is outside bounds [%v %v %v %v]",
		e.Point.X, e.Point.Y,
		e.Bounds.Left, e.Bounds.Bottom, e.Bounds.Right, e.Bounds.Top)
}
