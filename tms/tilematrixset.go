package tms

// The tile algorithms, ported from morecantile/models.py (MIT, Development
// Seed). model.go holds the document these operate on.
//
// Method naming follows one rule: XY-prefixed methods work in the grid's own
// CRS and need no coordinate transform, while the unprefixed geographic
// equivalents do. That split is what lets every bundled grid compute tile
// indices and extents even when this build cannot transform its CRS.

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/go-spatial/geom"
)

// llEpsilon is the small offset Tiles applies to its bounds so that a box
// exactly matching one tile yields exactly that tile. From morecantile's
// LL_EPSILON, itself from mapbox/mercantile.
const llEpsilon = 1e-11

// screenPixelSize is the 0.28 mm rendering pixel size OGC standardised on, used
// to relate a scale denominator to a cell size.
const screenPixelSize = 0.28e-3

// Zoom-level selection strategies for ZoomForRes, matching GDAL 3.2's
// ZOOM_LEVEL_STRATEGY and morecantile's zoom_level_strategy argument.
const (
	// ZoomStrategyAuto picks whichever neighbouring zoom level is closest.
	ZoomStrategyAuto = "auto"
	// ZoomStrategyLower picks the zoom immediately below the computed
	// non-integral level.
	ZoomStrategyLower = "lower"
	// ZoomStrategyUpper picks the zoom immediately above it.
	ZoomStrategyUpper = "upper"
)

// TileMatrixSet is a tiling scheme: an OGC TMS 2.0 definition together with the
// arithmetic that turns tile indices into coordinates.
//
// Construct one with New, or obtain a registered grid from the Registry.
// A TileMatrixSet is immutable once built and safe for concurrent use.
type TileMatrixSet struct {
	def Definition
	// raw is the definition exactly as it was parsed, so /tileMatrixSets/{id}
	// can serve the bundled document byte-for-byte rather than a re-marshalled
	// approximation of it.
	raw []byte

	// matrixIdx maps a zoom level to its index in def.TileMatrices.
	matrixIdx map[int]int

	// invertAxis reports whether this grid states its coordinates as
	// (lat, lon); see matrixOrigin.
	invertAxis bool

	crs           crsInfo
	metersPerUnit float64
	transformer   Transformer

	quadtree bool
	variable bool
}

// New builds a TileMatrixSet from a parsed definition.
//
// raw, when non-nil, is the definition's original JSON, retained for verbatim
// serving; pass nil for a definition that was not read from a document.
//
// New succeeds even when this build cannot transform the grid's CRS: the
// resulting grid's tile arithmetic is fully usable and only its geographic
// conversions fail. Callers that need a transform should check
// TransformAvailable — the Registry does this for them.
func New(def Definition, raw []byte) (*TileMatrixSet, error) {
	crsMeta, err := def.CRS.info()
	if err != nil {
		return nil, err
	}

	metersPerUnit, err := crsMeta.metersPerUnit()
	if err != nil {
		return nil, err
	}

	if len(def.TileMatrices) == 0 {
		return nil, fmt.Errorf("tms: TileMatrixSet %q has no tileMatrices", def.ID)
	}

	idx := make(map[int]int, len(def.TileMatrices))
	variable := false

	for i, m := range def.TileMatrices {
		z, err := m.Zoom()
		if err != nil {
			return nil, err
		}

		if prev, dup := idx[z]; dup {
			return nil, fmt.Errorf(
				"tms: TileMatrixSet %q has duplicate tileMatrix id %q (indices %d and %d)",
				def.ID, m.ID, prev, i)
		}

		idx[z] = i

		if len(m.VariableMatrixWidths) > 0 {
			variable = true
		}
	}

	invert := crsMeta.axisInverted
	if len(def.OrderedAxes) > 0 {
		invert = orderedAxisInverted(def.OrderedAxes)
	}

	return &TileMatrixSet{
		def:           def,
		raw:           raw,
		matrixIdx:     idx,
		invertAxis:    invert,
		crs:           crsMeta,
		metersPerUnit: metersPerUnit,
		transformer:   crsMeta.transformer(),
		quadtree:      checkQuadkeySupport(def.TileMatrices),
		variable:      variable,
	}, nil
}

/* ---------------------------------------------------------------- identity */

// ID returns the tileMatrixSetId, e.g. "WebMercatorQuad".
func (t *TileMatrixSet) ID() string { return t.def.ID }

// Title returns the grid's human-readable title, or "" when it declares none.
func (t *TileMatrixSet) Title() string {
	if t.def.Title == nil {
		return ""
	}

	return *t.def.Title
}

// URI returns the OGC definition-server URI identifying this tiling scheme,
// which is what a tileset's tileMatrixSetURI member should carry.
func (t *TileMatrixSet) URI() string { return t.def.URI }

// CRSURI returns the OGC definition-server URI of the grid's CRS.
func (t *TileMatrixSet) CRSURI() string { return t.crs.uri }

// OrderedAxes returns the declared axis names, or nil when the document leaves
// them to the CRS.
func (t *TileMatrixSet) OrderedAxes() []string {
	if len(t.def.OrderedAxes) == 0 {
		return nil
	}

	return append([]string(nil), t.def.OrderedAxes...)
}

