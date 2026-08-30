package tilecontent_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/mvttest"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/provider/postgis"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom"
)

// dataTestEnv gates the tile-content checks.
//
// They have their own switch rather than riding on RUN_POSTGIS_TESTS so that
// exactly one CI job runs them. Sharing that gate would run them a second time
// inside the general test job, where a failure says only that some test failed.
const dataTestEnv = "RUN_DATA_TESTS"

// The fixture, as testdata/postgis/postgis-scheme-edges.sql places it. Every
// coordinate asserted below was derived from these, and from the grid's own
// arithmetic, rather than read off a previous run.
const (
	fixtureCollection = "edges"
	fixtureLayer      = "edges"

	// extent is the tile-space grid every assertion here is expressed in.
	extent = 4096

	// mercatorTopLat is the highest latitude WebMercatorQuad can express. The
	// polar feature sits above it, which is why one scheme serves it and the
	// other has no tile for it at any zoom.
	mercatorTopLat = 85.0511287798066
)

// newEdgesAtlas builds an atlas serving the scheme_edges fixture in both
// tiling schemes.
//
// One layer, not one per scheme, and the fixture's placement is what allows it.
// ST_AsMVTGeom maps the bounding box onto the tile grid affinely; for a 4326
// layer that is linear in latitude, where WebMercatorQuad's grid is linear in
// mercator y. The two agree exactly at a tile's own edges and at the equator
// and diverge in between -- roughly 100 units in 4096 at zoom 4, per
// .github/cite/config.toml. Every fixture point sits where they agree, so one
// layer is exact in both schemes. A point at a general latitude would need a
// per-scheme layer, and that is a trap worth knowing about before adding one.
func newEdgesAtlas(t *testing.T) *atlas.Atlas {
	t.Helper()

	cfg := dict.Dict{
		postgis.ConfigKeyName: "edges_provider",
		postgis.ConfigKeyURI: ttools.GetEnvDefault(
			"PGURI",
			"postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable",
		),
		postgis.ConfigKeySSLMode: ttools.GetEnvDefault("PGSSLMODE", "disable"),
		postgis.ConfigKeyLayers: []map[string]any{
			{
				postgis.ConfigKeyLayerName:   fixtureLayer,
				postgis.ConfigKeyGeomIDField: "fid",
				postgis.ConfigKeyGeomField:   "geom",
				// Declared rather than inferred: startup inference reads the
				// layer's SQL back, and a query ending in ST_AsMVTGeom returns
				// tile-space geometry it cannot type.
				postgis.ConfigKeyGeomType: "point",
				postgis.ConfigKeySRID:     4326,
				postgis.ConfigKeySQL:      "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid, name FROM scheme_edges WHERE geom && !BBOX!",
			},
		},
	}

	prvd, err := postgis.NewMVTTileProvider(cfg, nil)
	if err != nil {
		t.Fatalf("building the fixture provider: %v", err)
	}

	m := atlas.NewWebMercatorMap(fixtureCollection)
	// The default bounds stop at mercator's own top latitude, which would put
	// the polar feature outside the map that is supposed to serve it. This map
	// covers the whole globe because one of its two schemes does.
	m.Bounds = &geom.Extent{-180, -90, 180, 90}
	m.SetMVTProvider("edges_provider", prvd)
	m.Layers = []atlas.Layer{{
		Name:              fixtureLayer,
		ProviderLayerName: fixtureLayer,
		MinZoom:           0,
		MaxZoom:           22,
	}}

	for _, id := range []string{tms.WebMercatorQuad, tms.WorldCRS84Quad} {
		grid, err := tms.Get(id)
		if err != nil {
			t.Fatalf("tms.Get(%v) = %v, want nil", id, err)
		}
		if id == tms.WebMercatorQuad {
			m.TileMatrixSets = []*tms.TileMatrixSet{grid}
			continue
		}
		m.TileMatrixSets = append(m.TileMatrixSets, grid)
	}

	a := &atlas.Atlas{}
	a.AddMap(m)

	return a
}

// tileURI builds an OGC tile path. The segments are tileMatrix, tileRow,
// tileCol -- z/y/x, transposed from the z/x/y the rest of the codebase uses,
// which is why they are named rather than positional here.
func tileURI(scheme string, tileMatrix, tileRow, tileCol int) string {
	return fmt.Sprintf("/collections/%s/tiles/%s/%d/%d/%d", fixtureCollection, scheme, tileMatrix, tileRow, tileCol)
}

