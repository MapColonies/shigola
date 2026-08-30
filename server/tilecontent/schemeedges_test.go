package tilecontent_test

import (
	"net/http/httptest"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/internal/mvttest"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/tms"
)

// The scheme-edges fixture, as testdata/postgis/postgis-scheme-edges.sql places
// it: four points, one layer, each sitting where a 4326 layer is exact in both
// tiling schemes.
const (
	edgesCollection = "edges"
	edgesLayer      = "edges"

	// mercatorTopLat is the highest latitude WebMercatorQuad can express. The
	// polar feature sits above it, which is why one scheme serves it and the
	// other has no tile for it at any zoom.
	mercatorTopLat = 85.0511287798066
)

func newEdgesAtlas(t *testing.T) *atlas.Atlas {
	t.Helper()

	return newAtlas(t, edgesCollection, []map[string]any{
		providerLayer(edgesLayer, "point",
			"SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid, name FROM scheme_edges WHERE geom && !BBOX!"),
	})
}

func fetchEdges(t *testing.T, srv *httptest.Server, scheme string, tileMatrix, tileRow, tileCol int) mvttest.Tile {
	t.Helper()

	return fetch(t, srv, edgesCollection, scheme, tileMatrix, tileRow, tileCol)
}

func edgeAt(t *testing.T, tile mvttest.Tile, name string, x, y int32) {
	t.Helper()

	at(t, tile, edgesLayer, name, mvttest.Part{Points: []mvttest.Point{{X: x, Y: y}}})
}

func edgeAbsent(t *testing.T, tile mvttest.Tile, name string) {
	t.Helper()

	absent(t, tile, edgesLayer, name)
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

	srv := newServer(t, newEdgesAtlas(t))

	// The shallowest zoom, where the two schemes differ in shape rather than
	// only in extent, and where the bounding-box transform is under the most
	// strain. WorldCRS84Quad's world is two tiles wide; WebMercatorQuad's is
	// one. A check that only ever asks one scheme cannot see that.
	t.Run("the shallowest zoom of each scheme", func(t *testing.T) {
		t.Run("WebMercatorQuad is one tile wide", func(t *testing.T) {
			tile := fetchEdges(t, srv, tms.WebMercatorQuad, 0, 0, 0)

			// x = (lon + 180) / 360 * 4096; y = 2048 on the equator, where the
			// linear-in-latitude and linear-in-mercator mappings agree.
			assertLayers(t, tile, edgesLayer)
			assertFeatureIDs(t, tile, edgesLayer, 1, 3, 4)
			edgeAt(t, tile, "origin", 1024, 2048)       // lon -90
			edgeAt(t, tile, "corner", 2048, 2048)       // lon 0
			edgeAt(t, tile, "antimeridian", 4088, 2048) // lon 179.296875

			// Above 85.0511287798: not in this grid at any zoom.
			edgeAbsent(t, tile, "polar")

			mvttest.AssertGolden(t, "testdata/golden/edges-WebMercatorQuad-0-0-0.txt", tile.Render())
		})

		t.Run("WorldCRS84Quad is two tiles wide", func(t *testing.T) {
			// Column 0 spans lon -180..0, so x = (lon + 180) / 180 * 4096.
			// Row 0 spans lat -90..90, so y = (90 - lat) / 180 * 4096.
			west := fetchEdges(t, srv, tms.WorldCRS84Quad, 0, 0, 0)
			assertLayers(t, west, edgesLayer)
			assertFeatureIDs(t, west, edgesLayer, 1, 2, 4)
			edgeAt(t, west, "origin", 2048, 2048) // lon -90, lat 0
			edgeAt(t, west, "polar", 2048, 64)    // lon -90, lat 87.1875
			edgeAt(t, west, "corner", 4096, 2048) // lon 0, this column's right edge
			edgeAbsent(t, west, "antimeridian")
			mvttest.AssertGolden(t, "testdata/golden/edges-WorldCRS84Quad-0-0-0.txt", west.Render())

			// Column 1 spans lon 0..180, so x = lon / 180 * 4096.
			east := fetchEdges(t, srv, tms.WorldCRS84Quad, 0, 0, 1)
			assertFeatureIDs(t, east, edgesLayer, 3, 4)
			edgeAt(t, east, "antimeridian", 4080, 2048) // lon 179.296875
			edgeAt(t, east, "corner", 0, 2048)          // lon 0, this column's left edge
			edgeAbsent(t, east, "origin")
			edgeAbsent(t, east, "polar")
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
			tile := fetchEdges(t, srv, tms.WorldCRS84Quad, 1, 0, 1)
			edgeAt(t, tile, "polar", 0, 128) // lon -90 is column 1's left edge
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
					tile := fetchEdges(t, srv, tms.WebMercatorQuad, zoom, 0, col)
					edgeAbsent(t, tile, "polar")
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
			edgeAt(t, fetchEdges(t, srv, tms.WebMercatorQuad, 1, 0, 1), "antimeridian", 4080, 4096)
			edgeAt(t, fetchEdges(t, srv, tms.WebMercatorQuad, 1, 1, 1), "antimeridian", 4080, 0)

			// The column before it holds none of this.
			edgeAbsent(t, fetchEdges(t, srv, tms.WebMercatorQuad, 1, 0, 0), "antimeridian")
		})

		t.Run("WorldCRS84Quad", func(t *testing.T) {
			edgeAt(t, fetchEdges(t, srv, tms.WorldCRS84Quad, 0, 0, 1), "antimeridian", 4080, 2048)
			edgeAbsent(t, fetchEdges(t, srv, tms.WorldCRS84Quad, 0, 0, 0), "antimeridian")
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
		// WorldCRS84Quad's 4 by 2. Four tiles meet there in each, and the
		// feature belongs to all four.
		type tcase struct {
			scheme   string
			row, col int
			x, y     int32
		}

		fn := func(tc tcase) func(*testing.T) {
			return func(t *testing.T) {
				edgeAt(t, fetchEdges(t, srv, tc.scheme, 1, tc.row, tc.col), "corner", tc.x, tc.y)
			}
		}

		tests := map[string]tcase{
			// The corner is each tile's own far side, so its coordinate is the
			// extent or zero in each axis, according to which quadrant the tile
			// is.
			"WebMercatorQuad, north-west of the corner": {tms.WebMercatorQuad, 0, 0, extent, extent},
			"WebMercatorQuad, north-east of the corner": {tms.WebMercatorQuad, 0, 1, 0, extent},
			"WebMercatorQuad, south-west of the corner": {tms.WebMercatorQuad, 1, 0, extent, 0},
			"WebMercatorQuad, south-east of the corner": {tms.WebMercatorQuad, 1, 1, 0, 0},

			"WorldCRS84Quad, north-west of the corner": {tms.WorldCRS84Quad, 0, 1, extent, extent},
			"WorldCRS84Quad, north-east of the corner": {tms.WorldCRS84Quad, 0, 2, 0, extent},
			"WorldCRS84Quad, south-west of the corner": {tms.WorldCRS84Quad, 1, 1, extent, 0},
			"WorldCRS84Quad, south-east of the corner": {tms.WorldCRS84Quad, 1, 2, 0, 0},
		}

		for name, tc := range tests {
			t.Run(name, fn(tc))
		}
	})
}
