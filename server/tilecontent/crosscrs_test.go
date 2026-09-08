package tilecontent_test

import (
	"net/http/httptest"
	"testing"

	"github.com/MapColonies/shigola/internal/mvttest"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/tms"
)

// The cross-CRS fixture, as testdata/postgis/postgis-cross-crs.sql places it:
// three points on the -90 meridian, served through two layers over the same
// table -- one that hands ST_AsMVTGeom 4326 geometry, one that hands it 3857.
const (
	crossCollection = "crosscrs"
	crossNativeLyr  = "native"
	crossMercLyr    = "mercator"
)

func newCrossAtlas(t *testing.T) *httptest.Server {
	t.Helper()

	a := newAtlas(t, crossCollection, []map[string]any{
		providerLayerSRID(crossNativeLyr, "point", 4326,
			"SELECT ST_AsMVTGeom(ST_Transform(geom, !TILE_SRID!), !TILE_BBOX!) AS geom, fid, name "+
				"FROM cross_crs WHERE geom && !BBOX!"),
		// The same ground, reaching ST_AsMVTGeom as 3857 -- the shape an
		// OpenMapTiles import has, and the one that produced the reported
		// symptom. The declared srid is 3857 because that is what the subquery
		// returns, which is what !BBOX! has to be converted into to select with.
		providerLayerSRID(crossMercLyr, "point", 3857,
			"SELECT ST_AsMVTGeom(ST_Transform(geom, !TILE_SRID!), !TILE_BBOX!) AS geom, fid, name "+
				"FROM (SELECT ST_Transform(geom, 3857) AS geom, fid, name FROM cross_crs) q "+
				"WHERE geom && !BBOX!"),
	})

	return newServer(t, a)
}

// crossAt asserts that both layers put the named point at the same place, and
// that the place is the one the caller derived from the grid.
//
// Checking the two layers against each other is the assertion that needs no
// arithmetic: the same ground, in the same scheme, at the same zoom, must
// produce the same tile whichever SRID it was stored in. Checking them against
// a derived coordinate is what stops both being wrong the same way.
func crossAt(t *testing.T, tile mvttest.Tile, name string, x, y int32) {
	t.Helper()

	for _, layer := range []string{crossNativeLyr, crossMercLyr} {
		at(t, tile, layer, name, mvttest.Part{Points: []mvttest.Point{{X: x, Y: y}}})
	}
}

// TestTileContentCrossCRS pins the tile a scheme produces against the SRID the
// layer is stored in (MAPCO-11599).
//
// The two have to be independent, and were not: the provider converted a tile's
// envelope into the layer's SRID and handed that to ST_AsMVTGeom, which spaces a
// tile affinely across whatever envelope it is given. A 3857 layer served in
// WorldCRS84Quad therefore came out spaced by mercator y inside a tile the
// client draws as linear in latitude -- and at the shallow zooms, where a tile
// spans most of a hemisphere, that is most of the tile's height.
//
// Every expected coordinate below is derived from the grid. A longitude maps
// linearly in both schemes, so x = (lon - left) / width * 4096. Latitude maps
// linearly in WorldCRS84Quad, so y = (top - lat) / height * 4096; in
// WebMercatorQuad it maps linearly in mercator y, so
// y = (yTop - R*ln(tan(pi/4 + lat/2))) / (2*yTop) * 4096 with
// yTop = 20037508.3428.
func TestTileContentCrossCRS(t *testing.T) {
	ttools.ShouldSkip(t, dataTestEnv)

	srv := newCrossAtlas(t)

	// The zoom the report was about. A WorldCRS84Quad zoom 1 tile spans a
	// quarter of the world in longitude and a whole hemisphere in latitude, so
	// the gap between spacing it by latitude and spacing it by mercator y is
	// almost the tile: 'mid' belongs at 2048 and used to arrive at 3999,
	// 'high' belongs at 512 and used to arrive at 3842.
	t.Run("WorldCRS84Quad spaces a tile by latitude", func(t *testing.T) {
		// Zoom 1 is 4 columns by 2 rows. Column 1 spans lon -90..0 and row 0
		// spans lat 90..0, so lon -90 is the column's left edge at x = 0 and
		// y = (90 - lat) / 90 * 4096.
		tile := fetch(t, srv, crossCollection, tms.WorldCRS84Quad, 1, 0, 1)

		crossAt(t, tile, "mid", 0, 2048) // lat 45
		crossAt(t, tile, "high", 0, 512) // lat 78.75
		crossAt(t, tile, "equator", 0, 4096)
	})

	t.Run("WorldCRS84Quad at its shallowest zoom", func(t *testing.T) {
		// Zoom 0 is 2 columns by 1 row. Column 0 spans lon -180..0 and row 0
		// spans lat 90..-90, so x = (lon + 180) / 180 * 4096 and
		// y = (90 - lat) / 180 * 4096.
		//
		// This tile reaches 90S, whose mercator y is negative infinity. It used
		// to be converted into the layer's SRID anyway and formatted into
		// ST_MakeEnvelope as a bare -Inf, which PostgreSQL reads as a negated
		// identifier: the whole tile failed with `column "inf" does not exist`.
		tile := fetch(t, srv, crossCollection, tms.WorldCRS84Quad, 0, 0, 0)

		crossAt(t, tile, "equator", 2048, 2048)
		crossAt(t, tile, "mid", 2048, 1024) // lat 45
		crossAt(t, tile, "high", 2048, 256) // lat 78.75
	})

	// The same independence in the other direction: a 4326 layer served in
	// WebMercatorQuad has to be spaced by mercator y, not by latitude. That one
	// was wrong too, and invisibly so -- the error is zero at a tile's own edges
	// and at the equator, which is exactly where the scheme_edges fixture had to
	// place its points to be exact.
	t.Run("WebMercatorQuad spaces a tile by mercator y", func(t *testing.T) {
		// Zoom 0 is one tile spanning -180..180 by -85.0511..85.0511, so
		// x = (lon + 180) / 360 * 4096.
		tile := fetch(t, srv, crossCollection, tms.WebMercatorQuad, 0, 0, 0)

		crossAt(t, tile, "equator", 1024, 2048)
		// Spacing by latitude instead would put these at 964 and 150.
		crossAt(t, tile, "mid", 1024, 1473) // lat 45
		crossAt(t, tile, "high", 1024, 537) // lat 78.75
	})
}
