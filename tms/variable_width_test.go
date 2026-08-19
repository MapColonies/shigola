package tms

// Ported from morecantile/tests/test_tms_variable_width.py (MIT, Development
// Seed).
//
// GNOSISGlobalGrid and CDB1GlobalGrid coalesce columns towards the poles, so
// several column indices alias to one tile. This build does not activate them
// (their coalesced columns do not fit tegola's tile pipeline), but their
// arithmetic is CRS-transform-free and the model must carry variable widths for
// the projected grids to be activatable later — so the upstream golden values
// are exercised here through LoadGrid.

import (
	"errors"
	"testing"
)

func mustLoadGrid(t *testing.T, id string) *TileMatrixSet {
	t.Helper()

	grid, err := LoadGrid(id)
	if err != nil {
		t.Fatalf("LoadGrid(%q): %v", id, err)
	}

	return grid
}

func mustXYBounds(t *testing.T, grid *TileMatrixSet, tile Tile) BoundingBox {
	t.Helper()

	bounds, err := grid.XYBounds(tile)
	if err != nil {
		t.Fatalf("XYBounds(%v): %v", tile, err)
	}

	return bounds
}

// TestVariableWidthMatrixNotSynthesised is morecantile's test_invalid_matrix:
// a variable-width grid cannot have deeper matrices derived from its scale
// ratio, because coalescence is not implied by the ratio.
func TestVariableWidthMatrixNotSynthesised(t *testing.T) {
	for _, tc := range []struct {
		id   string
		zoom int
	}{
		{"CDB1GlobalGrid", 22},
		{"GNOSISGlobalGrid", 29},
	} {
		t.Run(tc.id, func(t *testing.T) {
			grid := mustLoadGrid(t, tc.id)

			var zoomErr ErrInvalidZoom
			if _, err := grid.Matrix(tc.zoom); !errors.As(err, &zoomErr) {
				t.Errorf("Matrix(%d) error = %v, want ErrInvalidZoom", tc.zoom, err)
			}
		})
	}
}

// TestGNOSISBounds is the bounds half of morecantile's test_gnosisg.
func TestGNOSISBounds(t *testing.T) {
	grid := mustLoadGrid(t, "GNOSISGlobalGrid")

	for _, tc := range []struct {
		tile Tile
		want [4]float64
	}{
		{Tile{X: 0, Y: 0, Z: 0}, [4]float64{-180, 0, -90, 90}},
		{Tile{X: 1, Y: 1, Z: 0}, [4]float64{-90, -90, 0, 0}},
		{Tile{X: 0, Y: 0, Z: 1}, [4]float64{-180, 45, -90, 90}},
	} {
		got := mustXYBounds(t, grid, tc.tile)
		assertBoundsClose(t, got, tc.want, "XYBounds"+tc.tile.String())

		// The grid's CRS is geographic, so geographic bounds must match exactly.
		geo, err := grid.Bounds(tc.tile)
		if err != nil {
			t.Fatalf("Bounds(%v): %v", tc.tile, err)
		}

		assertBoundsClose(t, geo, tc.want, "Bounds"+tc.tile.String())
	}
}

// TestGNOSISColumnAliasing checks coalescence: in a coalesced row, adjacent
// column indices name the same tile, while in a non-coalesced row they do not.
func TestGNOSISColumnAliasing(t *testing.T) {
	grid := mustLoadGrid(t, "GNOSISGlobalGrid")

	sameBounds := func(a, b Tile) bool {
		return mustXYBounds(t, grid, a) == mustXYBounds(t, grid, b)
	}

	// Row 0 of zoom 1 coalesces pairs of columns.
	for _, pair := range [][2]Tile{
		{{X: 0, Y: 0, Z: 1}, {X: 1, Y: 0, Z: 1}},
		{{X: 2, Y: 0, Z: 1}, {X: 3, Y: 0, Z: 1}},
		{{X: 4, Y: 0, Z: 1}, {X: 5, Y: 0, Z: 1}},
		{{X: 6, Y: 0, Z: 1}, {X: 7, Y: 0, Z: 1}},
	} {
		if !sameBounds(pair[0], pair[1]) {
			t.Errorf("%v and %v should alias to the same tile", pair[0], pair[1])
		}
	}

	// Row 3 of zoom 1 is the southern coalesced row.
	for _, pair := range [][2]Tile{
		{{X: 0, Y: 3, Z: 1}, {X: 1, Y: 3, Z: 1}},
		{{X: 2, Y: 3, Z: 1}, {X: 3, Y: 3, Z: 1}},
		{{X: 4, Y: 3, Z: 1}, {X: 5, Y: 3, Z: 1}},
		{{X: 6, Y: 3, Z: 1}, {X: 7, Y: 3, Z: 1}},
	} {
		if !sameBounds(pair[0], pair[1]) {
			t.Errorf("%v and %v should alias to the same tile", pair[0], pair[1])
		}
	}

	// Rows 1 and 2 do not coalesce.
	for _, pair := range [][2]Tile{
		{{X: 0, Y: 1, Z: 1}, {X: 1, Y: 1, Z: 1}},
		{{X: 2, Y: 1, Z: 1}, {X: 3, Y: 1, Z: 1}},
	} {
		if sameBounds(pair[0], pair[1]) {
			t.Errorf("%v and %v should be distinct tiles", pair[0], pair[1])
		}
	}
}

