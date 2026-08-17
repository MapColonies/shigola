package tms

// Ported from morecantile/utils.py (MIT, Development Seed). The pyproj-dependent
// helpers (meters_per_unit, to_rasterio_crs) live in crs.go instead, since this
// port resolves CRS metadata from a table rather than from PROJ.

import "math"

// lonsContainAntimeridian reports whether the 180th meridian lies between two
// longitudes.
//
// Ported from morecantile.utils.lons_contain_antimeridian. Note that the
// original sorts a local list but discards the result, so the comparison runs
// against the arguments in the order they were passed; that behaviour is
// preserved here deliberately, because Tiles depends on it.
func lonsContainAntimeridian(lon1, lon2 float64) bool {
	lon1Clipped := math.Max(-180.0, math.Min(lon1, 180))
	lon2Clipped := math.Max(-180.0, math.Min(lon2, 180))

	lon1Converted := math.Mod(lon1Clipped+360, 360)
	lon2Converted := math.Mod(lon2Clipped+360, 360)

	return lon1Converted < 180 && 180 < lon2Converted
}

// bboxToFeature builds the GeoJSON Polygon geometry for a bounding box.
//
// Ported from morecantile.utils.bbox_to_feature. The ring is closed and wound
// in the same order as the original: SW, NW, NE, SE, SW.
func bboxToFeature(west, south, east, north float64) map[string]any {
	return map[string]any{
		"type": "Polygon",
		"coordinates": [][][]float64{{
			{west, south},
			{west, north},
			{east, north},
			{east, south},
			{west, south},
		}},
	}
}

// defaultPointInBBoxPrecision is the number of decimal places pointInBBox
// rounds to before comparing, matching morecantile's default.
const defaultPointInBBoxPrecision = 5

// pointInBBox reports whether a point lies within a bounding box, comparing
// coordinates rounded to precision decimal places.
//
// Ported from morecantile.utils.point_in_bbox.
func pointInBBox(point Coords, bbox BoundingBox, precision int) bool {
	return roundFloat(point.X, precision) >= roundFloat(bbox.Left, precision) &&
		roundFloat(point.X, precision) <= roundFloat(bbox.Right, precision) &&
		roundFloat(point.Y, precision) >= roundFloat(bbox.Bottom, precision) &&
		roundFloat(point.Y, precision) <= roundFloat(bbox.Top, precision)
}

// roundFloat rounds to a number of decimal places, half away from zero, as
// Go's math.Round does. Python's round() is half-to-even, but the difference
// only shows for values exactly on a half at the rounding digit, which cannot
// arise from the comparisons pointInBBox makes.
func roundFloat(v float64, precision int) float64 {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return v
	}

	factor := math.Pow(10, float64(precision))

	return math.Round(v*factor) / factor
}

// truncateCoordinates clamps a longitude and latitude into a bounding box.
//
// Ported from morecantile.utils.truncate_coordinates.
func truncateCoordinates(lng, lat float64, bbox BoundingBox) (float64, float64) {
	if lng > bbox.Right {
		lng = bbox.Right
	} else if lng < bbox.Left {
		lng = bbox.Left
	}

	if lat > bbox.Top {
		lat = bbox.Top
	} else if lat < bbox.Bottom {
		lat = bbox.Bottom
	}

	return lng, lat
}

// isPowerOfTwo reports whether a number is a power of two.
//
// Ported from morecantile.utils.is_power_of_two.
func isPowerOfTwo(number int64) bool {
	return number&(number-1) == 0 && number != 0
}

// checkQuadkeySupport reports whether a list of tile matrices forms a 2x2
// quadtree: every matrix square, a power of two wide, and each one twice the
// width of the one before it.
//
// Ported from morecantile.utils.check_quadkey_support.
func checkQuadkeySupport(matrices []TileMatrix) bool {
	for i := 0; i < len(matrices)-1; i++ {
		m := matrices[i]
		if m.MatrixWidth != m.MatrixHeight {
			return false
		}

		if !isPowerOfTwo(m.MatrixWidth) {
			return false
		}

		if m.MatrixWidth*2 != matrices[i+1].MatrixWidth {
			return false
		}
	}

	return true
}
