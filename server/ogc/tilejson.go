package ogc

import (
	"net/http"

	"github.com/go-spatial/geom"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/mapbox/tilejson"
	"github.com/MapColonies/shigola/tms"
)

// tileJSONVersion is the TileJSON specification version this service emits.
//
// 3.0.0 is the first that lets a tileset name a CRS other than WebMercator,
// which is exactly what a WorldCRS84Quad tileset needs (ADR-0006).
const tileJSONVersion = "3.0.0"

// TileJSON is the TileJSON 3.0 view of a tileset.
//
// It exists alongside the canonical TileSetMetadata because the map clients in
// widest use read TileJSON and not OGC tileset metadata; serving both from one
// resource means a client can use whichever it understands (ADR-0006).
type TileJSON struct {
	tilejson.TileJSON
	// CRS names the tileset's coordinate reference system. TileJSON before 3.0
	// had no such field and assumed WebMercator, which would misdescribe every
	// tileset in another scheme.
	CRS string `json:"crs,omitempty"`
	// TileMatrixSetID ties the document back to the OGC tiling scheme it
	// describes, which plain TileJSON cannot express.
	TileMatrixSetID string `json:"tileMatrixSetId,omitempty"`
}

// tileJSON builds the TileJSON representation of a tileset.
func (s *Service) tileJSON(r *http.Request, c Collection, grid *tms.TileMatrixSet) TileJSON {
	base := []string{"collections", c.ID, "tiles", grid.ID()}

	minZoom, maxZoom := zoomRange(c.Map)

	doc := TileJSON{
		CRS:             grid.CRSURI(),
		TileMatrixSetID: grid.ID(),
	}

	doc.TileJSON.TileJSON = tileJSONVersion
	doc.Name = strPtr(c.Title())
	doc.Format = "pbf"
	doc.Scheme = tilejson.SchemeXYZ
	doc.Version = "1.0.0"
	doc.MinZoom = minZoom
	doc.MaxZoom = maxZoom
	doc.Bounds = boundsOrWorld(c.Map.Bounds)
	doc.Center = c.Map.Center
	doc.Grids = []string{}
	doc.Data = []string{}

	if c.Map.Attribution != "" {
		doc.Attribution = strPtr(c.Map.Attribution)
	}

	// The tile template is the OGC path — z/y/x — with TileJSON's own variable
	// names. A client substituting {z}/{y}/{x} therefore hits the same tiles the
	// item link describes, rather than a transposed pair.
	doc.Tiles = []string{
		s.hrefTemplate(r, base, "{z}", "{y}", "{x}") + "?f=mvt",
	}

	doc.VectorLayers = make([]tilejson.VectorLayer, 0, len(c.Map.Layers))
	for i := range c.Map.Layers {
		name := c.Map.Layers[i].MVTName()
		doc.VectorLayers = append(doc.VectorLayers, tilejson.VectorLayer{
			Version:      2,
			Extent:       int(c.Map.TileExtent),
			ID:           name,
			Name:         name,
			GeometryType: geomType(c.Map.Layers[i].GeomType),
			MinZoom:      c.Map.Layers[i].MinZoom,
			MaxZoom:      c.Map.Layers[i].MaxZoom,
		})
	}

	return doc
}

// zoomRange is the span the tileset's layers cover between them.
func zoomRange(m atlas.Map) (minZoom, maxZoom uint) {
	if len(m.Layers) == 0 {
		return 0, 0
	}

	minZoom, maxZoom = m.Layers[0].MinZoom, m.Layers[0].MaxZoom
	for i := range m.Layers {
		minZoom = min(minZoom, m.Layers[i].MinZoom)
		maxZoom = max(maxZoom, m.Layers[i].MaxZoom)
	}

	return minZoom, maxZoom
}

// boundsOrWorld returns a map's bounds, or TileJSON's whole-world default.
func boundsOrWorld(bounds *geom.Extent) [4]float64 {
	if bounds == nil {
		return [4]float64{-180, -90, 180, 90}
	}

	return [4]float64{bounds.MinX(), bounds.MinY(), bounds.MaxX(), bounds.MaxY()}
}

// geomType maps a layer's geometry to TileJSON's vocabulary.
func geomType(g geom.Geometry) tilejson.GeomType {
	class, ok := classifyGeometry(g)
	if !ok {
		return tilejson.GeomTypeUnknown
	}

	return class.tileJSON
}

func strPtr(s string) *string { return &s }
