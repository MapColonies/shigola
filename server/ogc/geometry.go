package ogc

import (
	"github.com/go-spatial/geom"

	"github.com/go-spatial/tegola/mapbox/tilejson"
)

// geometryClass is how one layer's geometry is named in each of the two
// vocabularies this service emits.
type geometryClass struct {
	// dimension is OGC's topological dimension of the geometry.
	dimension int
	// tileJSON is TileJSON's name for the same thing.
	tileJSON tilejson.GeomType
}

// classifyGeometry maps a layer's geometry onto both vocabularies at once.
//
// One switch rather than one per vocabulary: the tileset metadata and the
// TileJSON view describe the same layer, so a geometry added to one and
// forgotten in the other would make the two representations disagree about what
// the layer holds.
//
// The boolean reports whether the geometry is one this service can classify at
// all; a layer whose type is only known once its data is read is not.
func classifyGeometry(g geom.Geometry) (geometryClass, bool) {
	switch g.(type) {
	case geom.Point, geom.MultiPoint:
		return geometryClass{dimension: dimensionPoints, tileJSON: tilejson.GeomTypePoint}, true
	case geom.Line, geom.LineString, geom.MultiLineString:
		return geometryClass{dimension: dimensionCurves, tileJSON: tilejson.GeomTypeLine}, true
	case geom.Polygon, geom.MultiPolygon:
		return geometryClass{dimension: dimensionSurfaces, tileJSON: tilejson.GeomTypePolygon}, true
	default:
		return geometryClass{}, false
	}
}