// TestGNOSISTiles checks that Tiles emits one tile per coalesced group rather
// than one per nominal column.
func TestGNOSISTiles(t *testing.T) {
	grid := mustLoadGrid(t, "GNOSISGlobalGrid")

	zoom0, err := grid.Tiles(-180, -90, 180, 90, []int{0}, false)
	if err != nil {
		t.Fatalf("Tiles at zoom 0: %v", err)
	}

	if len(zoom0) != 8 {
		t.Errorf("Tiles at zoom 0 returned %d tiles, want 8", len(zoom0))
	}

	zoom1, err := grid.Tiles(-180, -90, 180, 90, []int{1}, false)
	if err != nil {
		t.Fatalf("Tiles at zoom 1: %v", err)
	}

	if len(zoom1) != 24 {
		t.Errorf("Tiles at zoom 1 returned %d tiles, want 24", len(zoom1))
	}

	// Tile(1,0,1) is an alias of Tile(0,0,1) and must not be emitted alongside it.
	if containsTile(zoom1, Tile{X: 1, Y: 0, Z: 1}) {
		t.Error("Tiles emitted the aliased tile Tile(x=1, y=0, z=1)")
	}
}

// TestGNOSISParentChildren checks that aliases are not double-counted when
// walking the pyramid.
func TestGNOSISParentChildren(t *testing.T) {
	grid := mustLoadGrid(t, "GNOSISGlobalGrid")

	for _, tile := range []Tile{
		{X: 0, Y: 0, Z: 1},
		{X: 0, Y: 0, Z: 2},
		{X: 0, Y: 0, Z: 3},
	} {
		parents, err := grid.Parent(tile, -1)
		if err != nil {
			t.Fatalf("Parent(%v): %v", tile, err)
		}

		if len(parents) != 1 {
			t.Errorf("Parent(%v) returned %d tiles, want 1: %v", tile, len(parents), parents)
		}
	}

	for _, tc := range []struct {
		tile Tile
		zoom int
		want int
	}{
		{Tile{X: 0, Y: 0, Z: 0}, 1, 3},
		{Tile{X: 0, Y: 0, Z: 0}, 2, 11},
		{Tile{X: 0, Y: 1, Z: 1}, 2, 4},
	} {
		children, err := grid.Children(tc.tile, tc.zoom)
		if err != nil {
			t.Fatalf("Children(%v, zoom=%d): %v", tc.tile, tc.zoom, err)
		}

		if len(children) != tc.want {
			t.Errorf("Children(%v, zoom=%d) returned %d tiles, want %d",
				tc.tile, tc.zoom, len(children), tc.want)
		}
	}
}