// NativeSRID returns the EPSG code of the grid's CRS — the SRID tegola's
// pipeline works in when producing tiles for this grid.
//
// It reports 0 for a CRS with no EPSG code. OGC:CRS84 is the notable case: it is
// EPSG:4326's axes in longitude/latitude order, so callers needing an SRID for
// it should treat 0 as 4326.
func (t *TileMatrixSet) NativeSRID() (uint64, error) { return t.def.CRS.EPSG() }

// MetersPerUnit returns the coefficient converting the grid CRS's units to
// metres.
func (t *TileMatrixSet) MetersPerUnit() float64 { return t.metersPerUnit }

// Definition returns the OGC TMS 2.0 document for this grid.
func (t *TileMatrixSet) Definition() Definition { return t.def }

// DefinitionJSON returns the grid's definition as JSON.
//
// When the grid was built from a document, this is that document's bytes
// unchanged — which is what /tileMatrixSets/{id} should serve, so that clients
// see the canonical OGC definition rather than a re-encoding of it.
func (t *TileMatrixSet) DefinitionJSON() ([]byte, error) {
	if len(t.raw) > 0 {
		return append([]byte(nil), t.raw...), nil
	}

	return marshalDefinition(t.def)
}

// TransformAvailable reports whether this build can convert between the grid's
// CRS and geographic coordinates. When it is false, the XY-prefixed methods
// still work and the geographic ones return ErrNoTransformBackend.
func (t *TileMatrixSet) TransformAvailable() bool {
	_, ok := t.transformer.(unavailableTransformer)

	return !ok
}

// IsQuadtree reports whether the grid is a 2x2 quadtree, and so has quadkeys.
func (t *TileMatrixSet) IsQuadtree() bool { return t.quadtree }

// IsVariable reports whether any tile matrix has variable width.
func (t *TileMatrixSet) IsVariable() bool { return t.variable }

// MinZoom returns the lowest zoom level the grid defines.
func (t *TileMatrixSet) MinZoom() int {
	z, _ := t.def.TileMatrices[0].Zoom()

	return z
}

// MaxZoom returns the highest zoom level the grid defines.
func (t *TileMatrixSet) MaxZoom() int {
	z, _ := t.def.TileMatrices[len(t.def.TileMatrices)-1].Zoom()

	return z
}

/* ----------------------------------------------------------------- matrices */

// Matrix returns the TileMatrix for a zoom level.
//
// When the grid does not define that level, Matrix synthesises one by extending
// the grid's constant scale ratio past its deepest matrix, as morecantile does.
// That requires a uniform ratio and fixed-width matrices, so it fails for
// variable-width grids and for grids whose zoom levels do not share one ratio.
//
// Unlike morecantile, a zoom below MinZoom is rejected rather than extrapolated;
// the original loops forever on that input.
func (t *TileMatrixSet) Matrix(zoom int) (TileMatrix, error) {
	if idx, ok := t.matrixIdx[zoom]; ok {
		return t.def.TileMatrices[idx], nil
	}

	if t.variable {
		return TileMatrix{}, InvalidZoomError{Message: fmt.Sprintf(
			"tileMatrix not found for level %d: cannot construct one for a TileMatrixSet with variable width",
			zoom)}
	}

	if zoom < t.MinZoom() {
		return TileMatrix{}, InvalidZoomError{Message: fmt.Sprintf(
			"tileMatrix not found for level %d: below the TileMatrixSet's minimum zoom (%d)",
			zoom, t.MinZoom())}
	}

	ratio, err := t.scaleRatio()
	if err != nil {
		return TileMatrix{}, err
	}

	// factor is how much the matrix grows per level: the scale denominator
	// shrinks by ratio each level, so the matrix widens by 1/ratio.
	factor := 1 / ratio

	deepest := t.def.TileMatrices[len(t.def.TileMatrices)-1]
	synth := deepest

	for z := t.MaxZoom(); z < zoom; z++ {
		synth = TileMatrix{
			ID:                   strconv.Itoa(z + 1),
			ScaleDenominator:     synth.ScaleDenominator / factor,
			CellSize:             synth.CellSize / factor,
			CornerOfOrigin:       synth.CornerOfOrigin,
			PointOfOrigin:        synth.PointOfOrigin,
			TileWidth:            synth.TileWidth,
			TileHeight:           synth.TileHeight,
			MatrixWidth:          int64(float64(synth.MatrixWidth) * factor),
			MatrixHeight:         int64(float64(synth.MatrixHeight) * factor),
			VariableMatrixWidths: nil,
		}
	}

	return synth, nil
}

