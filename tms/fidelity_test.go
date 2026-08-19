package tms

// Tests closing coverage gaps in live code paths, several of them ported from
// morecantile tests that were initially skipped over. Each one here exists
// because the behaviour it pins is reachable but was unverified — which is how
// the negative-zoom and Feature-crs defects survived the first pass.

import (
	"errors"
	"testing"
)

// TestNegativeZoomGrid guards CDB1GlobalGrid's zoom range.
//
// OGC types a tileMatrix id as ^-?[0-9]+$ and CDB1GlobalGrid really does define
// levels -10 through 21, so a negative zoom is a legitimate tile index. An
// earlier version of this package rejected negative zooms outright, which made
// every geometry operation on that grid fail.
func TestNegativeZoomGrid(t *testing.T) {
	grid := mustLoadGrid(t, "CDB1GlobalGrid")

	if grid.MinZoom() != -10 {
		t.Fatalf("MinZoom = %d, want -10", grid.MinZoom())
	}

	if grid.MaxZoom() != 21 {
		t.Fatalf("MaxZoom = %d, want 21", grid.MaxZoom())
	}

	t.Run("geometry works at negative zooms", func(t *testing.T) {
		for _, zoom := range []int{-10, -5, -1, 0} {
			tile := Tile{X: 0, Y: 0, Z: zoom}

			bounds, err := grid.XYBounds(tile)
			if err != nil {
				t.Errorf("XYBounds(%v): %v", tile, err)

				continue
			}

			if bounds.Right <= bounds.Left || bounds.Top <= bounds.Bottom {
				t.Errorf("XYBounds(%v) = %v, want a non-empty box", tile, bounds)
			}

			if _, err := grid.UpperLeftXY(tile); err != nil {
				t.Errorf("UpperLeftXY(%v): %v", tile, err)
			}
		}
	})

	t.Run("negative zooms are valid tiles", func(t *testing.T) {
		if !grid.IsValid(Tile{X: 0, Y: 0, Z: -1}, true) {
			t.Error("IsValid at zoom -1 = false, want true")
		}

		if !grid.IsValid(Tile{X: 0, Y: 0, Z: -10}, true) {
			t.Error("IsValid at zoom -10 = false, want true")
		}

		if grid.IsValid(Tile{X: 0, Y: 0, Z: -11}, true) {
			t.Error("IsValid at zoom -11 = true, want false (below MinZoom)")
		}
	})

	t.Run("the whole grid covers the world", func(t *testing.T) {
		bbox, err := grid.XYBBox()
		if err != nil {
			t.Fatalf("XYBBox: %v", err)
		}

		assertBoundsClose(t, bbox, [4]float64{-180, -90, 180, 90}, "CDB1 XYBBox")
	})
}

// TestMatrixMissingFromRange checks that a zoom absent from the middle of a
// grid's range is an error rather than silently answering with the deepest
// matrix, which would report the wrong resolution.
func TestMatrixMissingFromRange(t *testing.T) {
	def, raw, err := LoadDefinition("WebMercatorQuad")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	// Punch a hole at zoom 5.
	var kept []TileMatrix

	for _, m := range def.TileMatrices {
		if m.ID != "5" {
			kept = append(kept, m)
		}
	}

	def.TileMatrices = kept

	grid, err := New(def, raw)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var zoomErr ErrInvalidZoom
	if _, err := grid.Matrix(5); !errors.As(err, &zoomErr) {
		t.Errorf("Matrix(5) error = %v, want ErrInvalidZoom", err)
	}

	// Note that this holed grid cannot synthesise deeper levels either, and for a
	// separate reason: with zoom 5 removed the ratio from zoom 4 to zoom 6 is
	// 0.25 where every other step is 0.5, so the grid no longer has one scale
	// ratio to extend. Synthesis past the deepest matrix is covered on the intact
	// grid instead.
	intact := mustGrid(t, "WebMercatorQuad")
	if _, err := intact.Matrix(intact.MaxZoom() + 1); err != nil {
		t.Errorf("Matrix past MaxZoom should synthesise on an intact grid, got %v", err)
	}
}