// fetchTile asks for one tile and returns it decoded.
//
// It advertises gzip and decodes the gzip itself rather than letting the client
// do it transparently, so that what is asserted is what came off the wire.
func fetchTile(t *testing.T, srv *httptest.Server, scheme string, tileMatrix, tileRow, tileCol int) mvttest.Tile {
	t.Helper()

	uri := tileURI(scheme, tileMatrix, tileRow, tileCol)

	req, err := http.NewRequest(http.MethodGet, srv.URL+uri, nil)
	if err != nil {
		t.Fatalf("building the request for %v: %v", uri, err)
	}
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %v: %v", uri, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %v status = %d, want 200", uri, resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("GET %v Content-Encoding = %q, want gzip", uri, got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %v: %v", uri, err)
	}

	tile := mvttest.Decode(t, body)

	// What came back, before anything is asserted about it. A check that
	// reports only its own verdict cannot tell "the tile held what I expected"
	// apart from "the tile held nothing and I expected nothing", and the second
	// is how a fixture that quietly stopped loading looks.
	t.Logf("GET %v -> 200 %s, %d bytes gzipped, holding %s",
		uri, resp.Header.Get("Content-Type"), len(body), describe(tile))

	return tile
}

// describe renders a tile's contents on one line, for the log.
func describe(tile mvttest.Tile) string {
	layer, ok := tile.Layer(fixtureLayer)
	if !ok {
		return "no " + fixtureLayer + " layer"
	}
	if len(layer.Features) == 0 {
		return "an empty " + fixtureLayer + " layer"
	}

	parts := make([]string, 0, len(layer.Features))
	for _, f := range layer.Features {
		name := f.Tags["name"]
		if name == "" {
			name = "unnamed"
		}
		where := "no geometry"
		if len(f.Geom) > 0 {
			where = fmt.Sprintf("(%d,%d)", f.Geom[0].X, f.Geom[0].Y)
		}
		parts = append(parts, fmt.Sprintf("%s#%d%s", name, f.ID, where))
	}

	return fmt.Sprintf("%d features: %s", len(layer.Features), strings.Join(parts, " "))
}

// featureAt asserts that the layer holds exactly one feature with the given
// name, and that it sits at the given tile-space position.
func featureAt(t *testing.T, tile mvttest.Tile, name string, x, y int32) {
	t.Helper()

	layer, ok := tile.Layer(fixtureLayer)
	if !ok {
		t.Fatalf("layer %q is missing; got %v", fixtureLayer, tile.LayerNames())
	}

	f, ok := layer.FeatureByTag("name", name)
	if !ok {
		t.Fatalf("want exactly one feature named %q, got %v", name, renderNames(layer))
	}

	if !f.HasID {
		t.Errorf("feature %q carries no id; the layer's id_fieldname did not reach ST_AsMVT", name)
	}

	want := []mvttest.Op{{Cmd: mvttest.MoveTo, X: x, Y: y}}
	if len(f.Geom) != 1 || f.Geom[0] != want[0] {
		t.Errorf("feature %q geometry = %v, want %v", name, f.Geom, want)
	}

	// Inclusive at both ends on purpose: a feature exactly on a tile edge sits
	// at 0 or at the extent, and that is where the corner case below lives.
	for _, op := range f.Geom {
		if op.X < 0 || op.X > extent || op.Y < 0 || op.Y > extent {
			t.Errorf("feature %q has %v outside the 0..%d tile grid", name, op, extent)
		}
	}

	if !t.Failed() {
		t.Logf("  ok  %-13s id=%d at (%d,%d)", name, f.ID, x, y)
	}
}

// featureAbsent asserts the named feature is not in this tile.
//
// Only this tile: whether the scheme can reach the feature at all is a
// different claim, and the caller makes it by asking for every tile that could
// hold it. Saying more than that here would put a conclusion in the log that
// the assertion did not reach.
func featureAbsent(t *testing.T, tile mvttest.Tile, name string) {
	t.Helper()

	layer, ok := tile.Layer(fixtureLayer)
	if !ok {
		t.Logf("  ok  %-13s absent from this tile, which holds no %v layer at all", name, fixtureLayer)
		return // no layer at all is the strongest form of absent
	}
	if _, ok := layer.FeatureByTag("name", name); ok {
		t.Errorf("feature %q is present, want it absent; the layer holds %v", name, renderNames(layer))
		return
	}

	t.Logf("  ok  %-13s absent from this tile", name)
}