// scaleRatio returns the constant ratio between consecutive scale denominators,
// failing when the grid has more than one.
func (t *TileMatrixSet) scaleRatio() (float64, error) {
	matrices := t.def.TileMatrices
	if len(matrices) < 2 {
		return 0, InvalidZoomError{Message: fmt.Sprintf(
			"TileMatrixSet %q defines a single tileMatrix, so no scale ratio can be derived",
			t.def.ID)}
	}

	seen := make(map[float64]struct{})
	var ratio float64

	for i := 1; i < len(matrices); i++ {
		// Rounded to two decimals exactly as morecantile does, so that float
		// noise in a published definition does not read as a varying scale.
		r := roundFloat(matrices[i].ScaleDenominator/matrices[i-1].ScaleDenominator, 2)
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			ratio = r
		}
	}

	if len(seen) > 1 {
		return 0, InvalidZoomError{Message: fmt.Sprintf(
			"cannot construct a tileMatrix for TileMatrixSet %q: its scale ratio varies between levels",
			t.def.ID)}
	}

	if ratio == 0 {
		return 0, InvalidZoomError{Message: fmt.Sprintf(
			"TileMatrixSet %q has a zero scale ratio", t.def.ID)}
	}

	return ratio, nil
}

// matrixOrigin returns the matrix's point of origin as (x, y).
//
// pointOfOrigin is stated in the axis order the grid declares, so for a grid
// with inverted axes — EPSG:4326's (lat, lon), as WGS1984Quad and the GNOSIS
// grids use — the stored pair must be swapped. Every algorithm below reads the
// origin through here, which is why coordinates elsewhere in this package are
// uniformly (x, y).
//
// Ported from morecantile.models.TileMatrixSet._matrix_origin.
func (t *TileMatrixSet) matrixOrigin(m TileMatrix) Coords {
	if t.invertAxis {
		return Coords{X: m.PointOfOrigin[1], Y: m.PointOfOrigin[0]}
	}

	return Coords{X: m.PointOfOrigin[0], Y: m.PointOfOrigin[1]}
}

// MatrixSize returns the number of columns and rows at a zoom level.
//
// For a square grid such as WebMercatorQuad this is (2^z, 2^z); for the 2:1
// WorldCRS84Quad it is (2*2^z, 2^z). Per-axis validation of a requested tile
// must use both values — assuming a square pyramid is what ties tegola's
// current handlers to WebMercator.
func (t *TileMatrixSet) MatrixSize(zoom int) (cols, rows int64, err error) {
	m, err := t.Matrix(zoom)
	if err != nil {
		return 0, 0, err
	}

	return m.MatrixWidth, m.MatrixHeight, nil
}

// Extrema is the inclusive range of valid tile indices at a zoom level.
type Extrema struct {
	MinX, MaxX int64
	MinY, MaxY int64
}

// MinMax returns the tile index extrema for a zoom level.
//
// Ported from morecantile.models.TileMatrixSet.minmax.
func (t *TileMatrixSet) MinMax(zoom int) (Extrema, error) {
	m, err := t.Matrix(zoom)
	if err != nil {
		return Extrema{}, err
	}

	return Extrema{
		MinX: 0, MaxX: m.MatrixWidth - 1,
		MinY: 0, MaxY: m.MatrixHeight - 1,
	}, nil
}

// IsValid reports whether a tile exists in the grid.
//
// With strict false, a zoom deeper than the grid's deepest matrix is accepted
// for fixed-width grids, since Matrix can synthesise those levels.
//
// Ported from morecantile.models.TileMatrixSet.is_valid.
func (t *TileMatrixSet) IsValid(tile Tile, strict bool) bool {
	if err := validateTile(tile); err != nil {
		return false
	}

	disableOverzoom := t.variable || strict
	if tile.Z < t.MinZoom() || (disableOverzoom && tile.Z > t.MaxZoom()) {
		return false
	}

	m, err := t.Matrix(tile.Z)
	if err != nil {
		return false
	}

	return tile.X >= 0 && tile.X <= m.MatrixWidth-1 &&
		tile.Y >= 0 && tile.Y <= m.MatrixHeight-1
}

/* ------------------------------------------------- tile indices from coords */

// TileFromXY returns the tile containing a point given in the grid's CRS.
//
// Indices are clamped into the matrix, so a point outside the grid yields the
// nearest edge tile rather than an error — morecantile's behaviour, which Tiles
// relies on. Pass ignoreCoalescence false to snap the column to the start of
// its coalesced run on a variable-width grid.
//
// Ported from morecantile.models.TileMatrixSet._tile.
func (t *TileMatrixSet) TileFromXY(x, y float64, zoom int, ignoreCoalescence bool) (Tile, error) {
	m, err := t.Matrix(zoom)
	if err != nil {
		return Tile{}, err
	}

	origin := t.matrixOrigin(m)

	var xtile int64
	if math.IsInf(x, 0) {
		xtile = 0
	} else {
		xtile = int64(math.Floor((x - origin.X) / (m.CellSize * float64(m.TileWidth))))
	}

	var ytile int64
	if math.IsInf(y, 0) {
		ytile = 0
	} else {
		coord := y - origin.Y
		if m.CornerOfOrigin == CornerTopLeft {
			coord = origin.Y - y
		}

		ytile = int64(math.Floor(coord / (m.CellSize * float64(m.TileHeight))))
	}

	// Avoid out-of-range tiles.
	if ytile < 0 {
		ytile = 0
	}

	if ytile >= m.MatrixHeight {
		ytile = m.MatrixHeight - 1
	}

	if xtile < 0 {
		xtile = 0
	}

	if xtile >= m.MatrixWidth {
		xtile = m.MatrixWidth - 1
	}

	if !ignoreCoalescence {
		cf, err := m.CoalesceFactor(ytile)
		if err != nil {
			return Tile{}, err
		}

		if cf != 1 && xtile%cf != 0 {
			xtile -= xtile % cf
		}
	}

	return Tile{X: xtile, Y: ytile, Z: zoom}, nil
}

