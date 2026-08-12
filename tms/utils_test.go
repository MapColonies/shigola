package tms

// Ported from morecantile/tests/test_utils.py (MIT, Development Seed).

import (
	"math"
	"testing"
)

func TestMetersPerUnit(t *testing.T) {
	// The semi-major axis is held in a variable so that the expected value is
	// computed by runtime float64 arithmetic, in the same order as the
	// implementation. Written as an untyped constant expression Go would fold it
	// at arbitrary precision and land one ULP away — and it is the operation
	// order, matching morecantile's, that this port has to preserve.
	semiMajor := 6378137.0
	degreesToMetres := 2 * math.Pi * semiMajor / 360.0

	// morecantile's test_mpu also covers US-survey-foot, foot and Mars CRSs.
	// Those need PROJ to resolve an ellipsoid and axis unit, so they are out of
	// reach of this build's CRS table; the metre and degree cases below are the
	// ones every bundled grid actually uses.
	tests := map[string]struct {
		crs      CRS
		expected float64
	}{
		"EPSG:4326 is degrees": {
			crs:      CRS{String: "http://www.opengis.net/def/crs/EPSG/0/4326"},
			expected: degreesToMetres,
		},
		"OGC:CRS84 is degrees": {
			crs:      CRS{String: "http://www.opengis.net/def/crs/OGC/1.3/CRS84"},
			expected: degreesToMetres,
		},
		"EPSG:3857 is metres": {
			crs:      CRS{String: "http://www.opengis.net/def/crs/EPSG/0/3857"},
			expected: 1.0,
		},
		"EPSG:2193 is metres": {
			crs:      CRS{String: "urn:ogc:def:crs:EPSG::2193"},
			expected: 1.0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			nfo, err := tc.crs.info()
			if err != nil {
				t.Fatalf("resolving CRS info: %v", err)
			}

			got, err := metersPerUnit(nfo)
			if err != nil {
				t.Fatalf("metersPerUnit: %v", err)
			}

			if got != tc.expected {
				t.Errorf("metersPerUnit = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestMetersPerUnitUnsupportedCRS(t *testing.T) {
	// A CRS outside the table cannot be resolved without PROJ, and must say so
	// rather than guessing a unit.
	crs := CRS{String: "http://www.opengis.net/def/crs/EPSG/0/2276"}

	if _, err := crs.info(); err == nil {
		t.Fatal("expected an error for a CRS outside this build's table, got nil")
	}
}

func TestLonsContainAntimeridian(t *testing.T) {
	tests := []struct {
		lon1, lon2 float64
		expected   bool
	}{
		{-180, 180, false},
		{179, -179, true},
	}

	for _, tc := range tests {
		if got := lonsContainAntimeridian(tc.lon1, tc.lon2); got != tc.expected {
			t.Errorf("lonsContainAntimeridian(%v, %v) = %v, want %v",
				tc.lon1, tc.lon2, got, tc.expected)
		}
	}
}

func TestIsPowerOfTwo(t *testing.T) {
	for _, tc := range []struct {
		n        int64
		expected bool
	}{
		{8, true},
		{7, false},
		{1, true},
		{0, false},
	} {
		if got := isPowerOfTwo(tc.n); got != tc.expected {
			t.Errorf("isPowerOfTwo(%d) = %v, want %v", tc.n, got, tc.expected)
		}
	}
}

func TestTruncateCoordinates(t *testing.T) {
	bbox := BoundingBox{Left: -180, Bottom: -90, Right: 180, Top: 90}

	tests := []struct {
		lng, lat         float64
		wantLng, wantLat float64
	}{
		{-181, 0, -180, 0},
		{181, 0, 180, 0},
		{0, -91, 0, -90},
		{0, 91, 0, 90},
		{10, 20, 10, 20},
	}

	for _, tc := range tests {
		gotLng, gotLat := truncateCoordinates(tc.lng, tc.lat, bbox)
		if gotLng != tc.wantLng || gotLat != tc.wantLat {
			t.Errorf("truncateCoordinates(%v, %v) = (%v, %v), want (%v, %v)",
				tc.lng, tc.lat, gotLng, gotLat, tc.wantLng, tc.wantLat)
		}
	}
}

func TestPointInBBox(t *testing.T) {
	bbox := BoundingBox{Left: -10, Bottom: -10, Right: 10, Top: 10}

	for _, tc := range []struct {
		point    Coords
		expected bool
	}{
		{Coords{0, 0}, true},
		{Coords{-10, -10}, true},
		{Coords{10, 10}, true},
		{Coords{10.00001, 0}, false},
		{Coords{0, -10.00001}, false},
	} {
		if got := pointInBBox(tc.point, bbox, defaultPointInBBoxPrecision); got != tc.expected {
			t.Errorf("pointInBBox(%v) = %v, want %v", tc.point, got, tc.expected)
		}
	}
}

func TestBBoxToFeature(t *testing.T) {
	geometry := bboxToFeature(-10, -20, 30, 40)

	if geometry["type"] != "Polygon" {
		t.Errorf("geometry type = %v, want Polygon", geometry["type"])
	}

	ring := geometry["coordinates"].([][][]float64)[0]
	if len(ring) != 5 {
		t.Fatalf("ring has %d positions, want 5 (closed)", len(ring))
	}

	if ring[0][0] != ring[4][0] || ring[0][1] != ring[4][1] {
		t.Errorf("ring is not closed: first %v, last %v", ring[0], ring[4])
	}
}