func renderNames(l mvttest.Layer) []string {
	names := make([]string, 0, len(l.Features))
	for _, f := range l.Features {
		names = append(names, f.Tags["name"])
	}
	return names
}

// TestTileContentSchemeEdges covers the places the two tiling schemes stop
// agreeing: the poles, the antimeridian, the shallowest zoom, and a tile corner
// (MAPCO-11552).
//
// Every expected coordinate below is derived from the grid rather than recorded
// from a run. WebMercatorQuad's world is one tile at zoom 0 spanning
// -180..180 by -85.0511..85.0511; WorldCRS84Quad's is two tiles spanning
// -180..180 by -90..90. A longitude maps linearly in both, so
// x = (lon - left) / width * 4096, and latitude maps linearly in
// WorldCRS84Quad, so y = (top - lat) / height * 4096.
func TestTileContentSchemeEdges(t *testing.T) {
	ttools.ShouldSkip(t, dataTestEnv)

	srv := httptest.NewServer(server.NewRouter(newEdgesAtlas(t)))
	defer srv.Close()

	// The shallowest zoom, where the two schemes differ in shape rather than
	// only in extent, and where the bounding-box transform is under the most
	// strain. WorldCRS84Quad's world is two tiles wide; WebMercatorQuad's is
	// one. A check that only ever asks one scheme cannot see that.
	t.Run("the shallowest zoom of each scheme", func(t *testing.T) {
		t.Run("WebMercatorQuad is one tile wide", func(t *testing.T) {
			tile := fetchTile(t, srv, tms.WebMercatorQuad, 0, 0, 0)

			// x = (lon + 180) / 360 * 4096; y = 2048 on the equator, where the
			// linear-in-latitude and linear-in-mercator mappings agree.
			featureAt(t, tile, "origin", 1024, 2048)       // lon -90
			featureAt(t, tile, "corner", 2048, 2048)       // lon 0
			featureAt(t, tile, "antimeridian", 4088, 2048) // lon 179.296875

			// Above 85.0511287798: not in this grid at any zoom.
			featureAbsent(t, tile, "polar")

			mvttest.AssertGolden(t, "testdata/golden/edges-WebMercatorQuad-0-0-0.txt", tile.Render())
		})

		t.Run("WorldCRS84Quad is two tiles wide", func(t *testing.T) {
			// Column 0 spans lon -180..0, so x = (lon + 180) / 180 * 4096.
			// Row 0 spans lat -90..90, so y = (90 - lat) / 180 * 4096.
			west := fetchTile(t, srv, tms.WorldCRS84Quad, 0, 0, 0)
			featureAt(t, west, "origin", 2048, 2048) // lon -90, lat 0
			featureAt(t, west, "polar", 2048, 64)    // lon -90, lat 87.1875
			featureAbsent(t, west, "antimeridian")
			mvttest.AssertGolden(t, "testdata/golden/edges-WorldCRS84Quad-0-0-0.txt", west.Render())

			// Column 1 spans lon 0..180, so x = lon / 180 * 4096.
			east := fetchTile(t, srv, tms.WorldCRS84Quad, 0, 0, 1)
			featureAt(t, east, "antimeridian", 4080, 2048) // lon 179.296875
			featureAbsent(t, east, "origin")
			featureAbsent(t, east, "polar")
			mvttest.AssertGolden(t, "testdata/golden/edges-WorldCRS84Quad-0-0-1.txt", east.Render())
		})
	})

	// The two schemes cover different worlds. WorldCRS84Quad reaches the pole;
	// WebMercatorQuad stops where its projection stops being able to express a
	// latitude. Ground above that line is served by one and simply has no tile
	// in the other -- which is a fact about the scheme, not an error, so the
	// assertion is an absence rather than a status code.
	t.Run("a feature above mercator's top latitude", func(t *testing.T) {
		t.Run("is served by the scheme that reaches it", func(t *testing.T) {
			// Deeper than zoom 0 as well, so this is about the grid rather than
			// about one tile. Zoom 1 splits the world into 4 by 2; lon -90 is
			// the boundary between columns 0 and 1, and selection by
			// bounding-box intersection includes the boundary, so the feature
			// is in both.
			tile := fetchTile(t, srv, tms.WorldCRS84Quad, 1, 0, 1)
			featureAt(t, tile, "polar", 0, 128) // lon -90 is column 1's left edge
		})

		t.Run("is unreachable in the scheme that does not", func(t *testing.T) {
			if mercatorTopLat >= 87.1875 {
				t.Fatal("the polar fixture point is no longer above mercator's top latitude")
			}

			// Every tile in the top row of the first three zooms. Nothing below
			// the top row can hold a higher latitude, so this covers the grid
			// rather than a guess at which column the longitude falls in.
			for zoom := 0; zoom <= 2; zoom++ {
				cols := 1 << zoom
				for col := 0; col < cols; col++ {
					tile := fetchTile(t, srv, tms.WebMercatorQuad, zoom, 0, col)
					featureAbsent(t, tile, "polar")
				}
			}
		})
	})

	// The last column is where an off-by-one in the column arithmetic, or a
	// longitude wrapped the wrong way, produces a tile that looks plausible and
	// holds the wrong ground. Each scheme is asked at its own shallowest zoom
	// that has more than one column, which is zoom 1 for WebMercatorQuad and
	// zoom 0 for WorldCRS84Quad.
	t.Run("the last column of each scheme", func(t *testing.T) {
		t.Run("WebMercatorQuad", func(t *testing.T) {
			// Zoom 1, column 1 spans lon 0..180: x = lon / 180 * 4096. The
			// equator is this column's bottom edge in row 0 and its top edge in
			// row 1, so the feature is in both.
			featureAt(t, fetchTile(t, srv, tms.WebMercatorQuad, 1, 0, 1), "antimeridian", 4080, 4096)
			featureAt(t, fetchTile(t, srv, tms.WebMercatorQuad, 1, 1, 1), "antimeridian", 4080, 0)

			// The column before it holds none of this.
			featureAbsent(t, fetchTile(t, srv, tms.WebMercatorQuad, 1, 0, 0), "antimeridian")
		})

		t.Run("WorldCRS84Quad", func(t *testing.T) {
			featureAt(t, fetchTile(t, srv, tms.WorldCRS84Quad, 0, 0, 1), "antimeridian", 4080, 2048)
			featureAbsent(t, fetchTile(t, srv, tms.WorldCRS84Quad, 0, 0, 0), "antimeridian")
		})
	})

	// A feature exactly on a tile corner belongs to every tile that meets
	// there. The provider selects by bounding-box intersection, which includes
	// the boundary, so ground on a shared edge is in both tiles that share it.
	//
	// This is correct and is pinned deliberately. It reads like a leak against
	// the epic's framing of what a tile should and should not contain, and the
	// obvious "fix" -- narrowing the selection to exclude the boundary -- would
	// drop features sitting exactly on tile edges, which in a real dataset is a
	// great many of them.
	t.Run("a feature on a tile corner is in every tile sharing it", func(t *testing.T) {
		// The prime meridian meets the equator. At zoom 1 that is the middle of
		// WebMercatorQuad's 2 by 2 world, and a column and row boundary in
		// WorldCRS84Quad's 4 by 2.
		t.Run("WebMercatorQuad", func(t *testing.T) {
			for _, c := range []struct {
				row, col int
				x, y     int32
			}{
				{row: 0, col: 0, x: 4096, y: 4096}, // corner is bottom-right
				{row: 0, col: 1, x: 0, y: 4096},    // bottom-left
				{row: 1, col: 0, x: 4096, y: 0},    // top-right
				{row: 1, col: 1, x: 0, y: 0},       // top-left
			} {
				name := fmt.Sprintf("row %d column %d", c.row, c.col)
				t.Run(name, func(t *testing.T) {
					featureAt(t, fetchTile(t, srv, tms.WebMercatorQuad, 1, c.row, c.col), "corner", c.x, c.y)
				})
			}
		})

		t.Run("WorldCRS84Quad", func(t *testing.T) {
			for _, c := range []struct {
				row, col int
				x, y     int32
			}{
				{row: 0, col: 1, x: 4096, y: 4096},
				{row: 0, col: 2, x: 0, y: 4096},
				{row: 1, col: 1, x: 4096, y: 0},
				{row: 1, col: 2, x: 0, y: 0},
			} {
				name := fmt.Sprintf("row %d column %d", c.row, c.col)
				t.Run(name, func(t *testing.T) {
					featureAt(t, fetchTile(t, srv, tms.WorldCRS84Quad, 1, c.row, c.col), "corner", c.x, c.y)
				})
			}
		})
	})
}
