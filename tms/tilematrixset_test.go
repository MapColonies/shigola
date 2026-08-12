package tms

// Ported from morecantile/tests/test_morecantile.py and test_models.py (MIT,
// Development Seed). Every expected value here is morecantile's own, several of
// them inherited in turn from mapbox/mercantile's suite, so these tests check
// the port against upstream rather than against itself.

import (
	"errors"
	"math"
	"testing"
)

// tolerance is the comparison morecantile uses throughout its suite:
// round(a - b, 6) == 0.
const tolerance = 5e-7

func assertClose(t *testing.T, got, want float64, label string) {
	t.Helper()

	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %.10f, want %.10f (difference %g)", label, got, want, got-want)
	}
}

func assertBoundsClose(t *testing.T, got BoundingBox, want [4]float64, label string) {
	t.Helper()

	assertClose(t, got.Left, want[0], label+".left")
	assertClose(t, got.Bottom, want[1], label+".bottom")
	assertClose(t, got.Right, want[2], label+".right")
	assertClose(t, got.Top, want[3], label+".top")
}

// mustGrid fetches a grid through the registry, failing the test if it is not
// available in this build.
func mustGrid(t *testing.T, id string) *TileMatrixSet {
	t.Helper()

	grid, err := Get(id)
	if err != nil {
		t.Fatalf("Get(%q): %v", id, err)
	}

	return grid
}

// TestTMSProperties is morecantile's test_TMSproperties.
func TestTMSProperties(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	if got := grid.MetersPerUnit(); got != 1.0 {
		t.Errorf("MetersPerUnit = %v, want 1.0", got)
	}

	if got := grid.MinZoom(); got != 0 {
		t.Errorf("MinZoom = %d, want 0", got)
	}

	if got := grid.MaxZoom(); got != 24 {
		t.Errorf("MaxZoom = %d, want 24", got)
	}

	srid, err := grid.NativeSRID()
	if err != nil {
		t.Fatalf("NativeSRID: %v", err)
	}

	if srid != 3857 {
		t.Errorf("NativeSRID = %d, want 3857", srid)
	}
}

// TestXYBounds is morecantile's test_xy_bounds, from mercantile's suite.
func TestXYBounds(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	got, err := grid.XYBounds(Tile{X: 486, Y: 332, Z: 10})
	if err != nil {
		t.Fatalf("XYBounds: %v", err)
	}

	assertBoundsClose(t, got, [4]float64{
		-1017529.7205322663,
		7005300.768279833,
		-978393.962050256,
		7044436.526761846,
	}, "XYBounds(486,332,10)")
}

// TestBounds is morecantile's test_bounds/test_bbox.
func TestBounds(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	got, err := grid.Bounds(Tile{X: 486, Y: 332, Z: 10})
	if err != nil {
		t.Fatalf("Bounds: %v", err)
	}

	assertBoundsClose(t, got, [4]float64{
		-9.140625, 53.12040528310657, -8.7890625, 53.33087298301705,
	}, "Bounds(486,332,10)")
}

// TestUpperLeft covers morecantile's test_projul_tile and test_ul_tile.
func TestUpperLeft(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")
	tile := Tile{X: 486, Y: 332, Z: 10}

	xy, err := grid.UpperLeftXY(tile)
	if err != nil {
		t.Fatalf("UpperLeftXY: %v", err)
	}

	assertClose(t, xy.X, -1017529.7205322663, "UpperLeftXY.x")
	assertClose(t, xy.Y, 7044436.526761846, "UpperLeftXY.y")

	lnglat, err := grid.UpperLeft(tile)
	if err != nil {
		t.Fatalf("UpperLeft: %v", err)
	}

	assertClose(t, lnglat.X, -9.140625, "UpperLeft.lng")
	assertClose(t, lnglat.Y, 53.33087298301705, "UpperLeft.lat")
}

// TestTileFromXY is morecantile's test_projtile.
func TestTileFromXY(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	got, err := grid.TileFromXY(1000, 1000, 1, true)
	if err != nil {
		t.Fatalf("TileFromXY: %v", err)
	}

	if want := (Tile{X: 1, Y: 0, Z: 1}); got != want {
		t.Errorf("TileFromXY(1000,1000,1) = %v, want %v", got, want)
	}
}