// TestZoomForResStrategyCaseInsensitive covers morecantile lower-casing the
// strategy before comparing it.
func TestZoomForResStrategyCaseInsensitive(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	for _, strategy := range []string{"LOWER", "Lower", "lower"} {
		got, err := grid.ZoomForRes(612.0, -1, -1, strategy)
		if err != nil {
			t.Fatalf("ZoomForRes(%q): %v", strategy, err)
		}

		if got != 7 {
			t.Errorf("ZoomForRes(612, %q) = %d, want 7", strategy, got)
		}
	}

	for _, strategy := range []string{"AUTO", "Auto", "auto", ""} {
		got, err := grid.ZoomForRes(612.0, -1, -1, strategy)
		if err != nil {
			t.Fatalf("ZoomForRes(%q): %v", strategy, err)
		}

		if got != 8 {
			t.Errorf("ZoomForRes(612, %q) = %d, want 8", strategy, got)
		}
	}

	if _, err := grid.ZoomForRes(612.0, -1, -1, "sideways"); err == nil {
		t.Error("expected an error for an unknown strategy, got nil")
	}
}

// TestBottomLeftOrigin covers the cornerOfOrigin == bottomLeft branch, which no
// bundled grid exercises — all 13 are topLeft, so this code path had no coverage
// at all.
//
// Ported in spirit from morecantile's test_bottomleft_origin and
// test_topLeft_BottomLeft_bounds_equal_bounds, which build the bottomLeft grid
// with TileMatrixSet.custom. This port has no custom constructor, so the same
// grid is derived from the bundled WebMercatorQuad document instead.
func TestBottomLeftOrigin(t *testing.T) {
	topLeft := mustGrid(t, "WebMercatorQuad")

	def, raw, err := LoadDefinition("WebMercatorQuad")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	const edge = 20037508.342789244

	def.ID = "WebMercatorQuadBottomLeft"
	for i := range def.TileMatrices {
		def.TileMatrices[i].CornerOfOrigin = CornerBottomLeft
		def.TileMatrices[i].PointOfOrigin = [2]float64{-edge, -edge}
	}

	bottomLeft, err := New(def, raw)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Both grids must describe the same world.
	topBBox, err := topLeft.XYBBox()
	if err != nil {
		t.Fatalf("topLeft XYBBox: %v", err)
	}

	bottomBBox, err := bottomLeft.XYBBox()
	if err != nil {
		t.Fatalf("bottomLeft XYBBox: %v", err)
	}

	assertBoundsClose(t, bottomBBox,
		[4]float64{topBBox.Left, topBBox.Bottom, topBBox.Right, topBBox.Top},
		"bottomLeft XYBBox")

	// Flipping the origin corner flips the row numbering, so row y from the top
	// is row rows-1-y from the bottom and the two must agree tile for tile.
	for zoom := 0; zoom <= 4; zoom++ {
		cols, rows, err := topLeft.MatrixSize(zoom)
		if err != nil {
			t.Fatalf("MatrixSize(%d): %v", zoom, err)
		}

		for x := int64(0); x < cols; x++ {
			for y := int64(0); y < rows; y++ {
				fromTop, err := topLeft.XYBounds(Tile{X: x, Y: y, Z: zoom})
				if err != nil {
					t.Fatalf("topLeft XYBounds: %v", err)
				}

				fromBottom, err := bottomLeft.XYBounds(Tile{X: x, Y: rows - 1 - y, Z: zoom})
				if err != nil {
					t.Fatalf("bottomLeft XYBounds: %v", err)
				}

				assertBoundsClose(t, fromBottom,
					[4]float64{fromTop.Left, fromTop.Bottom, fromTop.Right, fromTop.Top},
					"bottomLeft row flip at z"+string(rune('0'+zoom)))
			}
		}
	}
}

