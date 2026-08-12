package tms

// The coordinate-transform seam.
//
// morecantile hands every CRS conversion to pyproj. This port has no PROJ
// backend and stays cgo-free, so each grid gets a Transformer that either does
// the conversion arithmetically or reports ErrNoTransformBackend.
//
// Only conversions between a grid's CRS and geographic coordinates need this.
// Tile-index arithmetic — MatrixSize, XYBounds, Tile, Quadkey and the rest — is
// closed-form over the grid's matrix definitions and works for every bundled
// grid regardless of whether a Transformer is available.

import "math"

// Transformer converts between a grid's own CRS and geographic (longitude,
// latitude) coordinates.
//
// Coordinates are always passed in x/y (longitude/latitude) order regardless of
// the axis order the CRS declares, matching pyproj's always_xy=True, which is
// what morecantile uses throughout.
type Transformer interface {
	// ToGeographic converts a point in the grid's CRS to longitude/latitude.
	ToGeographic(x, y float64) (lon, lat float64, err error)
	// FromGeographic converts a longitude/latitude point into the grid's CRS.
	FromGeographic(lon, lat float64) (x, y float64, err error)
}

// BoundsTransformer is an optional refinement of Transformer for coordinate
// reference systems whose transform is not separable and monotonic per axis.
//
// The default bounds conversion transforms only the two opposing corners of a
// box, which is exact when each output axis depends monotonically on a single
// input axis — true of the identity and of Mercator, the transforms this build
// ships. pyproj instead densifies each edge with intermediate points
// (densify_pts=21) because it must cope with arbitrary projections, where an
// edge can bow beyond its endpoints. A Transformer for such a CRS should
// implement this interface so bounds stay correct.
type BoundsTransformer interface {
	TransformBoundsToGeographic(bbox BoundingBox) (BoundingBox, error)
}

// identityTransformer serves grids whose CRS is already geographic, where
// converting to longitude/latitude is a no-op.
//
// It applies to CRS84 and to EPSG:4326: although EPSG:4326 declares its axes as
// (lat, lon), the ported algorithms read pointOfOrigin through matrixOrigin,
// which un-inverts the axis order first, so coordinates reaching a Transformer
// are always (lon, lat).
type identityTransformer struct{}

func (identityTransformer) ToGeographic(x, y float64) (float64, float64, error) {
	return x, y, nil
}

func (identityTransformer) FromGeographic(lon, lat float64) (float64, float64, error) {
	return lon, lat, nil
}

// webMercatorTransformer implements EPSG:3857 (WGS 84 / Pseudo-Mercator), the
// spherical Mercator projection of the WGS 84 datum, in closed form.
//
// Latitude is deliberately *not* clamped. tegola's maths/webmercator.LatToY
// clamps to +/-89.5 degrees, which is fine for a grid that only ever reaches
// +/-85.05, but clamping here would silently corrupt the grid bounds this
// package reports. Latitudes beyond the projection's domain produce infinities,
// as they mathematically should.
type webMercatorTransformer struct {
	// radius is the semi-major axis of the datum's ellipsoid, used as the
	// sphere radius by the pseudo-Mercator definition.
	radius float64
}

func (t webMercatorTransformer) ToGeographic(x, y float64) (float64, float64, error) {
	lon := x / t.radius * 180.0 / math.Pi
	lat := (2*math.Atan(math.Exp(y/t.radius)) - math.Pi/2) * 180.0 / math.Pi

	return wrapLongitude(lon), lat, nil
}

// wrapLongitude brings a longitude back into [-180, 180] by whole turns.
//
// PROJ normalises longitude this way, so morecantile's own test pins the wrapped
// value for an x far outside the grid — the arithmetic alone would report
// something like -254 degrees. Values already in range are returned untouched, so
// that +180 stays +180 rather than folding to -180 and inverting the grid's
// eastern edge.
func wrapLongitude(lon float64) float64 {
	if lon >= -180 && lon <= 180 {
		return lon
	}

	wrapped := math.Mod(lon+180, 360)
	if wrapped < 0 {
		wrapped += 360
	}

	return wrapped - 180
}

func (t webMercatorTransformer) FromGeographic(lon, lat float64) (float64, float64, error) {
	x := lon * math.Pi / 180.0 * t.radius
	y := t.radius * math.Log(math.Tan(math.Pi/4+(lat*math.Pi/180.0)/2))

	return x, y, nil
}

// unavailableTransformer stands in for the CRSs this build cannot transform. It
// keeps a grid's definition, model and tile arithmetic usable while making any
// geographic conversion fail loudly rather than silently returning wrong
// numbers.
type unavailableTransformer struct {
	crs string
}

func (t unavailableTransformer) ToGeographic(_, _ float64) (float64, float64, error) {
	return 0, 0, t.err()
}

func (t unavailableTransformer) FromGeographic(_, _ float64) (float64, float64, error) {
	return 0, 0, t.err()
}

func (t unavailableTransformer) err() error {
	return UnsupportedCRSError{CRS: t.crs, Reason: ErrNoTransformBackend.Error()}
}

// transformer returns the Transformer for the CRS, which is
// unavailableTransformer when this build has no arithmetic conversion for it.
//
// Extending the set of usable grids means adding a case here, not touching any
// call site.
func (c crsInfo) transformer() Transformer {
	if c.geographic {
		return identityTransformer{}
	}

	if normalizeCRSKey(c.authority, c.code) == "EPSG:3857" {
		return webMercatorTransformer{radius: c.semiMajorMetre}
	}

	return unavailableTransformer{crs: c.uri}
}

// transformBoundsToGeographic converts a box in the grid's CRS to geographic
// coordinates, delegating to the Transformer when it knows how to do better
// than transforming corners.
func transformBoundsToGeographic(tr Transformer, bbox BoundingBox) (BoundingBox, error) {
	if bt, ok := tr.(BoundsTransformer); ok {
		return bt.TransformBoundsToGeographic(bbox)
	}

	left, top, err := tr.ToGeographic(bbox.Left, bbox.Top)
	if err != nil {
		return BoundingBox{}, err
	}

	right, bottom, err := tr.ToGeographic(bbox.Right, bbox.Bottom)
	if err != nil {
		return BoundingBox{}, err
	}

	return BoundingBox{
		Left:   math.Min(left, right),
		Bottom: math.Min(bottom, top),
		Right:  math.Max(left, right),
		Top:    math.Max(bottom, top),
	}, nil
}