// TestTileFromLngLat covers test_tile_coordinates, test_tile_not_truncated and
// test_tile_truncate.
func TestTileFromLngLat(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	tests := []struct {
		name     string
		lng, lat float64
		zoom     int
		truncate bool
		want     Tile
	}{
		{name: "north-west corner", lng: -179, lat: 85, zoom: 5, want: Tile{X: 0, Y: 0, Z: 5}},
		{name: "mercantile parity", lng: 20.6852, lat: 40.1222, zoom: 9, want: Tile{X: 285, Y: 193, Z: 9}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := grid.TileFromLngLat(tc.lng, tc.lat, tc.zoom, tc.truncate, false)
			if err != nil {
				t.Fatalf("TileFromLngLat: %v", err)
			}

			if got != tc.want {
				t.Errorf("TileFromLngLat(%v,%v,%d) = %v, want %v", tc.lng, tc.lat, tc.zoom, got, tc.want)
			}
		})
	}

	t.Run("truncated input matches clamped input", func(t *testing.T) {
		truncated, err := grid.TileFromLngLat(-181.0, 0.0, 9, true, false)
		if err != nil {
			t.Fatalf("TileFromLngLat truncated: %v", err)
		}

		clamped, err := grid.TileFromLngLat(-180.0, 0.0, 9, false, false)
		if err != nil {
			t.Fatalf("TileFromLngLat clamped: %v", err)
		}

		if truncated != clamped {
			t.Errorf("truncated %v != clamped %v", truncated, clamped)
		}
	})
}

// TestXY covers test_xy_tile, test_xy_null_island and test_xy_truncate.
func TestXY(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	t.Run("null island", func(t *testing.T) {
		got, err := grid.XY(0, 0, false)
		if err != nil {
			t.Fatalf("XY: %v", err)
		}

		assertClose(t, got.X, 0, "XY(0,0).x")
		assertClose(t, got.Y, 0, "XY(0,0).y")
	})

	t.Run("round trips the 486-332-10 corner", func(t *testing.T) {
		ul, err := grid.UpperLeft(Tile{X: 486, Y: 332, Z: 10})
		if err != nil {
			t.Fatalf("UpperLeft: %v", err)
		}

		got, err := grid.XY(ul.X, ul.Y, false)
		if err != nil {
			t.Fatalf("XY: %v", err)
		}

		assertClose(t, got.X, -1017529.7205322663, "XY.x")
		assertClose(t, got.Y, 7044436.526761846, "XY.y")
	})

	t.Run("truncates to the grid bounds", func(t *testing.T) {
		bbox, err := grid.BBox()
		if err != nil {
			t.Fatalf("BBox: %v", err)
		}

		truncated, err := grid.XY(-181.0, 0.0, true)
		if err != nil {
			t.Fatalf("XY truncated: %v", err)
		}

		atEdge, err := grid.XY(bbox.Left, 0.0, false)
		if err != nil {
			t.Fatalf("XY at edge: %v", err)
		}

		assertClose(t, truncated.X, atEdge.X, "truncated.x")
		assertClose(t, truncated.Y, atEdge.Y, "truncated.y")
	})
}

// TestLngLatXYRoundtrip is morecantile's test_lnglat_xy_roundtrip.
func TestLngLatXYRoundtrip(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	wantLng, wantLat := -105.0844, 40.5853

	xy, err := grid.XY(wantLng, wantLat, false)
	if err != nil {
		t.Fatalf("XY: %v", err)
	}

	got, err := grid.LngLat(xy.X, xy.Y, false)
	if err != nil {
		t.Fatalf("LngLat: %v", err)
	}

	assertClose(t, got.X, wantLng, "roundtrip lng")
	assertClose(t, got.Y, wantLat, "roundtrip lat")
}

// TestWebMercatorBBox pins the grid's full extent. The latitude limit is where
// clamping bugs surface: tegola's maths/webmercator.LatToY clamps at +/-89.5, so
// a transform routed through it would not produce this value.
func TestWebMercatorBBox(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	xy, err := grid.XYBBox()
	if err != nil {
		t.Fatalf("XYBBox: %v", err)
	}

	assertBoundsClose(t, xy, [4]float64{
		-20037508.342789244, -20037508.342789244,
		20037508.342789244, 20037508.342789244,
	}, "XYBBox")

	geo, err := grid.BBox()
	if err != nil {
		t.Fatalf("BBox: %v", err)
	}

	assertBoundsClose(t, geo, [4]float64{
		-180, -85.05112877980659, 180, 85.05112877980659,
	}, "BBox")
}