// TileFromLngLat returns the tile containing a geographic point.
//
// truncate clamps the point into the grid's geographic bounds first; without it
// a point outside those bounds still returns the nearest edge tile, as
// morecantile does after warning.
//
// Ported from morecantile.models.TileMatrixSet.tile.
func (t *TileMatrixSet) TileFromLngLat(lng, lat float64, zoom int, truncate, ignoreCoalescence bool) (Tile, error) {
	if truncate {
		bbox, err := t.BBox()
		if err != nil {
			return Tile{}, err
		}

		lng, lat = truncateCoordinates(lng, lat, bbox)
	}

	x, y, err := t.transformer.FromGeographic(lng, lat)
	if err != nil {
		return Tile{}, err
	}

	return t.TileFromXY(x, y, zoom, ignoreCoalescence)
}

/* --------------------------------------------------- coords from tile index */

// UpperLeftXY returns a tile's upper-left corner in the grid's CRS.
//
// Ported from morecantile.models.TileMatrixSet._ul.
func (t *TileMatrixSet) UpperLeftXY(tile Tile) (Coords, error) {
	m, cf, origin, err := t.tileFrame(tile)
	if err != nil {
		return Coords{}, err
	}

	x := origin.X + math.Floor(float64(tile.X)/float64(cf))*m.CellSize*float64(cf)*float64(m.TileWidth)

	y := origin.Y + float64(tile.Y+1)*m.CellSize*float64(m.TileHeight)
	if m.CornerOfOrigin == CornerTopLeft {
		y = origin.Y - float64(tile.Y)*m.CellSize*float64(m.TileHeight)
	}

	return Coords{X: x, Y: y}, nil
}

// LowerRightXY returns a tile's lower-right corner in the grid's CRS.
//
// Ported from morecantile.models.TileMatrixSet._lr.
func (t *TileMatrixSet) LowerRightXY(tile Tile) (Coords, error) {
	m, cf, origin, err := t.tileFrame(tile)
	if err != nil {
		return Coords{}, err
	}

	x := origin.X + (math.Floor(float64(tile.X)/float64(cf))+1)*m.CellSize*float64(cf)*float64(m.TileWidth)

	y := origin.Y + float64(tile.Y)*m.CellSize*float64(m.TileHeight)
	if m.CornerOfOrigin == CornerTopLeft {
		y = origin.Y - float64(tile.Y+1)*m.CellSize*float64(m.TileHeight)
	}

	return Coords{X: x, Y: y}, nil
}

// tileFrame resolves the matrix, coalesce factor and origin a tile's geometry
// is computed against — the common preamble of the corner and bounds methods.
func (t *TileMatrixSet) tileFrame(tile Tile) (TileMatrix, int64, Coords, error) {
	if err := validateTile(tile); err != nil {
		return TileMatrix{}, 0, Coords{}, err
	}

	m, err := t.Matrix(tile.Z)
	if err != nil {
		return TileMatrix{}, 0, Coords{}, err
	}

	cf, err := m.CoalesceFactor(tile.Y)
	if err != nil {
		return TileMatrix{}, 0, Coords{}, err
	}

	return m, cf, t.matrixOrigin(m), nil
}

// XYBounds returns a tile's bounding box in the grid's CRS.
//
// This is the extent tegola queries, clips and encodes against, and it needs no
// coordinate transform for any grid.
//
// Ported from morecantile.models.TileMatrixSet.xy_bounds.
// The box is composed from the two corners rather than recomputing the same
// arithmetic a third time. morecantile's xy_bounds does repeat it, but the
// duplication is not load-bearing: a tile's box is exactly its upper-left and
// lower-right corners, so composing them keeps the corner-of-origin rule in one
// place.
func (t *TileMatrixSet) XYBounds(tile Tile) (BoundingBox, error) {
	ul, err := t.UpperLeftXY(tile)
	if err != nil {
		return BoundingBox{}, err
	}

	lr, err := t.LowerRightXY(tile)
	if err != nil {
		return BoundingBox{}, err
	}

	return BoundingBox{Left: ul.X, Bottom: lr.Y, Right: lr.X, Top: ul.Y}, nil
}

// UpperLeft returns a tile's upper-left corner in geographic coordinates.
//
// Ported from morecantile.models.TileMatrixSet.ul.
func (t *TileMatrixSet) UpperLeft(tile Tile) (Coords, error) {
	c, err := t.UpperLeftXY(tile)
	if err != nil {
		return Coords{}, err
	}

	return t.LngLat(c.X, c.Y, false)
}

// LowerRight returns a tile's lower-right corner in geographic coordinates.
//
// Ported from morecantile.models.TileMatrixSet.lr.
func (t *TileMatrixSet) LowerRight(tile Tile) (Coords, error) {
	c, err := t.LowerRightXY(tile)
	if err != nil {
		return Coords{}, err
	}

	return t.LngLat(c.X, c.Y, false)
}

