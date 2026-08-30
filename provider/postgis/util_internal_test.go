package postgis

import (
	"testing"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/provider"
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
