package cache_test

import (
	"reflect"
	"testing"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/tms"
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
				TileMatrixSetID: tms.WebMercatorQuad,
				Z:               12,
				X:               11,
				Y:               123,
			},
		},
		{
			input: "/osm/12/11/123",
			expected: &cache.Key{
				TileMatrixSetID: tms.WebMercatorQuad,
				Z:               12,
				X:               11,
				Y:               123,
				MapName:         "osm",
			},
		},
		{
			input: "/osm/buildings/12/11/123",
			expected: &cache.Key{
				TileMatrixSetID: tms.WebMercatorQuad,
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
		TileMatrixSetID: tms.WorldCRS84Quad,
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
		TileMatrixSetID: tms.WebMercatorQuad,
		MapName:         "osm",
		Z:               3, X: 5, Y: 2,
	}
	crs84 := webMercator
	crs84.TileMatrixSetID = tms.WorldCRS84Quad

	if webMercator.String() == crs84.String() {
		t.Fatalf("keys collide across grids: %v", webMercator.String())
	}

	// An unset grid means the grid tegola served before the field existed.
	var legacy cache.Key = webMercator
	legacy.TileMatrixSetID = ""

	if legacy.String() != webMercator.String() {
		t.Errorf("unset TileMatrixSetID = %q, want it to match %v (%q)",
			legacy.String(), tms.WebMercatorQuad, webMercator.String())
	}
}

// TestParseKeyForGridParsesRequestPaths pins what ParseKeyForGrid reads: a
// request path, with the grid supplied by the caller.
//
// It does not read back what Key.String writes, and this test says so, because
// the two forms cannot be told apart: a written key with no layer has the same
// number of segments as a path with one, so "WorldCRS84Quad/osm/0/1/0" reads
// just as well as map "WorldCRS84Quad", layer "osm". Anyone tempted to make the
// parser accept both should read this first.
func TestParseKeyForGridParsesRequestPaths(t *testing.T) {
	grid, err := tms.Get(tms.WorldCRS84Quad)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got, err := cache.ParseKeyForGrid("/osm/water/3/5/2", grid)
	if err != nil {
		t.Fatalf("ParseKeyForGrid: %v", err)
	}

	want := cache.Key{
		TileMatrixSetID: tms.WorldCRS84Quad,
		MapName:         "osm",
		LayerName:       "water",
		Z:               3, X: 5, Y: 2,
	}

	if *got != want {
		t.Errorf("ParseKeyForGrid = %+v, want %+v", *got, want)
	}

	// The written form is storage, not input. With a layer it has six segments
	// and is rejected outright; without one it would parse, reading the grid id
	// as the map name. Rejection is the better of the two, and neither is a
	// format to read back in.
	if _, err := cache.ParseKeyForGrid(want.String(), grid); err == nil {
		t.Errorf("ParseKeyForGrid now reads Key.String output; if that is intended, " +
			"resolve the ambiguity documented on ParseKeyForGrid first")
	}
}

// TestParseKeyForGridRejectsNilGrid: without a grid there is no matrix to check
// z/x/y against and no id to record, so this is a caller error, not a panic
// inside the cache.
func TestParseKeyForGridRejectsNilGrid(t *testing.T) {
	if _, err := cache.ParseKeyForGrid("/osm/3/5/2", nil); err == nil {
		t.Error("ParseKeyForGrid(nil grid) = nil error, want an error")
	}
}