// Bounds returns a tile's bounding box in geographic coordinates.
//
// Ported from morecantile.models.TileMatrixSet.bounds.
func (t *TileMatrixSet) Bounds(tile Tile) (BoundingBox, error) {
	xy, err := t.XYBounds(tile)
	if err != nil {
		return BoundingBox{}, err
	}

	ul, err := t.LngLat(xy.Left, xy.Top, false)
	if err != nil {
		return BoundingBox{}, err
	}

	lr, err := t.LngLat(xy.Right, xy.Bottom, false)
	if err != nil {
		return BoundingBox{}, err
	}

	return BoundingBox{Left: ul.X, Bottom: lr.Y, Right: lr.X, Top: ul.Y}, nil
}

// XYBBox returns the whole grid's bounding box in its own CRS, derived from the
// corner tiles of its shallowest matrix.
//
// Ported from morecantile.models.TileMatrixSet.xy_bbox.
func (t *TileMatrixSet) XYBBox() (BoundingBox, error) {
	zoom := t.MinZoom()

	m, err := t.Matrix(zoom)
	if err != nil {
		return BoundingBox{}, err
	}

	ul, err := t.UpperLeftXY(Tile{X: 0, Y: 0, Z: zoom})
	if err != nil {
		return BoundingBox{}, err
	}

	lr, err := t.LowerRightXY(Tile{X: m.MatrixWidth - 1, Y: m.MatrixHeight - 1, Z: zoom})
	if err != nil {
		return BoundingBox{}, err
	}

	return BoundingBox{Left: ul.X, Bottom: lr.Y, Right: lr.X, Top: ul.Y}, nil
}

// BBox returns the whole grid's bounding box in geographic coordinates.
//
// Ported from morecantile.models.TileMatrixSet.bbox.
func (t *TileMatrixSet) BBox() (BoundingBox, error) {
	xy, err := t.XYBBox()
	if err != nil {
		return BoundingBox{}, err
	}

	return transformBoundsToGeographic(t.transformer, xy)
}

// LngLat converts a point in the grid's CRS to geographic coordinates,
// optionally clamping the result into the grid's geographic bounds.
//
// Ported from morecantile.models.TileMatrixSet.lnglat. morecantile additionally
// warns when the input lies outside the grid; callers wanting that check should
// use PointInXYBounds.
func (t *TileMatrixSet) LngLat(x, y float64, truncate bool) (Coords, error) {
	lng, lat, err := t.transformer.ToGeographic(x, y)
	if err != nil {
		return Coords{}, err
	}

	if truncate {
		bbox, err := t.BBox()
		if err != nil {
			return Coords{}, err
		}

		lng, lat = truncateCoordinates(lng, lat, bbox)
	}

	return Coords{X: lng, Y: lat}, nil
}

// XY converts a geographic point into the grid's CRS.
//
// Ported from morecantile.models.TileMatrixSet.xy.
func (t *TileMatrixSet) XY(lng, lat float64, truncate bool) (Coords, error) {
	if truncate {
		bbox, err := t.BBox()
		if err != nil {
			return Coords{}, err
		}

		lng, lat = truncateCoordinates(lng, lat, bbox)
	}

	x, y, err := t.transformer.FromGeographic(lng, lat)
	if err != nil {
		return Coords{}, err
	}

	return Coords{X: x, Y: y}, nil
}

// PointInXYBounds reports whether a point in the grid's CRS lies within the
// grid's bounds.
func (t *TileMatrixSet) PointInXYBounds(p Coords) (bool, error) {
	bbox, err := t.XYBBox()
	if err != nil {
		return false, err
	}

	return pointInBBox(p, bbox, defaultPointInBBoxPrecision), nil
}

// IntersectsXY reports whether a box in the grid's CRS overlaps the grid.
//
// Ported from morecantile.models.TileMatrixSet.intersect_tms.
func (t *TileMatrixSet) IntersectsXY(bbox BoundingBox) (bool, error) {
	tmsBounds, err := t.XYBBox()
	if err != nil {
		return false, err
	}

	return bbox.Left < tmsBounds.Right &&
		bbox.Right > tmsBounds.Left &&
		bbox.Top > tmsBounds.Bottom &&
		bbox.Bottom < tmsBounds.Top, nil
}

/* ------------------------------------------------------- tegola-facing view */

// TileExtent returns a tile's extent in the grid's CRS as a geom.Extent, the
// form tegola's provider and encode paths work with.
func (t *TileMatrixSet) TileExtent(tile Tile) (geom.Extent, error) {
	b, err := t.XYBounds(tile)
	if err != nil {
		return geom.Extent{}, err
	}

	return geom.Extent{b.Left, b.Bottom, b.Right, b.Top}, nil
}

// TileGeoExtent returns a tile's extent in geographic coordinates as a
// geom.Extent, for TileJSON bounds and tileset metadata.
func (t *TileMatrixSet) TileGeoExtent(tile Tile) (geom.Extent, error) {
	b, err := t.Bounds(tile)
	if err != nil {
		return geom.Extent{}, err
	}

	return geom.Extent{b.Left, b.Bottom, b.Right, b.Top}, nil
}

