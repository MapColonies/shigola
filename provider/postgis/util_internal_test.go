package postgis

import (
	"strings"
	"testing"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/provider"
	"github.com/go-spatial/geom"
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
			expected: "SELECT id, 76.43702829 as width, 76.43702829 as height, 272989.38673276 as scale_denom FROM foo WHERE geom && ST_MakeEnvelope(899816.69697310,6789748.34851564,919996.07244038,6809927.72398292,3857)",
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

// TestTransformPoint pins the reprojection replaceTokens applies to the two
// BBOX corners. The expected values were taken from basic.Transform, the
// generic geometry walk this replaced in MAPCO-11492: every case below is a
// path that function served, so a mismatch here is a behaviour change rather
// than a new opinion. Note that replaceTokens' own cases all use a tile whose
// SRID matches the layer's, so before this test the cross-SRID pairs had no
// coverage at all.
func TestTransformPoint(t *testing.T) {
	type tcase struct {
		fromSRID uint64
		toSRID   uint64
		pt       geom.Point
		expected geom.Point
		err      string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			got, err := transformPoint(tc.fromSRID, tc.toSRID, tc.pt)

			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Errorf("error, expected %v got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error, expected nil got %v", err)
				return
			}

			if got.X() != tc.expected.X() || got.Y() != tc.expected.Y() {
				t.Errorf("point,\n expected \n \t{%.14f,%.14f}\n got \n \t{%.14f,%.14f}",
					tc.expected.X(), tc.expected.Y(), got.X(), got.Y())
			}
		}
	}

	tests := map[string]tcase{
		"web mercator to itself is identity": {
			fromSRID: shigola.WebMercator,
			toSRID:   shigola.WebMercator,
			pt:       geom.Point{-10175297.20532266, -156543.03392804},
			expected: geom.Point{-10175297.20532266, -156543.03392804},
		},
		"wgs84 to itself is identity": {
			fromSRID: shigola.WGS84,
			toSRID:   shigola.WGS84,
			pt:       geom.Point{-2.8125, -92.8125},
			expected: geom.Point{-2.8125, -92.8125},
		},
		"web mercator to wgs84": {
			fromSRID: shigola.WebMercator,
			toSRID:   shigola.WGS84,
			pt:       geom.Point{899816.69697310, 6789748.34851564},
			expected: geom.Point{8.083190917968796, 51.94257177774757},
		},
		"wgs84 to web mercator": {
			fromSRID: shigola.WGS84,
			toSRID:   shigola.WebMercator,
			pt:       geom.Point{8.0831909179688, 51.94257177774757},
			expected: geom.Point{899816.6969731004, 6.789748348515639e+06},
		},
		"wgs84 to web mercator at the projection's limits": {
			fromSRID: shigola.WGS84,
			toSRID:   shigola.WebMercator,
			pt:       geom.Point{-180, -85.0511287798066},
			expected: geom.Point{-2.0037508342789244e+07, -2.0037508342789255e+07},
		},
		// A WorldCRS84Quad tile buffered at z=0 runs past the pole, and
		// webmercator.PLatToY answers a latitude it cannot project with 0
		// rather than NaN. Pinned because it is what the BBOX has always
		// carried for that tile, not because a y of 0 is meaningful.
		"wgs84 latitude beyond the mercator limit collapses to zero": {
			fromSRID: shigola.WGS84,
			toSRID:   shigola.WebMercator,
			pt:       geom.Point{-2.8125, -92.8125},
			expected: geom.Point{-313086.06785608194, 0},
		},
		"an srid neither side supports is refused": {
			fromSRID: 4269,
			toSRID:   shigola.WebMercator,
			pt:       geom.Point{1, 2},
			err:      "do not know how to convert from 4269 to 3857",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