// TestQuadkey covers test_quadkey, test_quadkey_to_tile,
// test_empty_quadkey_to_tile and test_quadkey_failure.
func TestQuadkey(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	const expected = "0313102310"

	got, err := grid.Quadkey(Tile{X: 486, Y: 332, Z: 10})
	if err != nil {
		t.Fatalf("Quadkey: %v", err)
	}

	if got != expected {
		t.Errorf("Quadkey(486,332,10) = %q, want %q", got, expected)
	}

	tile, err := grid.QuadkeyToTile(expected)
	if err != nil {
		t.Fatalf("QuadkeyToTile: %v", err)
	}

	if want := (Tile{X: 486, Y: 332, Z: 10}); tile != want {
		t.Errorf("QuadkeyToTile(%q) = %v, want %v", expected, tile, want)
	}

	empty, err := grid.QuadkeyToTile("")
	if err != nil {
		t.Fatalf("QuadkeyToTile(\"\"): %v", err)
	}

	if want := (Tile{X: 0, Y: 0, Z: 0}); empty != want {
		t.Errorf("QuadkeyToTile(\"\") = %v, want %v", empty, want)
	}

	var qkErr QuadKeyError
	if _, err := grid.QuadkeyToTile("lolwut"); !errors.As(err, &qkErr) {
		t.Errorf("QuadkeyToTile(\"lolwut\") error = %v, want QuadKeyError", err)
	}
}

// TestQuadkeyUnsupported checks that a non-quadtree grid refuses quadkeys.
func TestQuadkeyUnsupported(t *testing.T) {
	grid := mustGrid(t, "WorldCRS84Quad")

	var unsupported NoQuadkeySupportError
	if _, err := grid.Quadkey(Tile{X: 0, Y: 0, Z: 1}); !errors.As(err, &unsupported) {
		t.Errorf("Quadkey error = %v, want NoQuadkeySupportError", err)
	}
}

// TestIsValid is morecantile's test_is_valid_tile.
func TestIsValid(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	tests := map[string]struct {
		tile Tile
		want bool
	}{
		"root tile":                {tile: Tile{X: 0, Y: 0, Z: 0}, want: true},
		"zoom 0 holds one tile":    {tile: Tile{X: 1, Y: 0, Z: 0}, want: false},
		"below MinZoom":            {tile: Tile{X: 0, Y: 0, Z: -1}, want: false},
		"at MaxZoom":               {tile: Tile{X: 0, Y: 0, Z: 24}, want: true},
		"past MaxZoom when strict": {tile: Tile{X: 0, Y: 0, Z: 25}, want: false},
		"negative column":          {tile: Tile{X: -1, Y: 0, Z: 1}, want: false},
		"negative row":             {tile: Tile{X: 0, Y: -1, Z: 1}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := grid.IsValid(tc.tile, true); got != tc.want {
				t.Errorf("IsValid(%v) = %v, want %v", tc.tile, got, tc.want)
			}
		})
	}
}

// TestIsValidOverzoom is morecantile's test_is_valid_overzoom.
func TestIsValidOverzoom(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")
	tile := Tile{X: 0, Y: 0, Z: 25}

	if !grid.IsValid(tile, false) {
		t.Error("IsValid(z25, strict=false) = false, want true")
	}

	if grid.IsValid(tile, true) {
		t.Error("IsValid(z25, strict=true) = true, want false")
	}

	// A variable-width grid cannot synthesise deeper matrices, so overzoom is
	// rejected even when not strict.
	gnosis, err := LoadGrid("GNOSISGlobalGrid")
	if err != nil {
		t.Fatalf("LoadGrid(GNOSISGlobalGrid): %v", err)
	}

	if !gnosis.IsValid(Tile{X: 0, Y: 0, Z: 28}, false) {
		t.Error("GNOSIS IsValid(z28, strict=false) = false, want true")
	}

	if gnosis.IsValid(Tile{X: 0, Y: 0, Z: 29}, false) {
		t.Error("GNOSIS IsValid(z29, strict=false) = true, want false")
	}

	if gnosis.IsValid(Tile{X: 0, Y: 0, Z: 29}, true) {
		t.Error("GNOSIS IsValid(z29, strict=true) = true, want false")
	}
}