// TestGNOSISNeighbors ports morecantile's exact neighbour expectations, which
// pin how coalescence collapses adjacent columns.
func TestGNOSISNeighbors(t *testing.T) {
	grid := mustLoadGrid(t, "GNOSISGlobalGrid")

	tests := []struct {
		tile Tile
		want []Tile
	}{
		{
			tile: Tile{X: 0, Y: 0, Z: 1},
			want: []Tile{{X: 0, Y: 1, Z: 1}, {X: 1, Y: 1, Z: 1}, {X: 2, Y: 0, Z: 1}, {X: 2, Y: 1, Z: 1}},
		},
		{
			tile: Tile{X: 2, Y: 0, Z: 1},
			want: []Tile{
				{X: 0, Y: 0, Z: 1}, {X: 1, Y: 1, Z: 1}, {X: 2, Y: 1, Z: 1},
				{X: 3, Y: 1, Z: 1}, {X: 4, Y: 0, Z: 1}, {X: 4, Y: 1, Z: 1},
			},
		},
		{
			tile: Tile{X: 6, Y: 0, Z: 1},
			want: []Tile{{X: 4, Y: 0, Z: 1}, {X: 5, Y: 1, Z: 1}, {X: 6, Y: 1, Z: 1}, {X: 7, Y: 1, Z: 1}},
		},
		{
			tile: Tile{X: 0, Y: 1, Z: 1},
			want: []Tile{{X: 0, Y: 0, Z: 1}, {X: 0, Y: 2, Z: 1}, {X: 1, Y: 1, Z: 1}, {X: 1, Y: 2, Z: 1}},
		},
		{
			tile: Tile{X: 3, Y: 1, Z: 1},
			want: []Tile{
				{X: 2, Y: 0, Z: 1}, {X: 2, Y: 1, Z: 1}, {X: 2, Y: 2, Z: 1},
				{X: 3, Y: 2, Z: 1}, {X: 4, Y: 0, Z: 1}, {X: 4, Y: 1, Z: 1},
				{X: 4, Y: 2, Z: 1},
			},
		},
		{
			tile: Tile{X: 0, Y: 3, Z: 1},
			want: []Tile{{X: 0, Y: 2, Z: 1}, {X: 1, Y: 2, Z: 1}, {X: 2, Y: 2, Z: 1}, {X: 2, Y: 3, Z: 1}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.tile.String(), func(t *testing.T) {
			got, err := grid.Neighbors(tc.tile)
			if err != nil {
				t.Fatalf("Neighbors: %v", err)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("Neighbors(%v) = %v, want %v", tc.tile, got, tc.want)
			}

			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("Neighbors(%v)[%d] = %v, want %v", tc.tile, i, got[i], tc.want[i])
				}
			}
		})
	}

	// Aliased tiles must have identical neighbours.
	a, err := grid.Neighbors(Tile{X: 0, Y: 0, Z: 1})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}

	b, err := grid.Neighbors(Tile{X: 1, Y: 0, Z: 1})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}

	if len(a) != len(b) {
		t.Fatalf("aliased tiles have different neighbour counts: %v vs %v", a, b)
	}

	for i := range a {
		if a[i] != b[i] {
			t.Errorf("aliased neighbours differ at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestGNOSISTileFromLngLat checks that a point in a coalesced row resolves to the
// start of its run, unless coalescence is explicitly ignored.
func TestGNOSISTileFromLngLat(t *testing.T) {
	grid := mustLoadGrid(t, "GNOSISGlobalGrid")

	tests := []struct {
		lng, lat          float64
		ignoreCoalescence bool
		want              Tile
	}{
		{lng: -180, lat: 90, want: Tile{X: 0, Y: 0, Z: 2}},
		{lng: -150, lat: 90, want: Tile{X: 0, Y: 0, Z: 2}},
		{lng: -80, lat: 90, want: Tile{X: 4, Y: 0, Z: 2}},
		{lng: -180, lat: -90, want: Tile{X: 0, Y: 7, Z: 2}},
		{lng: -150, lat: -90, want: Tile{X: 0, Y: 7, Z: 2}},
		{lng: -80, lat: -90, want: Tile{X: 4, Y: 7, Z: 2}},
		// Ignoring coalescence returns the alias instead of the run's start.
		{lng: -150, lat: 90, ignoreCoalescence: true, want: Tile{X: 1, Y: 0, Z: 2}},
		{lng: 150, lat: -90, ignoreCoalescence: true, want: Tile{X: 14, Y: 7, Z: 2}},
	}

	for _, tc := range tests {
		got, err := grid.TileFromLngLat(tc.lng, tc.lat, 2, false, tc.ignoreCoalescence)
		if err != nil {
			t.Fatalf("TileFromLngLat(%v, %v): %v", tc.lng, tc.lat, err)
		}

		if got != tc.want {
			t.Errorf("TileFromLngLat(%v, %v, ignoreCoalescence=%v) = %v, want %v",
				tc.lng, tc.lat, tc.ignoreCoalescence, got, tc.want)
		}
	}
}
