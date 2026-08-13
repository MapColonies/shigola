package ogc_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/dimfeld/httptreemux"
	"github.com/go-spatial/geom"
	vectorTile "github.com/go-spatial/geom/encoding/mvt/vector_tile"
	"github.com/golang/protobuf/proto"

	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/provider"
	"github.com/go-spatial/tegola/provider/test"
	"github.com/go-spatial/tegola/server/ogc"
	"github.com/go-spatial/tegola/tms"
)

// newAtlas builds an atlas holding one two-layer map, optionally offering more
// than one tiling scheme.
func newAtlas(t *testing.T, gridIDs ...string) *atlas.Atlas {
	t.Helper()

	m := atlas.NewWebMercatorMap("osm")
	m.Bounds = &geom.Extent{-20, -10, 20, 10}
	m.Attribution = "test attribution"
	m.Layers = []atlas.Layer{
		{
			Name:              "water",
			ProviderLayerName: "water",
			MinZoom:           0,
			MaxZoom:           5,
			Provider:          &test.TileProvider{},
			GeomType:          geom.Polygon{},
		},
		{
			Name:              "roads",
			ProviderLayerName: "roads",
			MinZoom:           2,
			MaxZoom:           8,
			Provider:          &test.TileProvider{},
			GeomType:          geom.LineString{},
		},
	}

	if len(gridIDs) > 0 {
		grids := make([]*tms.TileMatrixSet, 0, len(gridIDs))
		for _, id := range gridIDs {
			grid, err := tms.Get(id)
			if err != nil {
				t.Fatalf("tms.Get(%q): %v", id, err)
			}
			grids = append(grids, grid)
		}
		m.TileMatrixSets = grids
	}

	a := &atlas.Atlas{}
	a.AddMap(m)

	return a
}

// newRouterFor mounts the surface over a given atlas.
func newRouterFor(t *testing.T, a *atlas.Atlas) *httptreemux.TreeMux {
	t.Helper()

	svc := ogc.New(ogc.Config{
		Atlas:     a,
		URLRoot:   func(*http.Request) *url.URL { return &url.URL{Scheme: "http", Host: "tegola.io"} },
		URIPrefix: "/",
	})

	r := httptreemux.New()
	group := r.NewGroup("/")
	for _, route := range svc.Routes() {
		group.UsingContext().Handler(route.Method, route.Path, route.Handler)
	}

	return r
}