// TestNeighbors covers test_neighbors, test_neighbors_invalid and
// test_root_neighbors_invalid.
func TestNeighbors(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	tests := []struct {
		name string
		tile Tile
		want int
	}{
		{name: "interior tile has eight", tile: Tile{X: 243, Y: 166, Z: 9}, want: 8},
		{name: "left edge loses three", tile: Tile{X: 0, Y: 166, Z: 9}, want: 5},
		{name: "root tile has none", tile: Tile{X: 0, Y: 0, Z: 0}, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := grid.Neighbors(tc.tile)
			if err != nil {
				t.Fatalf("Neighbors: %v", err)
			}

			if len(got) != tc.want {
				t.Fatalf("Neighbors(%v) returned %d tiles, want %d: %v", tc.tile, len(got), tc.want, got)
			}

			for _, n := range got {
				if n.Z != tc.tile.Z {
					t.Errorf("neighbor %v is not at zoom %d", n, tc.tile.Z)
				}

				if d := n.X - tc.tile.X; d < -1 || d > 1 {
					t.Errorf("neighbor %v is more than one column away", n)
				}

				if d := n.Y - tc.tile.Y; d < -1 || d > 1 {
					t.Errorf("neighbor %v is more than one row away", n)
				}
			}
		})
	}
}

// TestParent covers test_parent and test_parent_multi.
func TestParent(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	got, err := grid.Parent(Tile{X: 486, Y: 332, Z: 10}, -1)
	if err != nil {
		t.Fatalf("Parent: %v", err)
	}

	if len(got) != 1 || got[0] != (Tile{X: 243, Y: 166, Z: 9}) {
		t.Errorf("Parent(486,332,10) = %v, want [Tile(x=243, y=166, z=9)]", got)
	}

	twoUp, err := grid.Parent(Tile{X: 486, Y: 332, Z: 10}, 8)
	if err != nil {
		t.Fatalf("Parent to zoom 8: %v", err)
	}

	if len(twoUp) == 0 || twoUp[0] != (Tile{X: 121, Y: 83, Z: 8}) {
		t.Errorf("Parent(486,332,10, zoom=8) = %v, want first tile Tile(x=121, y=83, z=8)", twoUp)
	}

	var zoomErr InvalidZoomError
	if _, err := grid.Parent(Tile{X: 486, Y: 332, Z: 10}, 11); !errors.As(err, &zoomErr) {
		t.Errorf("Parent to a deeper zoom error = %v, want InvalidZoomError", err)
	}

	root, err := grid.Parent(Tile{X: 0, Y: 0, Z: 0}, -1)
	if err != nil {
		t.Fatalf("Parent of root: %v", err)
	}

	if len(root) != 0 {
		t.Errorf("Parent(0,0,0) = %v, want empty", root)
	}
}

// TestChildren covers test_children, test_children_multi and
// test_children_invalid_zoom.
func TestChildren(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	const x, y, z = 243, 166, 9

	children, err := grid.Children(Tile{X: x, Y: y, Z: z}, -1)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}

	if len(children) != 4 {
		t.Fatalf("Children returned %d tiles, want 4: %v", len(children), children)
	}

	for _, want := range []Tile{
		{X: 2 * x, Y: 2 * y, Z: z + 1},
		{X: 2*x + 1, Y: 2 * y, Z: z + 1},
		{X: 2*x + 1, Y: 2*y + 1, Z: z + 1},
		{X: 2 * x, Y: 2*y + 1, Z: z + 1},
	} {
		if !containsTile(children, want) {
			t.Errorf("Children is missing %v", want)
		}
	}

	deeper, err := grid.Children(Tile{X: x, Y: y, Z: z}, 11)
	if err != nil {
		t.Fatalf("Children to zoom 11: %v", err)
	}

	if len(deeper) != 16 {
		t.Fatalf("Children to zoom 11 returned %d tiles, want 16", len(deeper))
	}

	for _, want := range []Tile{
		{X: 972, Y: 664, Z: 11}, {X: 973, Y: 664, Z: 11},
		{X: 973, Y: 665, Z: 11}, {X: 972, Y: 665, Z: 11},
		{X: 974, Y: 664, Z: 11}, {X: 975, Y: 664, Z: 11},
		{X: 975, Y: 665, Z: 11}, {X: 974, Y: 665, Z: 11},
		{X: 974, Y: 666, Z: 11}, {X: 975, Y: 666, Z: 11},
		{X: 975, Y: 667, Z: 11}, {X: 974, Y: 667, Z: 11},
		{X: 972, Y: 666, Z: 11}, {X: 973, Y: 666, Z: 11},
		{X: 973, Y: 667, Z: 11}, {X: 972, Y: 667, Z: 11},
	} {
		if !containsTile(deeper, want) {
			t.Errorf("Children to zoom 11 is missing %v", want)
		}
	}

	var zoomErr InvalidZoomError
	if _, err := grid.Children(Tile{X: x, Y: y, Z: z}, 8); !errors.As(err, &zoomErr) {
		t.Errorf("Children to a shallower zoom error = %v, want InvalidZoomError", err)
	}
}

