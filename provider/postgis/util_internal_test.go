package postgis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/maths/webmercator"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom/slippy"
)

func TestReplaceTokens(t *testing.T) {
	type tcase struct {
		sql      string
		tile     provider.Tile
		expected string
		layer    Layer
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			sql, err := replaceTokens(tc.sql, &tc.layer, tc.tile, true)
			if err != nil {
				t.Errorf("unexpected error, Expected nil Got %v", err)
				return
			}

			if sql != tc.expected {
				t.Errorf("incorrect sql,\n Expected \n \t%v\n Got \n \t%v", tc.expected, sql)
				return
			}
		}
	}

	tests := map[string]tcase{
		"replace BBOX": {
			sql:      "SELECT * FROM foo WHERE geom && !BBOX!",
			layer:    Layer{srid: shigola.WebMercator},
			tile:     provider.NewTile(2, 1, 1, 64, shigola.WebMercator),
			expected: "SELECT * FROM foo WHERE geom && ST_MakeEnvelope(-10175297.20532266,-156543.03392804,156543.03392804,10175297.20532266,3857)",
		},
		"replace BBOX for WorldCRS84Quad tile": {
			sql:      "SELECT * FROM foo WHERE geom && !BBOX!",
			layer:    Layer{srid: shigola.WGS84},
			tile:     provider.NewTile(0, 1, 0, 64, shigola.WGS84),
			expected: "SELECT * FROM foo WHERE geom && ST_MakeEnvelope(-2.81250000,-92.81250000,182.81250000,92.81250000,4326)",
		},
		"replace BBOX with != in query": {
			sql:      "SELECT * FROM foo WHERE geom && !BBOX! AND bar != 42",
			layer:    Layer{srid: shigola.WebMercator},
			tile:     provider.NewTile(2, 1, 1, 64, shigola.WebMercator),
			expected: "SELECT * FROM foo WHERE geom && ST_MakeEnvelope(-10175297.20532266,-156543.03392804,156543.03392804,10175297.20532266,3857) AND bar != 42",
		},
		"replace BBOX and ZOOM 1": {
			sql:      "SELECT id, scalerank=!ZOOM! FROM foo WHERE geom && !BBOX!",
			layer:    Layer{srid: shigola.WebMercator},
			tile:     provider.NewTile(2, 1, 1, 64, shigola.WebMercator),
			expected: "SELECT id, scalerank=2 FROM foo WHERE geom && ST_MakeEnvelope(-10175297.20532266,-156543.03392804,156543.03392804,10175297.20532266,3857)",
		},
		"replace BBOX and ZOOM 2": {
			sql:      "SELECT id, scalerank=!ZOOM! FROM foo WHERE geom && !BBOX!",
			layer:    Layer{srid: shigola.WebMercator},
			tile:     provider.NewTile(16, 11241, 26168, 64, shigola.WebMercator),
			expected: "SELECT id, scalerank=16 FROM foo WHERE geom && ST_MakeEnvelope(-13163688.81778845,4035254.04260249,-13163058.21230510,4035884.64808584,3857)",
		},
		"replace pixel_width/height and scale_denominator": {
			sql:   "SELECT id, !pixel_width! as width, !pixel_height! as height, !scale_denominator! as scale_denom FROM foo WHERE geom && !BBOX!",
			layer: Layer{srid: shigola.WebMercator},
			tile:  provider.NewTile(11, 1070, 676, 64, shigola.WebMercator),
			// The last digit of scale_denom and of the envelope's minx moved by
			// 1e-8 (10 nanometres) when tile extents started coming from the
			// TileMatrixSet registry: the WebMercatorQuad document's origin and
			// cell sizes are exact, where the previous slippy grid round-tripped
			// through a projection and came out very slightly asymmetric.
			expected: "SELECT id, 76.43702829 as width, 76.43702829 as height, 272989.38673277 as scale_denom FROM foo WHERE geom && ST_MakeEnvelope(899816.69697310,6789748.34851564,919996.07244038,6809927.72398292,3857)",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestUppercaseTokens(t *testing.T) {
	type tcase struct {
		str      string
		expected string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			out := uppercaseTokens(tc.str)

			if out != tc.expected {
				t.Errorf("expected \n \t%v\n out \n \t%v", tc.expected, out)
				return
			}
		}
	}

	tests := map[string]tcase{
		"uppercase tokens": {
			str:      "this !lower! case !STrInG! should uppercase !TOKENS!",
			expected: "this !LOWER! case !STRING! should uppercase !TOKENS!",
		},
		"no tokens": {
			str:      "no token",
			expected: "no token",
		},
		"empty string": {
			str:      "",
			expected: "",
		},
		"unclosed token": {
			str:      "unclosed !token",
			expected: "unclosed !token",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// mercatorEnvelope renders the layer-SRID envelope the provider should emit for
// a geographic tile against a mercator layer, from the corner it should have
// converted.
//
// The latitudes are written out by the caller, so the clamp is still what is
// being asserted: an unclamped provider produces -Inf or 238107693.26 at a pole,
// neither of which this can render. The mercator y for them is computed rather
// than written because ST_MakeEnvelope is formatted to eight decimal places, and
// at a magnitude of 2e7 that is fifteen significant digits -- past the point
// where amd64 and arm64 libm agree on tan and log. The literal that used to be
// here passed locally and failed in CI on the sign of a zero.
func mercatorEnvelope(minLon, minLat, maxLon, maxLat float64) string {
	return fmt.Sprintf("ST_MakeEnvelope(%.8f,%.8f,%.8f,%.8f,3857)",
		webmercator.PLonToX(minLon), webmercator.PLatToY(minLat),
		webmercator.PLonToX(maxLon), webmercator.PLatToY(maxLat))
}

// TestReplaceTokensTileCRS covers the two envelopes replaceTokens emits once the
// tiling scheme's CRS stops matching the layer's (MAPCO-11614).
//
// Every expected value is derived from the grid rather than recorded from a run.
// WorldCRS84Quad z1 is four columns by two rows over -180..180 by -90..90, so
// column 2 row 0 is lon 0..90 by lat 0..90; the mercator envelope for it is
// R*rad(lon) across and R*ln(tan(pi/4+rad(lat)/2)) up, with the top clamped to
// the mercator limit because 90N has no mercator y.
func TestReplaceTokensTileCRS(t *testing.T) {
	type tcase struct {
		sql        string
		tile       provider.Tile
		layer      Layer
		withBuffer bool
		expected   string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			sql, err := replaceTokens(tc.sql, &tc.layer, tc.tile, tc.withBuffer)
			if err != nil {
				t.Errorf("unexpected error, Expected nil Got %v", err)
				return
			}

			if sql != tc.expected {
				t.Errorf("incorrect sql,\n Expected \n \t%v\n Got \n \t%v", tc.expected, sql)
			}
		}
	}

	tests := map[string]tcase{
		// The tile whose symptom opened the ticket: a 3857 layer served in
		// WorldCRS84Quad. !BBOX! selects in the SRID the index is in, !TILE_BBOX!
		// clips in the one the scheme spaces the tile by, and they are different
		// rectangles in different units.
		"3857 layer in WorldCRS84Quad": {
			sql:   "SELECT ST_AsMVTGeom(ST_Transform(geom,!TILE_SRID!), !TILE_BBOX!) FROM foo WHERE geom && !BBOX!",
			layer: Layer{srid: shigola.WebMercator},
			tile:  provider.NewTile(1, 2, 0, 64, shigola.WGS84),
			// lat 90 clamps to the mercator limit; lon and the equator convert
			// as they are.
			expected: "SELECT ST_AsMVTGeom(ST_Transform(geom,4326), " +
				"ST_MakeEnvelope(0.00000000,0.00000000,90.00000000,90.00000000,4326)) " +
				"FROM foo WHERE geom && " +
				mercatorEnvelope(0, 0, 90, mercatorLatLimit),
		},
		// The crash: WorldCRS84Quad z0 reaches the south pole, whose mercator y
		// is -Inf, and its buffered extent reaches -92.8125 -- the buffer is in
		// MVT extent units, so 64 of them is 180/4096*64 degrees. Both used to be
		// formatted into ST_MakeEnvelope, where PostgreSQL read "-Inf" as a
		// negated identifier and failed the whole tile with
		// `column "inf" does not exist`.
		"WorldCRS84Quad pole clamps into the mercator range": {
			sql:        "SELECT !TILE_BBOX! AS tile, !BBOX! AS layer",
			layer:      Layer{srid: shigola.WebMercator},
			tile:       provider.NewTile(0, 0, 0, 64, shigola.WGS84),
			withBuffer: true,
			expected: "SELECT ST_MakeEnvelope(-182.81250000,-92.81250000,2.81250000,92.81250000,4326) AS tile, " +
				mercatorEnvelope(-182.8125, -mercatorLatLimit, 2.8125, mercatorLatLimit) + " AS layer",
		},
		// A geographic layer in a geographic scheme needs no transform, and the
		// two envelopes stay one rectangle -- the case that hid the bug.
		"4326 layer in WorldCRS84Quad leaves both envelopes alone": {
			sql:   "SELECT !TILE_BBOX! AS tile, !BBOX! AS layer",
			layer: Layer{srid: shigola.WGS84},
			tile:  provider.NewTile(1, 2, 0, 64, shigola.WGS84),
			expected: "SELECT ST_MakeEnvelope(0.00000000,0.00000000,90.00000000,90.00000000,4326) AS tile, " +
				"ST_MakeEnvelope(0.00000000,0.00000000,90.00000000,90.00000000,4326) AS layer",
		},
		// WebMercatorQuad z2 is four columns square over +-20037508.34; column 1
		// row 1 is the quadrant just west of the prime meridian and just north of
		// the equator. Nothing about this request changed.
		"3857 layer in WebMercatorQuad leaves both envelopes alone": {
			sql:   "SELECT !TILE_BBOX! AS tile, !BBOX! AS layer",
			layer: Layer{srid: shigola.WebMercator},
			tile:  provider.NewTile(2, 1, 1, 64, shigola.WebMercator),
			expected: "SELECT ST_MakeEnvelope(-10018754.17139462,0.00000000,0.00000000,10018754.17139462,3857) AS tile, " +
				"ST_MakeEnvelope(-10018754.17139462,0.00000000,0.00000000,10018754.17139462,3857) AS layer",
		},
		// Resolution comes off the scheme's matrix, in metres, for both schemes.
		// WorldCRS84Quad z1 has 0.3515625 degrees per pixel, which is 39135.76 m
		// at 111319.49 m per degree -- the same pixel WebMercatorQuad reaches at
		// z2, which is what !WEB_MERCATOR_ZOOM! reports.
		"WorldCRS84Quad resolution is stated in metres": {
			sql:      "SELECT !PIXEL_WIDTH! w, !PIXEL_HEIGHT! h, !SCALE_DENOMINATOR! s, !ZOOM! z, !WEB_MERCATOR_ZOOM! m",
			layer:    Layer{srid: shigola.WebMercator},
			tile:     provider.NewTile(1, 2, 0, 64, shigola.WGS84),
			expected: "SELECT 39135.75848201 w, 39135.75848201 h, 139770566.00717899 s, 1 z, 2 m",
		},
		"WebMercatorQuad zoom is its own web mercator zoom": {
			sql:      "SELECT !PIXEL_WIDTH! w, !SCALE_DENOMINATOR! s, !ZOOM! z, !WEB_MERCATOR_ZOOM! m",
			layer:    Layer{srid: shigola.WebMercator},
			tile:     provider.NewTile(2, 1, 1, 64, shigola.WebMercator),
			expected: "SELECT 39135.75848201 w, 139770566.00717944 s, 2 z, 2 m",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestReplaceTokensNoNonFiniteEnvelope walks every WorldCRS84Quad tile of the
// shallow zooms against a mercator layer and checks that nothing non-finite
// reaches the SQL.
//
// A single expected string cannot cover this: the failure was one row of one
// zoom -- the row touching 90S -- and it took down the tile with a syntax error
// rather than a wrong number, because Go formats -Inf as a bare `-Inf` that
// PostgreSQL reads as an identifier.
func TestReplaceTokensNoNonFiniteEnvelope(t *testing.T) {
	grid, err := tms.Get(tms.WorldCRS84Quad)
	if err != nil {
		t.Fatalf("tms.Get(%v): %v", tms.WorldCRS84Quad, err)
	}

	layer := Layer{srid: shigola.WebMercator}

	for z := 0; z <= 5; z++ {
		cols, rows, err := grid.MatrixSize(z)
		if err != nil {
			t.Fatalf("MatrixSize(%v): %v", z, err)
		}

		for x := int64(0); x < cols; x++ {
			for y := int64(0); y < rows; y++ {
				for _, withBuffer := range []bool{false, true} {
					tile := provider.NewTileForGrid(slippy.Zoom(z), uint(x), uint(y), 64, grid)

					sql, err := replaceTokens("!BBOX! !TILE_BBOX!", &layer, tile, withBuffer)
					if err != nil {
						t.Fatalf("z=%v x=%v y=%v buffered=%v: %v", z, x, y, withBuffer, err)
					}

					for _, bad := range []string{"Inf", "NaN"} {
						if strings.Contains(sql, bad) {
							t.Errorf("z=%v x=%v y=%v buffered=%v: %v in %v", z, x, y, withBuffer, bad, sql)
						}
					}
				}
			}
		}
	}
}

// TestWebMercatorQuadZ0ScaleDenominator pins the constant replaceTokens derives
// !WEB_MERCATOR_ZOOM! from against the registry, which is where it is defined.
func TestWebMercatorQuadZ0ScaleDenominator(t *testing.T) {
	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		t.Fatalf("tms.Get(%v): %v", tms.WebMercatorQuad, err)
	}

	m, err := grid.Matrix(0)
	if err != nil {
		t.Fatalf("Matrix(0): %v", err)
	}

	if m.ScaleDenominator != webMercatorQuadZ0ScaleDenominator {
		t.Errorf("webMercatorQuadZ0ScaleDenominator = %v, want %v",
			webMercatorQuadZ0ScaleDenominator, m.ScaleDenominator)
	}
}