/* ------------------------------------------------------------ tile families */

// Tiles returns every tile of the given zoom levels overlapping a geographic
// bounding box, in row-major order per zoom.
//
// A box crossing the antimeridian (west greater than east) is split and both
// halves are covered.
//
// Ported from morecantile.models.TileMatrixSet.tiles.
func (t *TileMatrixSet) Tiles(west, south, east, north float64, zooms []int, truncate bool) ([]Tile, error) {
	for _, c := range []float64{west, south, east, north} {
		if math.IsNaN(c) {
			return nil, fmt.Errorf("tms: all coordinates must be finite")
		}
	}

	bbox, err := t.BBox()
	if err != nil {
		return nil, err
	}

	if truncate {
		west, south = truncateCoordinates(west, south, bbox)
		east, north = truncateCoordinates(east, north, bbox)
	}

	boxes := []BoundingBox{{Left: west, Bottom: south, Right: east, Top: north}}
	if west > east {
		// The box crosses the antimeridian, so cover each side separately.
		boxes = []BoundingBox{
			{Left: bbox.Left, Bottom: south, Right: east, Top: north},
			{Left: west, Bottom: south, Right: bbox.Right, Top: north},
		}
	}

	var tiles []Tile

	for _, b := range boxes {
		w, s, e, n := b.Left, b.Bottom, b.Right, b.Top

		// Clamp bounding values.
		esContain180th := lonsContainAntimeridian(e, bbox.Right)
		w = math.Max(bbox.Left, w)
		s = math.Max(bbox.Bottom, s)

		if esContain180th {
			e = math.Max(bbox.Right, e)
		} else {
			e = math.Min(bbox.Right, e)
		}

		n = math.Min(bbox.Top, n)

		wx, ny, err := t.transformer.FromGeographic(w+llEpsilon, n-llEpsilon)
		if err != nil {
			return nil, err
		}

		ex, sy, err := t.transformer.FromGeographic(e-llEpsilon, s+llEpsilon)
		if err != nil {
			return nil, err
		}

		for _, z := range zooms {
			nwTile, err := t.TileFromXY(wx, ny, z, true)
			if err != nil {
				return nil, err
			}

			seTile, err := t.TileFromXY(ex, sy, z, true)
			if err != nil {
				return nil, err
			}

			minx := min(nwTile.X, seTile.X)
			maxx := max(nwTile.X, seTile.X)
			miny := min(nwTile.Y, seTile.Y)
			maxy := max(nwTile.Y, seTile.Y)

			m, err := t.Matrix(z)
			if err != nil {
				return nil, err
			}

			for j := miny; j <= maxy; j++ {
				cf, err := m.CoalesceFactor(j)
				if err != nil {
					return nil, err
				}

				for i := minx; i <= maxx; i++ {
					if cf != 1 && i%cf != 0 {
						continue
					}

					tiles = append(tiles, Tile{X: i, Y: j, Z: z})
				}
			}
		}
	}

	return tiles, nil
}

// Neighbors returns the up-to-eight tiles adjoining a tile, omitting any that
// fall outside the matrix. Ordering is not guaranteed to be meaningful; tiles
// are returned sorted by z, y, x.
//
// Ported from morecantile.models.TileMatrixSet.neighbors.
func (t *TileMatrixSet) Neighbors(tile Tile) ([]Tile, error) {
	m, err := t.Matrix(tile.Z)
	if err != nil {
		return nil, err
	}

	x, y := tile.X, tile.Y

	miny := max(int64(0), y-1)
	maxy := min(y+1, m.MatrixHeight-1)

	cf, err := m.CoalesceFactor(y)
	if err != nil {
		return nil, err
	}

	var minx, maxx int64
	if cf != 1 {
		if x%cf != 0 {
			x -= x % cf
		}

		minx = max(int64(0), x-(x%cf)-1)
		maxx = min(x+(x%cf)+cf, m.MatrixWidth-1)
	} else {
		minx = max(int64(0), x-1)
		maxx = min(x+1, m.MatrixWidth-1)
	}

	seen := make(map[Tile]struct{})

	for ytile := miny; ytile <= maxy; ytile++ {
		rowCF, err := m.CoalesceFactor(ytile)
		if err != nil {
			return nil, err
		}

		for xtile := minx; xtile <= maxx; xtile++ {
			nx := xtile
			if rowCF != 1 && nx%rowCF != 0 {
				nx -= nx % rowCF
			}

			if nx == x && ytile == y {
				continue
			}

			seen[Tile{X: nx, Y: ytile, Z: tile.Z}] = struct{}{}
		}
	}

	return sortedTiles(seen), nil
}