func containsTile(tiles []Tile, want Tile) bool {
	for _, tile := range tiles {
		if tile == want {
			return true
		}
	}

	return false
}

// TestTiles is morecantile's test_tiles, itself replicating mercantile's.
func TestTiles(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	t.Run("small box spans two tiles", func(t *testing.T) {
		got, err := grid.Tiles(-105, 39.99, -104.99, 40, []int{14}, false)
		if err != nil {
			t.Fatalf("Tiles: %v", err)
		}

		want := []Tile{{X: 3413, Y: 6202, Z: 14}, {X: 3413, Y: 6203, Z: 14}}
		if len(got) != len(want) {
			t.Fatalf("Tiles returned %d tiles, want %d: %v", len(got), len(want), got)
		}

		for _, w := range want {
			if !containsTile(got, w) {
				t.Errorf("Tiles is missing %v", w)
			}
		}
	})

	t.Run("truncated input matches clamped input", func(t *testing.T) {
		truncated, err := grid.Tiles(-181.0, 0.0, -170.0, 10.0, []int{2}, true)
		if err != nil {
			t.Fatalf("Tiles truncated: %v", err)
		}

		clamped, err := grid.Tiles(-180.0, 0.0, -170.0, 10.0, []int{2}, false)
		if err != nil {
			t.Fatalf("Tiles clamped: %v", err)
		}

		if len(truncated) != len(clamped) {
			t.Fatalf("truncated returned %d tiles, clamped %d", len(truncated), len(clamped))
		}

		for i := range truncated {
			if truncated[i] != clamped[i] {
				t.Errorf("tile %d: truncated %v != clamped %v", i, truncated[i], clamped[i])
			}
		}
	})

	t.Run("whole world at zoom 0 is one tile", func(t *testing.T) {
		for _, truncate := range []bool{false, true} {
			got, err := grid.Tiles(-180, -90, 180, 90, []int{0}, truncate)
			if err != nil {
				t.Fatalf("Tiles(truncate=%v): %v", truncate, err)
			}

			if len(got) != 1 || got[0] != (Tile{X: 0, Y: 0, Z: 0}) {
				t.Errorf("Tiles(truncate=%v) = %v, want [Tile(x=0, y=0, z=0)]", truncate, got)
			}
		}
	})

	// test_global_tiles_clamped: y is clamped into the matrix.
	t.Run("whole world at zoom 1 is four tiles", func(t *testing.T) {
		got, err := grid.Tiles(-180, -90, 180, 90, []int{1}, false)
		if err != nil {
			t.Fatalf("Tiles: %v", err)
		}

		if len(got) != 4 {
			t.Fatalf("Tiles returned %d tiles, want 4: %v", len(got), got)
		}

		minY, maxY := got[0].Y, got[0].Y
		for _, tile := range got {
			minY = min(minY, tile.Y)
			maxY = max(maxY, tile.Y)
		}

		if minY != 0 || maxY != 1 {
			t.Errorf("row range = %d..%d, want 0..1", minY, maxY)
		}
	})

	t.Run("antimeridian-crossing box is split", func(t *testing.T) {
		got, err := grid.Tiles(175.0, 5.0, -175.0, 10.0, []int{2}, false)
		if err != nil {
			t.Fatalf("Tiles: %v", err)
		}

		if len(got) != 2 {
			t.Errorf("Tiles across the antimeridian returned %d tiles, want 2: %v", len(got), got)
		}
	})

	// test_tiles_nan_bounds: NaN must fail rather than silently clamp to the
	// whole grid, which would generate every tile.
	t.Run("NaN bounds are rejected", func(t *testing.T) {
		if _, err := grid.Tiles(-105, math.NaN(), -104.99, 40, []int{14}, false); err == nil {
			t.Error("expected an error for NaN bounds, got nil")
		}
	})
}

