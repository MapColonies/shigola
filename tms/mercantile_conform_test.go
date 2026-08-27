package tms

// Ported from morecantile/tests/test_mercantile_conform.py (MIT, Development
// Seed), which pins WebMercatorQuad against mapbox/mercantile — the de facto
// reference for the XYZ scheme every existing tegola client uses.
//
// mercantile is a Python library, so instead of calling it, the formulas below
// reimplement it directly from its source. That keeps the check independent: the
// expected values come from mercantile's closed-form spherical-Mercator
// arithmetic, while the port derives its answers from cellSize and pointOfOrigin
// read out of the bundled OGC definition. Two unrelated derivations agreeing to
// within a micrometre is the actual evidence of conformance.
//
// Getting this wrong is what a WebMercator regression looks like: existing tile
// URLs would silently shift, and so would every cache key under them.

import (
	"math"
	"testing"
)

// mercantileCE is mapbox/mercantile's CE constant, the circumference of the
// Earth at the equator in Web Mercator metres.
const mercantileCE = 2 * math.Pi * 6378137.0

// mercantileEpsilon is the nudge mercantile.tile applies before flooring, to
// keep a coordinate exactly on a tile edge inside the lower-indexed tile.
const mercantileEpsilon = 1e-14

// mercantileXYBounds reimplements mercantile.xy_bounds.
func mercantileXYBounds(x, y int64, zoom int) BoundingBox {
	tileSize := mercantileCE / math.Pow(2, float64(zoom))

	left := float64(x)*tileSize - mercantileCE/2
	right := left + tileSize
	top := mercantileCE/2 - float64(y)*tileSize
	bottom := top - tileSize

	return BoundingBox{Left: left, Bottom: bottom, Right: right, Top: top}
}

// mercantileTile reimplements mercantile.tile.
func mercantileTile(lng, lat float64, zoom int) Tile {
	x := lng/360.0 + 0.5
	sinlat := math.Sin(lat * math.Pi / 180.0)
	y := 0.5 - 0.25*math.Log((1.0+sinlat)/(1.0-sinlat))/math.Pi

	z2 := math.Pow(2, float64(zoom))

	var xtile, ytile int64

	switch {
	case x <= 0:
		xtile = 0
	case x >= 1:
		xtile = int64(z2 - 1)
	default:
		xtile = int64(math.Floor((x + mercantileEpsilon) * z2))
	}

	switch {
	case y <= 0:
		ytile = 0
	case y >= 1:
		ytile = int64(z2 - 1)
	default:
		ytile = int64(math.Floor((y + mercantileEpsilon) * z2))
	}

	return Tile{X: xtile, Y: ytile, Z: zoom}
}

// TestMercantileConformTile is morecantile's test_get_tile.
func TestMercantileConformTile(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	for zoom := 0; zoom < 20; zoom++ {
		want := mercantileTile(-10, 10, zoom)

		got, err := grid.TileFromLngLat(-10, 10, zoom, false, false)
		if err != nil {
			t.Fatalf("TileFromLngLat at zoom %d: %v", zoom, err)
		}

		if got != want {
			t.Errorf("zoom %d: port returned %v, mercantile %v", zoom, got, want)
		}
	}
}

// TestMercantileConformXYBounds is morecantile's test_bounds. It walks fixed
// indices rather than the random sample upstream uses, so a failure is
// reproducible.
func TestMercantileConformXYBounds(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	for zoom := 0; zoom < 20; zoom++ {
		cols, rows, err := grid.MatrixSize(zoom)
		if err != nil {
			t.Fatalf("MatrixSize(%d): %v", zoom, err)
		}

		// Corners, centre and a couple of off-centre indices per zoom.
		candidates := [][2]int64{
			{0, 0},
			{cols - 1, rows - 1},
			{cols / 2, rows / 2},
			{cols / 3, rows / 4},
			{cols - 1, 0},
			{0, rows - 1},
		}

		for _, c := range candidates {
			tile := Tile{X: c[0], Y: c[1], Z: zoom}

			got, err := grid.XYBounds(tile)
			if err != nil {
				t.Fatalf("XYBounds(%v): %v", tile, err)
			}

			want := mercantileXYBounds(tile.X, tile.Y, zoom)

			assertBoundsClose(t, got, [4]float64{want.Left, want.Bottom, want.Right, want.Top},
				"XYBounds"+tile.String())
		}
	}
}

// TestMercantileConformExtendedZoom is morecantile's test_extend_zoom: past the
// deepest published matrix, the synthesised matrices must still agree with
// mercantile.
func TestMercantileConformExtendedZoom(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	if grid.MaxZoom() != 24 {
		t.Fatalf("MaxZoom = %d, want 24 — this test is about zooms beyond it", grid.MaxZoom())
	}

	tests := map[string]Tile{
		"one level past the deepest matrix": {X: 1000, Y: 1000, Z: 25},
		"two levels past":                   {X: 2000, Y: 2000, Z: 26},
		"three levels past":                 {X: 2000, Y: 2000, Z: 27},
		"six levels past":                   {X: 2000, Y: 2000, Z: 30},
	}

	for name, tile := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := grid.XYBounds(tile)
			if err != nil {
				t.Fatalf("XYBounds(%v): %v", tile, err)
			}

			want := mercantileXYBounds(tile.X, tile.Y, tile.Z)

			assertBoundsClose(t, got, [4]float64{want.Left, want.Bottom, want.Right, want.Top},
				"synthesised XYBounds"+tile.String())
		})
	}
}

// TestMercantileConformMatrixSize checks the synthesised matrices keep the
// square 2^z shape mercantile assumes.
func TestMercantileConformMatrixSize(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	for zoom := 0; zoom <= 30; zoom++ {
		cols, rows, err := grid.MatrixSize(zoom)
		if err != nil {
			t.Fatalf("MatrixSize(%d): %v", zoom, err)
		}

		want := int64(1) << zoom
		if cols != want || rows != want {
			t.Errorf("MatrixSize(%d) = (%d, %d), want (%d, %d)", zoom, cols, rows, want, want)
		}
	}
}
