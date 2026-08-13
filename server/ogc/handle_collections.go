package ogc

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/dimfeld/httptreemux"
	"github.com/go-spatial/geom"

	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/tms"
)

// HandleCollections serves the list of collections.
func (s *Service) HandleCollections(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	collections := s.collections()
	descs := make([]CollectionDesc, 0, len(collections))
	for _, c := range collections {
		descs = append(descs, s.collectionDesc(r, c))
	}

	writeJSON(w, format, Collections{
		Links: []Link{
			{Rel: relSelf, Href: s.href(r, "collections"), Type: MediaTypeJSON, Title: "this document"},
		},
		Collections: descs,
	})
}

// HandleCollection serves one collection.
func (s *Service) HandleCollection(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	c, err := s.collection(httptreemux.ContextParams(r.Context())["collection_id"])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, format, s.collectionDesc(r, c))
}

// collectionDesc builds a collection's description document.
func (s *Service) collectionDesc(r *http.Request, c Collection) CollectionDesc {
	return CollectionDesc{
		ID:       c.ID,
		Title:    c.Title(),
		Extent:   spatialExtent(c.Map.Bounds),
		DataType: dataTypeVector,
		CRS:      []string{crs84URI},
		Links: []Link{
			{Rel: relSelf, Href: s.href(r, "collections", c.ID), Type: MediaTypeJSON, Title: c.Title()},
			{
				Rel:   relTilesetsVector,
				Href:  s.href(r, "collections", c.ID, "tiles"),
				Type:  MediaTypeJSON,
				Title: "vector tilesets of this collection",
			},
		},
	}
}

// spatialExtent converts a map's WGS84 bounds into an OGC extent.
func spatialExtent(bounds *geom.Extent) *Extent {
	if bounds == nil {
		return nil
	}

	return &Extent{
		Spatial: &SpatialExtent{
			BBox: [][]float64{{bounds.MinX(), bounds.MinY(), bounds.MaxX(), bounds.MaxY()}},
			CRS:  crs84URI,
		},
	}
}

// HandleTileSets serves a collection's tilesets: one per tiling scheme the
// collection's map allows.
func (s *Service) HandleTileSets(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	c, err := s.collection(httptreemux.ContextParams(r.Context())["collection_id"])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	grids := c.Map.TileGrids()
	items := make([]TileSetItem, 0, len(grids))

	for _, grid := range grids {
		items = append(items, TileSetItem{
			Title:            c.Title(),
			DataType:         dataTypeVector,
			CRS:              grid.CRSURI(),
			TileMatrixSetID:  grid.ID(),
			TileMatrixSetURI: grid.URI(),
			Links: []Link{
				{
					Rel:   relSelf,
					Href:  s.href(r, "collections", c.ID, "tiles", grid.ID()),
					Type:  MediaTypeJSON,
					Title: grid.ID(),
				},
			},
		})
	}

	writeJSON(w, format, TileSets{
		Tilesets: items,
		Links: []Link{
			{Rel: relSelf, Href: s.href(r, "collections", c.ID, "tiles"), Type: MediaTypeJSON, Title: "this document"},
		},
	})
}

// HandleTileSet serves one tileset's metadata: a collection in one scheme.
//
// The canonical representation is OGC tileset metadata; ?f=tilejson serves the
// same tileset as TileJSON 3.0, which is what the map clients in widest use
// read (ADR-0006).
func (s *Service) HandleTileSet(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON, FormatTileJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	params := httptreemux.ContextParams(r.Context())

	c, err := s.collection(params["collection_id"])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	grid, err := s.collectionGrid(c, params["tile_matrix_set_id"])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	if format == FormatTileJSON {
		writeJSON(w, format, s.tileJSON(r, c, grid))
		return
	}

	writeJSON(w, format, s.tileSetMetadata(r, c, grid))
}

// collectionGrid resolves the tiling scheme a request names, for a collection.
//
// A scheme the server serves but this collection's map does not offer is a 404
// like an unknown one: from the client's side both mean "no such tileset".
func (s *Service) collectionGrid(c Collection, id string) (*tms.TileMatrixSet, error) {
	grid, err := tms.Get(id)
	if err != nil {
		return nil, err
	}

	if !c.Map.SupportsTileGrid(grid.ID()) {
		return nil, errors.New("collection " + c.ID + " is not served in tile matrix set " + id)
	}

	return grid, nil
}