// TestTilesRoundtrip is morecantile's test_tiles_roundtrip and
// test_tiles_roundtrip_children.
func TestTilesRoundtrip(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	for _, tile := range []Tile{
		{X: 3413, Y: 6202, Z: 14},
		{X: 486, Y: 332, Z: 10},
		{X: 10, Y: 10, Z: 10},
	} {
		bounds, err := grid.Bounds(tile)
		if err != nil {
			t.Fatalf("Bounds(%v): %v", tile, err)
		}

		got, err := grid.Tiles(bounds.Left, bounds.Bottom, bounds.Right, bounds.Top, []int{tile.Z}, false)
		if err != nil {
			t.Fatalf("Tiles: %v", err)
		}

		if len(got) != 1 || got[0] != tile {
			t.Errorf("Tiles(Bounds(%v)) = %v, want just the original tile", tile, got)
		}

		// One level deeper the same box must yield exactly that tile's children.
		children, err := grid.Tiles(bounds.Left, bounds.Bottom, bounds.Right, bounds.Top, []int{tile.Z + 1}, false)
		if err != nil {
			t.Fatalf("Tiles at z+1: %v", err)
		}

		if len(children) != 4 {
			t.Errorf("Tiles(Bounds(%v)) at z+1 returned %d tiles, want 4", tile, len(children))
		}
	}
}

// TestZoomForRes is morecantile's test_zoom_for_res.
func TestZoomForRes(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	tests := []struct {
		name     string
		res      float64
		strategy string
		want     int
	}{
		// The native resolution of zoom 7 is 1222.9924525628178 and of zoom 8
		// is 611.4962262814075.
		{name: "612 auto", res: 612.0, strategy: ZoomStrategyAuto, want: 8},
		{name: "612 lower", res: 612.0, strategy: ZoomStrategyLower, want: 7},
		{name: "612 upper", res: 612.0, strategy: ZoomStrategyUpper, want: 8},
		{name: "610 auto", res: 610.0, strategy: ZoomStrategyAuto, want: 8},
		// The native resolution of zoom 24 is 0.009330691929342784.
		{name: "finer than the deepest matrix", res: 0.0001, strategy: ZoomStrategyAuto, want: 24},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := grid.ZoomForRes(tc.res, -1, -1, tc.strategy)
			if err != nil {
				t.Fatalf("ZoomForRes: %v", err)
			}

			if got != tc.want {
				t.Errorf("ZoomForRes(%v, %q) = %d, want %d", tc.res, tc.strategy, got, tc.want)
			}
		})
	}

	t.Run("synthesised deeper level", func(t *testing.T) {
		// The theoretical resolution of zoom 25 is 0.004665345964671392.
		got, err := grid.ZoomForRes(0.0001, -1, 25, ZoomStrategyAuto)
		if err != nil {
			t.Fatalf("ZoomForRes: %v", err)
		}

		if got != 25 {
			t.Errorf("ZoomForRes(0.0001, maxZoom=25) = %d, want 25", got)
		}
	})
}

/* ----------------------------------------------- the 2:1 and inverted grids */

// TestWorldCRS84QuadMatrixSize pins the property that makes a TileMatrixSet id
// mandatory in the cache key (ADR-0007): CRS84Quad is twice as wide as it is
// tall at every zoom, so the same z/x/y denotes a different tile than in the
// square WebMercatorQuad.
func TestWorldCRS84QuadMatrixSize(t *testing.T) {
	grid := mustGrid(t, "WorldCRS84Quad")

	for z := 0; z <= 5; z++ {
		cols, rows, err := grid.MatrixSize(z)
		if err != nil {
			t.Fatalf("MatrixSize(%d): %v", z, err)
		}

		wantCols := int64(2) << z
		wantRows := int64(1) << z

		if cols != wantCols || rows != wantRows {
			t.Errorf("MatrixSize(%d) = (%d, %d), want (%d, %d)", z, cols, rows, wantCols, wantRows)
		}
	}
}

