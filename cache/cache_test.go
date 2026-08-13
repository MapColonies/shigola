package cache_test

import (
	"reflect"
	"testing"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/tms"
)

func TestParseKey(t *testing.T) {
	testcases := []struct {
		input    string
		expected *cache.Key
	}{
		// A path names no grid, so ParseKey reads it as a WebMercatorQuad tile.
		{
			input: "/12/11/123",
			expected: &cache.Key{
				TileMatrixSetId: tms.WebMercatorQuad,
				Z:               12,
				X:               11,
				Y:               123,
			},
		},
		{
			input: "/osm/12/11/123",
			expected: &cache.Key{
				TileMatrixSetId: tms.WebMercatorQuad,
				Z:               12,
				X:               11,
				Y:               123,
				MapName:         "osm",
			},
		},
		{
			input: "/osm/buildings/12/11/123",
			expected: &cache.Key{
				TileMatrixSetId: tms.WebMercatorQuad,
				Z:               12,
				X:               11,
				Y:               123,
				MapName:         "osm",
				LayerName:       "buildings",
			},
		},
	}

	for i, tc := range testcases {
		output, err := cache.ParseKey(tc.input)
		if err != nil {
			t.Errorf("testcase (%v) failed. err: %v", i, err)
			continue
		}

		if !reflect.DeepEqual(tc.expected, output) {
			t.Errorf("testcase (%v) failed. expected (%+v) does not match output (%+v)", i, tc.expected, output)
			continue
		}
	}
}

func TestParseKeyForGridWorldCRS84Quad(t *testing.T) {
	grid, err := tms.Get(tms.WorldCRS84Quad)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// x=78212 is past 2^16-1, so this tile only parses against a grid whose
	// matrix is 2*2^z wide.
	key, err := cache.ParseKeyForGrid("/zoning/roads/16/78212/21154.pbf", grid)
	if err != nil {
		t.Fatalf("ParseKeyForGrid: %v", err)
	}

	expected := &cache.Key{
		TileMatrixSetId: tms.WorldCRS84Quad,
		MapName:         "zoning",
		LayerName:       "roads",
		Z:               16,
		X:               78212,
		Y:               21154,
	}
	if !reflect.DeepEqual(expected, key) {
		t.Fatalf("key, expected %+v got %+v", expected, key)
	}
}

// TestKeyStringPartitionsByGrid is the property ADR-0007 exists for: the same
// z/x/y in two grids must not name the same cache entry. Every backend builds
// its path or redis key from Key.String(), so checking it here covers all of
// them, including each tier of a layered cache.
func TestKeyStringPartitionsByGrid(t *testing.T) {
	webMercator := cache.Key{
		TileMatrixSetId: tms.WebMercatorQuad,
		MapName:         "osm",
		Z:               3, X: 5, Y: 2,
	}
	crs84 := webMercator
	crs84.TileMatrixSetId = tms.WorldCRS84Quad

	if webMercator.String() == crs84.String() {
		t.Fatalf("keys collide across grids: %v", webMercator.String())
	}

	// An unset grid means the grid tegola served before the field existed.
	var legacy cache.Key = webMercator
	legacy.TileMatrixSetId = ""

	if legacy.String() != webMercator.String() {
		t.Errorf("unset TileMatrixSetId = %q, want it to match %v (%q)",
			legacy.String(), tms.WebMercatorQuad, webMercator.String())
	}
}