// tileSetMetadata builds a tileset's metadata document.
func (s *Service) tileSetMetadata(r *http.Request, c Collection, grid *tms.TileMatrixSet) TileSetMetadata {
	base := []string{"collections", c.ID, "tiles", grid.ID()}

	md := TileSetMetadata{
		Title:            c.Title(),
		DataType:         dataTypeVector,
		CRS:              grid.CRSURI(),
		TileMatrixSetID:  grid.ID(),
		TileMatrixSetURI: grid.URI(),
		TileMatrixLimits: tileMatrixLimits(c.Map, grid),
		Attribution:      c.Map.Attribution,
		Layers:           geoDataLayers(c.Map),
		Links: []Link{
			{Rel: relSelf, Href: s.href(r, base...), Type: MediaTypeJSON, Title: c.Title()},
			{
				Rel:   relTilingScheme,
				Href:  s.href(r, "tileMatrixSets", grid.ID()),
				Type:  MediaTypeJSON,
				Title: grid.ID(),
			},
			{
				// The tile URL template. OGC orders a tile path
				// {tileMatrix}/{tileRow}/{tileCol} — z/y/x, transposed from
				// tegola's native z/x/y.
				Rel:       relItem,
				Href:      s.hrefTemplate(r, base, "{tileMatrix}", "{tileRow}", "{tileCol}") + "?f=mvt",
				Type:      MediaTypeMVT,
				Title:     "tiles of this tileset",
				Templated: true,
			},
		},
	}

	if bounds := c.Map.Bounds; bounds != nil {
		md.BoundingBox = &BoundingBox{
			LowerLeft:  []float64{bounds.MinX(), bounds.MinY()},
			UpperRight: []float64{bounds.MaxX(), bounds.MaxY()},
			CRS:        crs84URI,
		}
	}

	return md
}

// geoDataLayers describes the vector layers a tileset's tiles carry.
func geoDataLayers(m atlas.Map) []GeoDataLayer {
	layers := make([]GeoDataLayer, 0, len(m.Layers))

	for i := range m.Layers {
		layers = append(layers, GeoDataLayer{
			ID:            m.Layers[i].MVTName(),
			DataType:      dataTypeVector,
			GeometryType:  geometryDimension(m.Layers[i].GeomType),
			MinTileMatrix: strconv.FormatUint(uint64(m.Layers[i].MinZoom), 10),
			MaxTileMatrix: strconv.FormatUint(uint64(m.Layers[i].MaxZoom), 10),
		})
	}

	return layers
}

// geometryDimension names a layer's geometry dimension as OGC does: points,
// curves, surfaces. An empty string means the layer's type is not known ahead of
// time, in which case the field is omitted rather than guessed.
func geometryDimension(g geom.Geometry) string {
	switch g.(type) {
	case geom.Point, geom.MultiPoint:
		return "points"
	case geom.Line, geom.LineString, geom.MultiLineString:
		return "curves"
	case geom.Polygon, geom.MultiPolygon:
		return "surfaces"
	default:
		return ""
	}
}

// tileMatrixLimits computes, for each zoom the collection's layers cover, the
// range of tiles its extent actually touches.
//
// This is what stops a client requesting the whole pyramid for a collection
// covering one city. Zooms come from the layers' own min/max, and the row and
// column ranges from the map's WGS84 bounds mapped into the grid.
func tileMatrixLimits(m atlas.Map, grid *tms.TileMatrixSet) []TileMatrixLimits {
	if m.Bounds == nil || len(m.Layers) == 0 {
		return nil
	}

	minZoom, maxZoom := m.Layers[0].MinZoom, m.Layers[0].MaxZoom
	for i := range m.Layers {
		minZoom = min(minZoom, m.Layers[i].MinZoom)
		maxZoom = max(maxZoom, m.Layers[i].MaxZoom)
	}

	limits := make([]TileMatrixLimits, 0, maxZoom-minZoom+1)

	for z := int(minZoom); z <= int(maxZoom); z++ {
		cols, rows, err := grid.MatrixSize(z)
		if err != nil {
			// Beyond the scheme's own zooms; the rest are too.
			break
		}

		// Corners rather than a scan: the extent is a box in geographic space,
		// and both grids in play here are axis-aligned in it.
		upperLeft, err := grid.TileFromLngLat(m.Bounds.MinX(), m.Bounds.MaxY(), z, true, true)
		if err != nil {
			continue
		}

		lowerRight, err := grid.TileFromLngLat(m.Bounds.MaxX(), m.Bounds.MinY(), z, true, true)
		if err != nil {
			continue
		}

		limits = append(limits, TileMatrixLimits{
			TileMatrix: strconv.Itoa(z),
			MinTileCol: clamp(min(upperLeft.X, lowerRight.X), 0, cols-1),
			MaxTileCol: clamp(max(upperLeft.X, lowerRight.X), 0, cols-1),
			MinTileRow: clamp(min(upperLeft.Y, lowerRight.Y), 0, rows-1),
			MaxTileRow: clamp(max(upperLeft.Y, lowerRight.Y), 0, rows-1),
		})
	}

	return limits
}

func clamp(v, lo, hi int64) int64 {
	return min(max(v, lo), hi)
}