// TestWorldCRS84QuadBounds checks the plate-carree arithmetic, and that a
// geographic grid's own CRS and geographic bounds coincide.
func TestWorldCRS84QuadBounds(t *testing.T) {
	grid := mustGrid(t, "WorldCRS84Quad")

	xy, err := grid.XYBBox()
	if err != nil {
		t.Fatalf("XYBBox: %v", err)
	}

	assertBoundsClose(t, xy, [4]float64{-180, -90, 180, 90}, "XYBBox")

	// At zoom 0 the matrix is 2x1 and a tile is 256 * 0.703125 = 180 degrees on
	// both sides, so each tile covers half the longitude range and the whole
	// latitude range. Zoom 1 is 4x2, giving 90-degree tiles.
	tests := []struct {
		tile Tile
		want [4]float64
	}{
		{Tile{X: 0, Y: 0, Z: 0}, [4]float64{-180, -90, 0, 90}},
		{Tile{X: 1, Y: 0, Z: 0}, [4]float64{0, -90, 180, 90}},
		{Tile{X: 0, Y: 0, Z: 1}, [4]float64{-180, 0, -90, 90}},
		{Tile{X: 3, Y: 1, Z: 1}, [4]float64{90, -90, 180, 0}},
	}

	for _, tc := range tests {
		got, err := grid.XYBounds(tc.tile)
		if err != nil {
			t.Fatalf("XYBounds(%v): %v", tc.tile, err)
		}

		assertBoundsClose(t, got, tc.want, "XYBounds"+tc.tile.String())

		// The CRS is geographic, so geographic bounds must be identical.
		geo, err := grid.Bounds(tc.tile)
		if err != nil {
			t.Fatalf("Bounds(%v): %v", tc.tile, err)
		}

		assertBoundsClose(t, geo, tc.want, "Bounds"+tc.tile.String())
	}
}

// TestWGS1984QuadInvertedAxes checks the inverted-axis path end to end.
// WGS1984Quad declares orderedAxes ["Lat","Lon"] and states pointOfOrigin as
// [90, -180]; if the swap in matrixOrigin were missing, every extent would come
// out transposed.
func TestWGS1984QuadInvertedAxes(t *testing.T) {
	grid := mustGrid(t, "WGS1984Quad")

	if !grid.invertAxis {
		t.Fatal("WGS1984Quad should be detected as axis-inverted")
	}

	xy, err := grid.XYBBox()
	if err != nil {
		t.Fatalf("XYBBox: %v", err)
	}

	assertBoundsClose(t, xy, [4]float64{-180, -90, 180, 90}, "XYBBox")

	got, err := grid.XYBounds(Tile{X: 0, Y: 0, Z: 0})
	if err != nil {
		t.Fatalf("XYBounds: %v", err)
	}

	assertBoundsClose(t, got, [4]float64{-180, -90, 0, 90}, "XYBounds(0,0,0)")

	// WorldCRS84Quad differs only in axis order, so the two must agree on every
	// tile's extent.
	crs84 := mustGrid(t, "WorldCRS84Quad")

	for z := 0; z <= 3; z++ {
		cols, rows, err := grid.MatrixSize(z)
		if err != nil {
			t.Fatalf("MatrixSize: %v", err)
		}

		for x := int64(0); x < cols; x++ {
			for y := int64(0); y < rows; y++ {
				tile := Tile{X: x, Y: y, Z: z}

				a, err := grid.XYBounds(tile)
				if err != nil {
					t.Fatalf("WGS1984Quad XYBounds(%v): %v", tile, err)
				}

				b, err := crs84.XYBounds(tile)
				if err != nil {
					t.Fatalf("WorldCRS84Quad XYBounds(%v): %v", tile, err)
				}

				assertBoundsClose(t, a, [4]float64{b.Left, b.Bottom, b.Right, b.Top},
					"WGS1984Quad vs WorldCRS84Quad "+tile.String())
			}
		}
	}
}