// Parent returns the tile or tiles at a shallower zoom that contain a tile.
//
// zoom selects the target level; pass -1 for the immediately shallower one. A
// tile at MinZoom has no parent and yields an empty slice. More than one tile
// can be returned when the matrices' aspect ratios differ between levels.
//
// Ported from morecantile.models.TileMatrixSet.parent.
func (t *TileMatrixSet) Parent(tile Tile, zoom int) ([]Tile, error) {
	if err := validateTile(tile); err != nil {
		return nil, err
	}

	if tile.Z == t.MinZoom() {
		return nil, nil
	}

	if zoom >= 0 && tile.Z <= zoom {
		return nil, InvalidZoomError{Message: "zoom must be less than that of the input tile"}
	}

	target := tile.Z - 1
	if zoom >= 0 {
		target = zoom
	}

	return t.relatives(tile, target)
}

// Children returns the tiles at a deeper zoom covered by a tile.
//
// zoom selects the target level; pass -1 for the immediately deeper one.
//
// Ported from morecantile.models.TileMatrixSet.children.
func (t *TileMatrixSet) Children(tile Tile, zoom int) ([]Tile, error) {
	if err := validateTile(tile); err != nil {
		return nil, err
	}

	if zoom >= 0 && tile.Z > zoom {
		return nil, InvalidZoomError{Message: "zoom must be greater than that of the input tile"}
	}

	target := tile.Z + 1
	if zoom >= 0 {
		target = zoom
	}

	return t.relatives(tile, target)
}

// relatives returns the tiles at targetZoom covering the given tile. It is the
// shared body of Parent and Children: both inset the tile's box by a tenth of a
// cell so that touching neighbours are not picked up, then enumerate the tiles
// spanned at the target level.
func (t *TileMatrixSet) relatives(tile Tile, targetZoom int) ([]Tile, error) {
	m, err := t.Matrix(tile.Z)
	if err != nil {
		return nil, err
	}

	inset := m.CellSize / 10.0

	bbox, err := t.XYBounds(tile)
	if err != nil {
		return nil, err
	}

	ul, err := t.TileFromXY(bbox.Left+inset, bbox.Top-inset, targetZoom, true)
	if err != nil {
		return nil, err
	}

	lr, err := t.TileFromXY(bbox.Right-inset, bbox.Bottom+inset, targetZoom, true)
	if err != nil {
		return nil, err
	}

	target, err := t.Matrix(targetZoom)
	if err != nil {
		return nil, err
	}

	var tiles []Tile

	for j := ul.Y; j <= lr.Y; j++ {
		cf, err := target.CoalesceFactor(j)
		if err != nil {
			return nil, err
		}

		for i := ul.X; i <= lr.X; i++ {
			if cf != 1 && i%cf != 0 {
				continue
			}

			tiles = append(tiles, Tile{X: i, Y: j, Z: targetZoom})
		}
	}

	return tiles, nil
}

// sortedTiles returns the set's tiles ordered by z, then y, then x — the
// ordering Python's sorted() gives a set of (x, y, z) named tuples, adjusted for
// this port's field order.
func sortedTiles(set map[Tile]struct{}) []Tile {
	out := make([]Tile, 0, len(set))
	for tile := range set {
		out = append(out, tile)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}

		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}

		return out[i].Z < out[j].Z
	})

	return out
}

/* ------------------------------------------------------------------ quadkeys */

// Quadkey returns a tile's quadkey.
//
// Ported from morecantile.models.TileMatrixSet.quadkey.
func (t *TileMatrixSet) Quadkey(tile Tile) (string, error) {
	if !t.quadtree {
		return "", NoQuadkeySupportError{Identifier: t.def.ID}
	}

	if err := validateTile(tile); err != nil {
		return "", err
	}

	qk := make([]byte, 0, tile.Z)

	for z := tile.Z; z > t.MinZoom(); z-- {
		digit := 0
		mask := int64(1) << (z - 1)

		if tile.X&mask != 0 {
			digit++
		}

		if tile.Y&mask != 0 {
			digit += 2
		}

		qk = append(qk, byte('0'+digit))
	}

	return string(qk), nil
}

// QuadkeyToTile returns the tile a quadkey identifies.
//
// Ported from morecantile.models.TileMatrixSet.quadkey_to_tile.
func (t *TileMatrixSet) QuadkeyToTile(qk string) (Tile, error) {
	if !t.quadtree {
		return Tile{}, NoQuadkeySupportError{Identifier: t.def.ID}
	}

	if qk == "" {
		return Tile{X: 0, Y: 0, Z: 0}, nil
	}

	var xtile, ytile int64

	for i := 0; i < len(qk); i++ {
		digit := qk[len(qk)-1-i]
		mask := int64(1) << i

		switch digit {
		case '0':
		case '1':
			xtile |= mask
		case '2':
			ytile |= mask
		case '3':
			xtile |= mask
			ytile |= mask
		default:
			return Tile{}, QuadKeyError{
				Message: fmt.Sprintf("unexpected quadkey digit %q", string(digit)),
			}
		}
	}

	return Tile{X: xtile, Y: ytile, Z: len(qk)}, nil
}

/* ------------------------------------------------------------- resolutions */