// TestCollections covers the two-tier model of ADR-0002: a map is a collection,
// and so is each of its layers.
func TestCollections(t *testing.T) {
	var doc ogc.Collections
	w := get(t, newRouterFor(t, newAtlas(t)), "/collections", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	ids := make([]string, 0, len(doc.Collections))
	for _, c := range doc.Collections {
		ids = append(ids, c.ID)
	}

	// The map-collection is always present, even though every layer also has
	// one, so that a map name is always a usable collection id.
	for _, want := range []string{"osm", "osm:water", "osm:roads"} {
		if !slices.Contains(ids, want) {
			t.Errorf("collections is missing %q, got %v", want, ids)
		}
	}

	for _, c := range doc.Collections {
		if c.DataType != "vector" {
			t.Errorf("%v dataType = %q, want vector", c.ID, c.DataType)
		}

		var hasTilesets bool
		for _, link := range c.Links {
			if link.Rel == "http://www.opengis.net/def/rel/ogc/1.0/tilesets-vector" {
				hasTilesets = true

				if want := "http://tegola.io/collections/" + c.ID + "/tiles"; link.Href != want {
					t.Errorf("%v tilesets link = %q, want %q", c.ID, link.Href, want)
				}
			}
		}

		if !hasTilesets {
			t.Errorf("%v has no tilesets-vector link", c.ID)
		}
	}
}

func TestCollection(t *testing.T) {
	r := newRouterFor(t, newAtlas(t))

	t.Run("a map-collection", func(t *testing.T) {
		var doc ogc.CollectionDesc
		if w := get(t, r, "/collections/osm", &doc); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		if doc.ID != "osm" {
			t.Errorf("id = %q, want osm", doc.ID)
		}

		if doc.Extent == nil || doc.Extent.Spatial == nil || len(doc.Extent.Spatial.BBox) != 1 {
			t.Fatalf("extent = %+v, want one bbox", doc.Extent)
		}

		if got, want := doc.Extent.Spatial.BBox[0], []float64{-20, -10, 20, 10}; !slices.Equal(got, want) {
			t.Errorf("bbox = %v, want %v", got, want)
		}
	})

	t.Run("a layer-collection", func(t *testing.T) {
		var doc ogc.CollectionDesc
		if w := get(t, r, "/collections/osm:water", &doc); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		if doc.ID != "osm:water" {
			t.Errorf("id = %q, want osm:water", doc.ID)
		}
	})

	t.Run("an unknown map", func(t *testing.T) {
		if w := get(t, r, "/collections/nope", nil); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("a known map with an unknown layer", func(t *testing.T) {
		if w := get(t, r, "/collections/osm:nope", nil); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

// TestTileSets covers a collection offering more than one tiling scheme.
func TestTileSets(t *testing.T) {
	a := newAtlas(t, tms.WebMercatorQuad, tms.WorldCRS84Quad)

	var doc ogc.TileSets
	w := get(t, newRouterFor(t, a), "/collections/osm/tiles", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if len(doc.Tilesets) != 2 {
		t.Fatalf("tilesets = %d, want 2", len(doc.Tilesets))
	}

	for _, ts := range doc.Tilesets {
		if ts.DataType != "vector" {
			t.Errorf("%v dataType = %q, want vector", ts.TileMatrixSetID, ts.DataType)
		}

		if ts.TileMatrixSetURI == "" {
			t.Errorf("%v has no tileMatrixSetURI", ts.TileMatrixSetID)
		}
	}
}

func TestTileSetMetadata(t *testing.T) {
	a := newAtlas(t, tms.WebMercatorQuad, tms.WorldCRS84Quad)
	r := newRouterFor(t, a)

	var doc ogc.TileSetMetadata
	w := get(t, r, "/collections/osm/tiles/WorldCRS84Quad", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}

	if doc.TileMatrixSetID != tms.WorldCRS84Quad {
		t.Errorf("tileMatrixSetId = %q, want %q", doc.TileMatrixSetID, tms.WorldCRS84Quad)
	}

	// The templated tile link is how a client learns the tile URL, including
	// that the path is z/y/x.
	var item *ogc.Link
	for i := range doc.Links {
		if doc.Links[i].Rel == "item" {
			item = &doc.Links[i]
		}
	}

	if item == nil {
		t.Fatal("no item link")
	}

	if !item.Templated {
		t.Error("item link is not marked templated")
	}

	want := "http://tegola.io/collections/osm/tiles/WorldCRS84Quad/{tileMatrix}/{tileRow}/{tileCol}?f=mvt"
	if item.Href != want {
		t.Errorf("item href = %q, want %q", item.Href, want)
	}

	// Layers carry their own zoom range, which is what a client uses to decide
	// which matrices to ask for.
	if len(doc.Layers) != 2 {
		t.Fatalf("layers = %d, want 2", len(doc.Layers))
	}

	// The map covers -20,-10..20,10, so at low zooms only a couple of tiles hold
	// data. Without limits a client would walk the whole pyramid.
	if len(doc.TileMatrixLimits) == 0 {
		t.Fatal("no tileMatrixSetLimits")
	}

	for _, limit := range doc.TileMatrixLimits {
		if limit.MinTileCol > limit.MaxTileCol || limit.MinTileRow > limit.MaxTileRow {
			t.Errorf("matrix %v has inverted limits: %+v", limit.TileMatrix, limit)
		}
	}

	// z0 of WorldCRS84Quad is two columns by one row, and the map straddles the
	// prime meridian, so it touches both columns and the only row.
	first := doc.TileMatrixLimits[0]
	if first.TileMatrix != "0" || first.MinTileCol != 0 || first.MaxTileCol != 1 || first.MaxTileRow != 0 {
		t.Errorf("z0 limits = %+v, want cols 0..1 and row 0", first)
	}

	t.Run("a scheme the collection does not offer is 404", func(t *testing.T) {
		if w := get(t, r, "/collections/osm/tiles/WGS1984Quad", nil); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

// TestTile covers the tile endpoint, whose path is z/y/x.
func TestTile(t *testing.T) {
	a := newAtlas(t, tms.WebMercatorQuad, tms.WorldCRS84Quad)
	r := newRouterFor(t, a)

	t.Run("serves a vector tile", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/collections/osm/tiles/WebMercatorQuad/3/3/3", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}

		if got := w.Header().Get("Content-Type"); got != ogc.MediaTypeMVT {
			t.Errorf("Content-Type = %q, want %q", got, ogc.MediaTypeMVT)
		}

		// The handler writes gzipped bytes; the server's middleware decides
		// whether the client sees them compressed.
		if _, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes())); err != nil {
			t.Errorf("body is not gzip: %v", err)
		}
	})

	// The whole reason the path order matters: row and column are bounded
	// differently in a non-square scheme, so a transposed path is rejected
	// rather than silently serving another tile.
	t.Run("row and column are bounded independently", func(t *testing.T) {
		// WorldCRS84Quad z1 is 4 columns by 2 rows: col 3 is valid, row 3 is not.
		if w := get(t, r, "/collections/osm/tiles/WorldCRS84Quad/1/1/3", nil); w.Code != http.StatusOK {
			t.Errorf("z1 row 1 col 3: status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}

		if w := get(t, r, "/collections/osm/tiles/WorldCRS84Quad/1/3/1", nil); w.Code != http.StatusBadRequest {
			t.Errorf("z1 row 3 col 1: status = %d, want 400", w.Code)
		}
	})

	t.Run("a zoom the collection holds nothing at is an empty tile", func(t *testing.T) {
		// every layer stops by z8
		w := get(t, r, "/collections/osm/tiles/WebMercatorQuad/12/2000/2000", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		gz, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatalf("body is not gzip: %v", err)
		}

		body, err := io.ReadAll(gz)
		if err != nil {
			t.Fatalf("reading tile: %v", err)
		}

		if len(body) != 0 {
			t.Errorf("empty tile has %d bytes, want 0", len(body))
		}
	})

	t.Run("a matrix the scheme does not have", func(t *testing.T) {
		if w := get(t, r, "/collections/osm/tiles/WebMercatorQuad/99/0/0", nil); w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("an unknown collection", func(t *testing.T) {
		if w := get(t, r, "/collections/nope/tiles/WebMercatorQuad/0/0/0", nil); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

// fixedProvider serves one geometry, in WGS84, whatever tile it is asked for.
//
// The shared test provider returns each tile's own outline, which encodes to the
// same bytes for every tile of every scheme — useless for telling schemes apart.
// A fixed geometry makes a tile's content depend on the ground it covers, which
// is the thing under test.
type fixedProvider struct {
	geometry geom.Geometry
}

func (fixedProvider) Layers() ([]provider.LayerInfo, error) { return nil, nil }

func (p fixedProvider) TileFeatures(_ context.Context, _ string, _ provider.Tile, _ provider.Params, fn func(*provider.Feature) error) error {
	return fn(&provider.Feature{
		ID:       1,
		Geometry: p.geometry,
		SRID:     tegola.WGS84,
		Tags:     map[string]any{},
	})
}

// TestTileDiffersByScheme is the end-to-end form of the Phase 1 property: the
// same z/y/x names different ground in different schemes, so a request for it
// must serve different tiles.
//
// At z3 tile row 3, column 3, WebMercatorQuad covers lon -45..0 and lat 0..41,
// while WorldCRS84Quad — twice as wide — covers lon -112.5..-90 and lat 0..22.5.
// The polygon below sits in the first and not the second.
func TestTileDiffersByScheme(t *testing.T) {
	m := atlas.NewWebMercatorMap("osm")
	m.Bounds = &geom.Extent{-180, -85, 180, 85}
	m.TileBuffer = 0
	m.Layers = []atlas.Layer{{
		Name:              "shape",
		ProviderLayerName: "shape",
		MinZoom:           0,
		MaxZoom:           8,
		Provider: fixedProvider{geometry: geom.Polygon{{
			{-30, 10}, {-20, 10}, {-20, 20}, {-30, 20}, {-30, 10},
		}}},
		GeomType: geom.Polygon{},
	}}
	m.TileMatrixSets = []*tms.TileMatrixSet{
		mustGrid(t, tms.WebMercatorQuad),
		mustGrid(t, tms.WorldCRS84Quad),
	}

	a := &atlas.Atlas{}
	a.AddMap(m)
	r := newRouterFor(t, a)

	features := func(uri string) int {
		req := httptest.NewRequest(http.MethodGet, uri, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%v: status = %d, want 200 (body %s)", uri, w.Code, w.Body.String())
		}

		gz, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatalf("%v: body is not gzip: %v", uri, err)
		}

		body, err := io.ReadAll(gz)
		if err != nil {
			t.Fatalf("%v: reading tile: %v", uri, err)
		}

		var tile vectorTile.Tile
		if err := proto.Unmarshal(body, &tile); err != nil {
			t.Fatalf("%v: decoding tile: %v", uri, err)
		}

		var count int
		for _, layer := range tile.Layers {
			count += len(layer.Features)
		}

		return count
	}

	if got := features("/collections/osm/tiles/WebMercatorQuad/3/3/3"); got != 1 {
		t.Errorf("WebMercatorQuad 3/3/3 holds %d features, want 1", got)
	}

	if got := features("/collections/osm/tiles/WorldCRS84Quad/3/3/3"); got != 0 {
		t.Errorf("WorldCRS84Quad 3/3/3 covers different ground, so it should hold 0 features, got %d", got)
	}
}

// mustGrid resolves a scheme, failing the test if this build cannot serve it.
func mustGrid(t *testing.T, id string) *tms.TileMatrixSet {
	t.Helper()

	grid, err := tms.Get(id)
	if err != nil {
		t.Fatalf("tms.Get(%q): %v", id, err)
	}

	return grid
}

// TestCollectionJSONShape guards the field names OGC clients read, which Go
// struct tags make easy to change by accident.
func TestCollectionJSONShape(t *testing.T) {
	var raw map[string]any
	w := get(t, newRouterFor(t, newAtlas(t)), "/collections/osm/tiles/WebMercatorQuad", &raw)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	for _, field := range []string{"dataType", "crs", "tileMatrixSetId", "tileMatrixSetURI", "tileMatrixSetLimits", "links", "layers"} {
		if _, ok := raw[field]; !ok {
			body, _ := json.Marshal(raw)
			t.Errorf("tileset metadata has no %q: %s", field, body)
		}
	}
}

// TestTileSetTileJSON covers the TileJSON 3.0 representation of a tileset.
func TestTileSetTileJSON(t *testing.T) {
	r := newRouterFor(t, newAtlas(t, tms.WebMercatorQuad, tms.WorldCRS84Quad))

	var doc map[string]any
	w := get(t, r, "/collections/osm/tiles/WorldCRS84Quad?f=tilejson", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}

	if got := doc["tilejson"]; got != "3.0.0" {
		t.Errorf("tilejson = %v, want 3.0.0", got)
	}

	// The point of 3.0: a tileset can say it is not WebMercator. Without this a
	// client renders CRS84 tiles as though they were WebMercator ones.
	if got := doc["crs"]; got == nil || got == "" {
		t.Error("tilejson has no crs, so a client cannot tell which scheme it is")
	}

	if got := doc["tileMatrixSetId"]; got != tms.WorldCRS84Quad {
		t.Errorf("tileMatrixSetId = %v, want %v", got, tms.WorldCRS84Quad)
	}

	tiles, ok := doc["tiles"].([]any)
	if !ok || len(tiles) == 0 {
		t.Fatalf("tiles = %v, want at least one template", doc["tiles"])
	}

	// The template must keep the OGC path order, or a client substituting its
	// own z/x/y fetches transposed tiles.
	want := "http://tegola.io/collections/osm/tiles/WorldCRS84Quad/{z}/{y}/{x}?f=mvt"
	if tiles[0] != want {
		t.Errorf("tiles[0] = %v, want %v", tiles[0], want)
	}

	layers, ok := doc["vector_layers"].([]any)
	if !ok || len(layers) != 2 {
		t.Errorf("vector_layers = %v, want 2", doc["vector_layers"])
	}

	// The default representation is unaffected.
	var canonical ogc.TileSetMetadata
	if w := get(t, r, "/collections/osm/tiles/WorldCRS84Quad", &canonical); w.Code != http.StatusOK {
		t.Fatalf("canonical status = %d, want 200", w.Code)
	}

	if canonical.TileMatrixSetID != tms.WorldCRS84Quad {
		t.Errorf("canonical tileMatrixSetId = %q, want %q", canonical.TileMatrixSetID, tms.WorldCRS84Quad)
	}
}
