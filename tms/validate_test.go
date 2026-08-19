package tms

// ValidateTile is the fork's own check, not part of the morecantile port, so
// its expectations are derived from the grid definitions rather than from
// upstream's suite. Kept out of tilematrixset_test.go for that reason: that file
// is the port's correctness oracle and every value in it is morecantile's.

import (
	"errors"
	"testing"
)

func TestValidateTile(t *testing.T) {
	tests := map[string]struct {
		gridID   string
		z        int
		x, y     int64
		wantAxis string // "" means the tile is valid
	}{
		// WebMercatorQuad is the square case: 2^z by 2^z.
		"web mercator z1 origin":     {gridID: WebMercatorQuad, z: 1, x: 0, y: 0},
		"web mercator z1 last tile":  {gridID: WebMercatorQuad, z: 1, x: 1, y: 1},
		"web mercator z1 x past end": {gridID: WebMercatorQuad, z: 1, x: 2, y: 0, wantAxis: AxisX},
		"web mercator z1 y past end": {gridID: WebMercatorQuad, z: 1, x: 0, y: 2, wantAxis: AxisY},

		// WorldCRS84Quad is 2*2^z by 2^z, and is the whole reason the two axes
		// are bounded separately: at z0 it has two columns and one row, so a
		// square-pyramid bound of 2^z would reject the valid (1,0) and a bound
		// of 2*2^z would admit the invalid (0,1).
		"crs84 z0 second column is valid": {gridID: WorldCRS84Quad, z: 0, x: 1, y: 0},
		"crs84 z0 x past end":             {gridID: WorldCRS84Quad, z: 0, x: 2, y: 0, wantAxis: AxisX},
		"crs84 z0 only one row":           {gridID: WorldCRS84Quad, z: 0, x: 0, y: 1, wantAxis: AxisY},

		"negative x": {gridID: WebMercatorQuad, z: 3, x: -1, y: 0, wantAxis: AxisX},
		"negative y": {gridID: WebMercatorQuad, z: 3, x: 0, y: -1, wantAxis: AxisY},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			grid, err := Get(tc.gridID)
			if err != nil {
				t.Fatalf("Get(%v) = %v", tc.gridID, err)
			}

			err = grid.ValidateTile(tc.z, tc.x, tc.y)

			if tc.wantAxis == "" {
				if err != nil {
					t.Fatalf("ValidateTile(%v, %v, %v) = %v, want nil", tc.z, tc.x, tc.y, err)
				}

				return
			}

			var outside ErrTileOutsideMatrix
			if !errors.As(err, &outside) {
				t.Fatalf("ValidateTile(%v, %v, %v) = %v, want ErrTileOutsideMatrix", tc.z, tc.x, tc.y, err)
			}

			if outside.Axis != tc.wantAxis {
				t.Errorf("Axis = %q, want %q", outside.Axis, tc.wantAxis)
			}

			// Callers phrase their own message from these, so they have to be
			// the matrix actually checked against.
			if outside.GridID != tc.gridID {
				t.Errorf("GridID = %q, want %q", outside.GridID, tc.gridID)
			}

			if outside.Cols <= 0 || outside.Rows <= 0 {
				t.Errorf("Cols, Rows = %d, %d; want the matrix dimensions", outside.Cols, outside.Rows)
			}
		})
	}
}

// TestValidateTileReportsMissingMatrix: a zoom the grid has no matrix for is a
// different failure from a tile outside one, and callers tell them apart by
// type — the OGC handler answers "no such tile matrix", the native one "invalid
// Z value".
func TestValidateTileReportsMissingMatrix(t *testing.T) {
	grid, err := Get(WebMercatorQuad)
	if err != nil {
		t.Fatalf("Get(%v) = %v", WebMercatorQuad, err)
	}

	err = grid.ValidateTile(-1, 0, 0)
	if err == nil {
		t.Fatal("ValidateTile(-1, 0, 0) = nil, want an error")
	}

	var outside ErrTileOutsideMatrix
	if errors.As(err, &outside) {
		t.Errorf("ValidateTile(-1, 0, 0) = %v, want a matrix-lookup error, not ErrTileOutsideMatrix", err)
	}
}