// ZoomForRes returns the zoom level whose cell size best matches a resolution
// expressed in the grid's units.
//
// minZoom and maxZoom bound the search; pass -1 for the grid's own limits.
// strategy is one of ZoomStrategyAuto, ZoomStrategyLower or ZoomStrategyUpper.
//
// Ported from morecantile.models.TileMatrixSet.zoom_for_res, itself adapted from
// GDAL's COG driver.
func (t *TileMatrixSet) ZoomForRes(res float64, minZoom, maxZoom int, strategy string) (int, error) {
	if maxZoom < 0 {
		maxZoom = t.MaxZoom()
	}

	if minZoom < 0 {
		minZoom = t.MinZoom()
	}

	if minZoom > maxZoom {
		return 0, InvalidZoomError{Message: fmt.Sprintf(
			"minimum zoom (%d) is above maximum zoom (%d)", minZoom, maxZoom)}
	}

	zoomLevel := minZoom
	var matrixRes float64

	for zoomLevel = minZoom; zoomLevel <= maxZoom; zoomLevel++ {
		m, err := t.Matrix(zoomLevel)
		if err != nil {
			return 0, err
		}

		matrixRes = m.CellSize
		if res > matrixRes || math.Abs(res-matrixRes)/matrixRes <= 1e-8 {
			break
		}
	}

	if zoomLevel > maxZoom {
		zoomLevel = maxZoom
	}

	if zoomLevel > 0 && math.Abs(res-matrixRes)/matrixRes > 1e-8 {
		switch strategy {
		case ZoomStrategyLower:
			zoomLevel = max(zoomLevel-1, minZoom)
		case ZoomStrategyUpper:
			zoomLevel = min(zoomLevel, maxZoom)
		case ZoomStrategyAuto, "":
			prev, err := t.Matrix(max(zoomLevel-1, minZoom))
			if err != nil {
				return 0, err
			}

			if (prev.CellSize / res) < (res / matrixRes) {
				zoomLevel = max(zoomLevel-1, minZoom)
			}
		default:
			return 0, fmt.Errorf(
				"tms: invalid zoom level strategy %q, want one of %q, %q or %q",
				strategy, ZoomStrategyLower, ZoomStrategyUpper, ZoomStrategyAuto)
		}
	}

	return zoomLevel, nil
}

// CellSize returns the cell size — the resolution in the grid's units — at a
// zoom level.
func (t *TileMatrixSet) CellSize(zoom int) (float64, error) {
	m, err := t.Matrix(zoom)
	if err != nil {
		return 0, err
	}

	return m.CellSize, nil
}

// ScaleDenominator returns the scale denominator at a zoom level.
func (t *TileMatrixSet) ScaleDenominator(zoom int) (float64, error) {
	m, err := t.Matrix(zoom)
	if err != nil {
		return 0, err
	}

	return m.ScaleDenominator, nil
}

/* ------------------------------------------------------------------ GeoJSON */

// FeatureOptions tunes Feature's output. The zero value produces the geographic
// GeoJSON feature morecantile returns by default.
type FeatureOptions struct {
	// FID overrides the feature id, which defaults to the tile's string form.
	FID string
	// Props are merged into the feature's properties.
	Props map[string]any
	// Buffer expands the feature's box by this many units on every side.
	Buffer float64
	// Precision, when positive, rounds coordinates to this many decimals.
	Precision int
	// Projected emits coordinates in the grid's CRS instead of geographic ones.
	Projected bool
}

// Feature returns the GeoJSON feature for a tile.
//
// Ported from morecantile.models.TileMatrixSet.feature.
func (t *TileMatrixSet) Feature(tile Tile, opts FeatureOptions) (map[string]any, error) {
	bounds, err := t.XYBounds(tile)
	if err != nil {
		return nil, err
	}

	west, south, east, north := bounds.Left, bounds.Bottom, bounds.Right, bounds.Top

	if !opts.Projected {
		geographic, err := transformBoundsToGeographic(t.transformer, bounds)
		if err != nil {
			return nil, err
		}

		west, south, east, north = geographic.Left, geographic.Bottom, geographic.Right, geographic.Top
	}

	if opts.Buffer != 0 {
		west -= opts.Buffer
		south -= opts.Buffer
		east += opts.Buffer
		north += opts.Buffer
	}

	if opts.Precision > 0 {
		west = roundFloat(west, opts.Precision)
		south = roundFloat(south, opts.Precision)
		east = roundFloat(east, opts.Precision)
		north = roundFloat(north, opts.Precision)
	}

	xyz := tile.String()

	feat := map[string]any{
		"type": "Feature",
		"bbox": []float64{
			math.Min(west, east), math.Min(south, north),
			math.Max(west, east), math.Max(south, north),
		},
		"id":       xyz,
		"geometry": bboxToFeature(west, south, east, north),
		"properties": map[string]any{
			"title":   "XYZ tile " + xyz,
			"tms":     t.def.ID,
			"tms_crs": t.crs.uri,
		},
	}

	if opts.Projected {
		feat["crs"] = map[string]any{
			"type":       "name",
			"properties": map[string]any{"name": t.crs.uri},
		}
	}

	if len(opts.Props) > 0 {
		props := feat["properties"].(map[string]any)
		for k, v := range opts.Props {
			props[k] = v
		}
	}

	if opts.FID != "" {
		feat["id"] = opts.FID
	}

	return feat, nil
}