// TestFeature is morecantile's test_feature.
func TestFeature(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")
	tile := Tile{X: 1, Y: 0, Z: 1}

	t.Run("defaults", func(t *testing.T) {
		feat, err := grid.Feature(tile, FeatureOptions{})
		if err != nil {
			t.Fatalf("Feature: %v", err)
		}

		if feat["bbox"] == nil || feat["id"] == nil || feat["geometry"] == nil {
			t.Fatalf("Feature is missing a required member: %v", feat)
		}

		props, ok := feat["properties"].(map[string]any)
		if !ok {
			t.Fatalf("properties is %T, want a map", feat["properties"])
		}

		if len(props) != 3 {
			t.Errorf("properties has %d keys, want 3: %v", len(props), props)
		}

		if _, ok := feat["crs"]; ok {
			t.Error("a geographic feature must not name a crs; GeoJSON is WGS 84")
		}
	})

	t.Run("with options", func(t *testing.T) {
		feat, err := grid.Feature(tile, FeatureOptions{
			Buffer:    -10,
			Precision: 4,
			FID:       "1",
			Props:     map[string]any{"some": "thing"},
		})
		if err != nil {
			t.Fatalf("Feature: %v", err)
		}

		if feat["id"] != "1" {
			t.Errorf("id = %v, want \"1\"", feat["id"])
		}

		props := feat["properties"].(map[string]any)
		if len(props) != 4 {
			t.Errorf("properties has %d keys, want 4: %v", len(props), props)
		}
	})

	t.Run("projected names its crs", func(t *testing.T) {
		feat, err := grid.Feature(tile, FeatureOptions{Projected: true})
		if err != nil {
			t.Fatalf("Feature: %v", err)
		}

		if feat["crs"] == nil {
			t.Error("a projected feature should name its crs")
		}
	})

	// A grid that is already EPSG:4326 emits no crs member even when projected,
	// because GeoJSON is defined in WGS 84.
	t.Run("projected EPSG:4326 omits its crs", func(t *testing.T) {
		wgs84 := mustGrid(t, "WGS1984Quad")

		feat, err := wgs84.Feature(Tile{X: 0, Y: 0, Z: 1}, FeatureOptions{Projected: true})
		if err != nil {
			t.Fatalf("Feature: %v", err)
		}

		if _, ok := feat["crs"]; ok {
			t.Error("an EPSG:4326 grid must not name a crs; it is already WGS 84")
		}
	})
}

// TestLngLatWrapsOutOfRangeLongitude is morecantile's test_lnglat_gdal3. PROJ
// normalises longitude by whole turns, so an x far west of the grid comes back
// wrapped rather than as an out-of-range degree count.
func TestLngLatWrapsOutOfRangeLongitude(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	got, err := grid.LngLat(-28366731.739810849, -1655181.9927159143, true)
	if err != nil {
		t.Fatalf("LngLat: %v", err)
	}

	assertClose(t, roundFloat(got.X, 5), 105.17731, "wrapped lng")
	assertClose(t, roundFloat(got.Y, 5), -14.70462, "lat")

	// An in-range coordinate is untouched, so the grid's eastern edge stays +180
	// rather than folding to -180.
	bbox, err := grid.BBox()
	if err != nil {
		t.Fatalf("BBox: %v", err)
	}

	assertClose(t, bbox.Right, 180, "BBox.right")
	assertClose(t, bbox.Left, -180, "BBox.left")
}

// TestNZTM2000QuadBounds is morecantile's test_nztm_quad_is_quad. NZTM2000Quad is
// projected, so it is gated in this build, but its tile arithmetic needs no
// transform and it declares inverted axes — making it a check on axis inversion
// for a projected grid.
func TestNZTM2000QuadBounds(t *testing.T) {
	grid := mustLoadGrid(t, "NZTM2000Quad")

	if !grid.invertAxis {
		t.Fatal("NZTM2000Quad should be axis-inverted")
	}

	got, err := grid.XYBounds(Tile{X: 0, Y: 0, Z: 0})
	if err != nil {
		t.Fatalf("XYBounds: %v", err)
	}

	// morecantile compares these to four decimal places.
	for _, c := range []struct {
		label     string
		got, want float64
	}{
		{"left", got.Left, -3260586.7284},
		{"bottom", got.Bottom, 419435.9938},
		{"right", got.Right, 6758167.443},
		{"top", got.Top, 10438190.1652},
	} {
		if roundFloat(c.got-c.want, 4) != 0 {
			t.Errorf("XYBounds.%s = %.4f, want %.4f", c.label, c.got, c.want)
		}
	}
}

// TestNZTM2000QuadScales is morecantile's test_nztm_quad_scales: NZTM2000Quad
// reuses WebMercatorQuad's scale denominators, offset by two zoom levels. It
// exercises Matrix synthesis on both grids at once.
func TestNZTM2000QuadScales(t *testing.T) {
	nztm := mustLoadGrid(t, "NZTM2000Quad")
	google := mustGrid(t, "WebMercatorQuad")

	for z := 2; z <= nztm.MaxZoom()+1; z++ {
		want, err := google.ScaleDenominator(z)
		if err != nil {
			t.Fatalf("WebMercatorQuad ScaleDenominator(%d): %v", z, err)
		}

		got, err := nztm.ScaleDenominator(z - 2)
		if err != nil {
			t.Fatalf("NZTM2000Quad ScaleDenominator(%d): %v", z-2, err)
		}

		if roundFloat(got-want, 4) != 0 {
			t.Errorf("NZTM2000Quad scale at z%d = %.4f, WebMercatorQuad at z%d = %.4f",
				z-2, got, z, want)
		}
	}
}
