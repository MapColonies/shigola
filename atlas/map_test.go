package atlas_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/provider/test"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom/slippy"
)

func TestMapFilterLayersByZoom(t *testing.T) {
	testcases := []struct {
		atlasMap atlas.Map
		zoom     slippy.Zoom
		expected atlas.Map
	}{
		{
			atlasMap: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 2,
					},
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
			zoom: 5,
			expected: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
		},
		{
			atlasMap: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 2,
					},
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
			zoom: 2,
			expected: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 2,
					},
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
		},
		{
			atlasMap: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 0,
					},
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
			zoom: 2,
			expected: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
		},
		{
			atlasMap: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 0,
					},
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
			zoom: 0,
			expected: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 0,
					},
				},
			},
		},
	}

	for i, tc := range testcases {
		output := tc.atlasMap.FilterLayersByZoom(tc.zoom)

		if !reflect.DeepEqual(output, tc.expected) {
			t.Errorf("testcase (%v) failed. output \n\n%+v\n\n does not match expected \n\n%+v", i, output, tc.expected)
		}
	}
}

func TestMapFilterLayersByName(t *testing.T) {
	testcases := []struct {
		grid     atlas.Map
		name     string
		expected atlas.Map
	}{
		{
			grid: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 2,
					},
					{
						Name:    "layer2",
						MinZoom: 1,
						MaxZoom: 5,
					},
				},
			},
			name: "layer1",
			expected: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:    "layer1",
						MinZoom: 0,
						MaxZoom: 2,
					},
				},
			},
		},
		{
			grid: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name: "layer1",
					},
					{
						Name: "layer1roads",
					},
				},
			},
			name: "layer1roads",
			expected: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name: "layer1roads",
					},
				},
			},
		},
	}

	for i, tc := range testcases {
		output := tc.grid.FilterLayersByName(tc.name)

		if !reflect.DeepEqual(output, tc.expected) {
			t.Errorf("testcase (%v) failed. output \n\n%+v\n\n does not match expected \n\n%+v", i, output, tc.expected)
		}
	}
}

// mustGrid resolves a TileMatrixSet, failing the test if this build cannot
// serve it.
func mustGrid(t *testing.T, id string) *tms.TileMatrixSet {
	t.Helper()

	grid, err := tms.Get(id)
	if err != nil {
		t.Fatalf("tms.Get(%q): %v", id, err)
	}

	return grid
}

// TestEncodeWithoutMVTProvider pins what a map with nothing to ask now does.
//
// Until MAPCO-11491 such a map encoded anyway, by walking its layers through
// the Go-side path. That path is gone, so the only honest answer is an error --
// and it has to be an error rather than a nil dereference, because atlas.Map is
// exported and assembling one by hand is how tests and embedders make one.
func TestEncodeWithoutMVTProvider(t *testing.T) {
	m := atlas.NewWebMercatorMap("no-provider")

	_, err := m.Encode(context.Background(), mustGrid(t, tms.WebMercatorQuad), slippy.Tile{Z: 0, X: 0, Y: 0}, nil)
	if err == nil {
		t.Fatal("Encode with no mvt provider, expected an error got nil")
	}

	var missing atlas.ErrNoMVTProvider
	if !errors.As(err, &missing) {
		t.Fatalf("Encode err, expected ErrNoMVTProvider got %T (%v)", err, err)
	}
	if missing.Map != "no-provider" {
		t.Errorf("err names map %q, expected %q", missing.Map, "no-provider")
	}
}

// TestEncodeServesTheMVTProvidersBytes covers the one remaining tile-production
// path: whatever the provider returns is what the tile carries, gzipped and
// otherwise untouched.
//
// Content that depends on the ground a tile covers is checked against the
// PostGIS fixture in server/tilecontent, which is where that question moved
// when the Go-side encode path was deleted.
func TestEncodeServesTheMVTProvidersBytes(t *testing.T) {
	want := []byte("encoded by the provider")

	m := atlas.NewWebMercatorMap("mvt")
	m.SetMVTProvider("mvt_test", &test.TileProvider{MVTTile: want})

	out, err := m.Encode(context.Background(), mustGrid(t, tms.WebMercatorQuad), slippy.Tile{Z: 0, X: 0, Y: 0}, nil)
	if err != nil {
		t.Fatalf("Encode error = %v", err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("tile is not gzip: %v", err)
	}
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("reading tile: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("tile bytes = %q, want %q", got, want)
	}
}

// TestMapServesLayerCollections pins the reason the field is a pointer: a Map
// built as a literal, which is how most of this tree builds one, says nothing
// about the flag and must still serve its layer collections (MAPCO-11493).
func TestMapServesLayerCollections(t *testing.T) {
	type tcase struct {
		atlasMap atlas.Map
		expected bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := tc.atlasMap.ServesLayerCollections(); got != tc.expected {
				t.Errorf("ServesLayerCollections() = %v, want %v", got, tc.expected)
			}
		}
	}

	serve, decline := true, false

	tests := map[string]tcase{
		"a zero map":                {atlasMap: atlas.Map{}, expected: true},
		"the constructor's map":     {atlasMap: atlas.NewWebMercatorMap("osm"), expected: true},
		"explicitly serving":        {atlasMap: atlas.Map{ServeLayerCollections: &serve}, expected: true},
		"explicitly whole-map only": {atlasMap: atlas.Map{ServeLayerCollections: &decline}, expected: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