// TestGridsDifferAtSameIndex is the concrete justification for ADR-0007: at the
// same z/x/y, two grids describe different ground. A cache keyed without a
// TileMatrixSet id would serve one for the other.
func TestGridsDifferAtSameIndex(t *testing.T) {
	webMercator := mustGrid(t, "WebMercatorQuad")
	crs84 := mustGrid(t, "WorldCRS84Quad")

	tile := Tile{X: 1, Y: 0, Z: 1}

	a, err := webMercator.Bounds(tile)
	if err != nil {
		t.Fatalf("WebMercatorQuad Bounds: %v", err)
	}

	b, err := crs84.Bounds(tile)
	if err != nil {
		t.Fatalf("WorldCRS84Quad Bounds: %v", err)
	}

	if math.Abs(a.Left-b.Left) < tolerance && math.Abs(a.Top-b.Top) < tolerance {
		t.Errorf("the two grids agree at %v (%v vs %v); they must not", tile, a, b)
	}
}

// TestInvertedLatLonGrid is morecantile's test_InvertedLatLonGrids. The grid is
// gated in this build, but its tile arithmetic needs no transform, so the golden
// value still applies — which is the point of keeping gated grids loadable.
func TestInvertedLatLonGrid(t *testing.T) {
	grid, err := LoadGrid("LINZAntarticaMapTilegrid")
	if err != nil {
		t.Fatalf("LoadGrid: %v", err)
	}

	got, err := grid.XYBBox()
	if err != nil {
		t.Fatalf("XYBBox: %v", err)
	}

	assertBoundsClose(t, got, [4]float64{
		-918457.73, -22441670.269999996, 28441670.269999996, 6918457.73,
	}, "LINZ XYBBox")
}

// TestGatedGridGeographicOpsFail records the boundary this build draws: a gated
// grid computes tile extents in its own CRS but cannot convert them to
// geographic coordinates.
func TestGatedGridGeographicOpsFail(t *testing.T) {
	grid, err := LoadGrid("UPSArcticWGS84Quad")
	if err != nil {
		t.Fatalf("LoadGrid: %v", err)
	}

	if grid.TransformAvailable() {
		t.Fatal("UPSArcticWGS84Quad should have no transform backend in this build")
	}

	if _, err := grid.XYBounds(Tile{X: 0, Y: 0, Z: 1}); err != nil {
		t.Errorf("XYBounds on a gated grid should still work, got %v", err)
	}

	if _, err := grid.Bounds(Tile{X: 0, Y: 0, Z: 1}); !errors.Is(err, ErrNoTransformBackend) {
		t.Errorf("Bounds error = %v, want ErrNoTransformBackend", err)
	}
}

// TestMatrixSynthesisRejectsShallowZoom covers the one deliberate divergence
// from morecantile, which loops forever on this input.
func TestMatrixSynthesisRejectsShallowZoom(t *testing.T) {
	def, raw, err := LoadDefinition("WebMercatorQuad")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	// Drop the shallowest levels so that MinZoom is 2.
	def.TileMatrices = def.TileMatrices[2:]

	grid, err := New(def, raw)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if grid.MinZoom() != 2 {
		t.Fatalf("MinZoom = %d, want 2", grid.MinZoom())
	}

	var zoomErr InvalidZoomError
	if _, err := grid.Matrix(0); !errors.As(err, &zoomErr) {
		t.Errorf("Matrix(0) error = %v, want InvalidZoomError", err)
	}
}

// TestTileExtentGeomForm checks the tegola-facing accessors line up with the
// ported bounds, in geom.Extent's (minx, miny, maxx, maxy) order.
func TestTileExtentGeomForm(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")
	tile := Tile{X: 486, Y: 332, Z: 10}

	bounds, err := grid.XYBounds(tile)
	if err != nil {
		t.Fatalf("XYBounds: %v", err)
	}

	extent, err := grid.TileExtent(tile)
	if err != nil {
		t.Fatalf("TileExtent: %v", err)
	}

	assertClose(t, extent.MinX(), bounds.Left, "TileExtent.MinX")
	assertClose(t, extent.MinY(), bounds.Bottom, "TileExtent.MinY")
	assertClose(t, extent.MaxX(), bounds.Right, "TileExtent.MaxX")
	assertClose(t, extent.MaxY(), bounds.Top, "TileExtent.MaxY")

	geo, err := grid.Bounds(tile)
	if err != nil {
		t.Fatalf("Bounds: %v", err)
	}

	geoExtent, err := grid.TileGeoExtent(tile)
	if err != nil {
		t.Fatalf("TileGeoExtent: %v", err)
	}

	assertClose(t, geoExtent.MinX(), geo.Left, "TileGeoExtent.MinX")
	assertClose(t, geoExtent.MaxY(), geo.Top, "TileGeoExtent.MaxY")
}
