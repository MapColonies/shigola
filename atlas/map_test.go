package atlas_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/golang/protobuf/proto"

	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/encoding/mvt"
	vectorTile "github.com/go-spatial/geom/encoding/mvt/vector_tile"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/internal/p"
	"github.com/go-spatial/tegola/provider"
	"github.com/go-spatial/tegola/provider/test"
	"github.com/go-spatial/tegola/provider/test/emptycollection"
)

type polygonProvider struct {
	geometry geom.Geometry
	srid     uint64
}

func (p polygonProvider) Layers() ([]provider.LayerInfo, error) { return nil, nil }

func (p polygonProvider) TileFeatures(ctx context.Context, layer string, t provider.Tile, queryParams provider.Params, fn func(f *provider.Feature) error) error {
	return fn(&provider.Feature{
		ID:       1,
		Geometry: p.geometry,
		SRID:     p.srid,
		Tags:     map[string]interface{}{},
	})
}

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

func TestEncodeWorldCRS84QuadClipsPolygonsBeforeMVTPrepare(t *testing.T) {
	largePolygon := geom.Polygon{{
		{30, 30},
		{40, 30},
		{40, 35},
		{30, 35},
		{30, 30},
	}}

	atlasMap := atlas.NewWebMercatorMap("crs84")
	atlasMap.TileSRID = tegola.WGS84
	atlasMap.TileBuffer = 64
	atlasMap.Layers = []atlas.Layer{{
		Name:              "polygons",
		ProviderLayerName: "polygons",
		MinZoom:           0,
		MaxZoom:           22,
		DontClean:         true,
		Provider: polygonProvider{
			geometry: largePolygon,
			srid:     tegola.WGS84,
		},
	}}

	out, err := atlasMap.Encode(context.Background(), slippy.Tile{Z: 16, X: 78212, Y: 21154}, nil)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	tile := decodeGzippedVectorTile(t, out)
	if len(tile.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(tile.Layers))
	}
	if len(tile.Layers[0].Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(tile.Layers[0].Features))
	}

	feature := tile.Layers[0].Features[0]
	geometry, err := mvt.DecodeGeometry(feature.GetType(), feature.GetGeometry())
	if err != nil {
		t.Fatalf("DecodeGeometry() error = %v", err)
	}
	assertGeometryWithinBufferedExtent(t, geometry, -64, 4096+64)
}

func TestEncodeWorldCRS84QuadCleansClippedPolygons(t *testing.T) {
	largePolygon := geom.Polygon{{
		{30, 30},
		{40, 30},
		{40, 35},
		{30, 35},
		{30, 30},
	}}

	atlasMap := atlas.NewWebMercatorMap("crs84")
	atlasMap.TileSRID = tegola.WGS84
	atlasMap.TileBuffer = 64
	atlasMap.Layers = []atlas.Layer{{
		Name:              "polygons",
		ProviderLayerName: "polygons",
		MinZoom:           0,
		MaxZoom:           22,
		Provider: polygonProvider{
			geometry: largePolygon,
			srid:     tegola.WGS84,
		},
	}}

	out, err := atlasMap.Encode(context.Background(), slippy.Tile{Z: 16, X: 78212, Y: 21154}, nil)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	tile := decodeGzippedVectorTile(t, out)
	if len(tile.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(tile.Layers))
	}
	if len(tile.Layers[0].Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(tile.Layers[0].Features))
	}

	feature := tile.Layers[0].Features[0]
	geometry, err := mvt.DecodeGeometry(feature.GetType(), feature.GetGeometry())
	if err != nil {
		t.Fatalf("DecodeGeometry() error = %v", err)
	}
	assertGeometryWithinBufferedExtent(t, geometry, -64, 4096+64)
}

func decodeGzippedVectorTile(t *testing.T, data []byte) vectorTile.Tile {
	t.Helper()

	var buf bytes.Buffer
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer r.Close()

	if _, err = io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}

	var tile vectorTile.Tile
	if err = proto.Unmarshal(buf.Bytes(), &tile); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}

	return tile
}

func assertGeometryWithinBufferedExtent(t *testing.T, geometry geom.Geometry, min, max float64) {
	t.Helper()

	checkPoint := func(pt geom.Point) {
		if pt.X() < min || pt.X() > max || pt.Y() < min || pt.Y() > max {
			t.Fatalf("decoded MVT coordinate outside buffered extent: %v", pt)
		}
	}

	switch g := geometry.(type) {
	case geom.Polygon:
		for _, ring := range g.LinearRings() {
			for _, pt := range ring {
				checkPoint(geom.Point(pt))
			}
		}
	case geom.MultiPolygon:
		for _, polygon := range g.Polygons() {
			for _, ring := range polygon {
				for _, pt := range ring {
					checkPoint(geom.Point(pt))
				}
			}
		}
	default:
		t.Fatalf("expected polygon geometry, got %T", geometry)
	}
}

func TestEncode(t *testing.T) {
	// create vars for the vector tile types so we can take their addresses
	// unknown := vectorTile.Tile_UNKNOWN
	// point := vectorTile.Tile_POINT
	// linestring := vectorTile.Tile_LINESTRING
	polygon := vectorTile.Tile_POLYGON

	type tcase struct {
		grid     atlas.Map
		tile     slippy.Tile
		expected vectorTile.Tile
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			out, err := tc.grid.Encode(context.Background(), tc.tile, nil)
			if err != nil {
				t.Errorf("err: %v", err)
				return
			}

			// decompress our output
			var buf bytes.Buffer
			r, err := gzip.NewReader(bytes.NewReader(out))
			if err != nil {
				t.Errorf("err: %v", err)
				return
			}

			_, err = io.Copy(&buf, r)
			if err != nil {
				t.Errorf("err: %v", err)
				return
			}

			var tile vectorTile.Tile

			if err = proto.Unmarshal(buf.Bytes(), &tile); err != nil {
				t.Errorf("error unmarshalling output: %v", err)
				return
			}

			// check the layer lengths match
			if len(tile.Layers) != len(tc.expected.Layers) {
				t.Errorf("expected (%d) layers, got (%d)", len(tc.expected.Layers), len(tile.Layers))
				return
			}

			for j, tileLayer := range tile.Layers {
				expectedLayer := tc.expected.Layers[j]

				if *tileLayer.Version != *expectedLayer.Version {
					t.Errorf("expected %v got %v", *tileLayer.Version, *expectedLayer.Version)
					return
				}

				if *tileLayer.Name != *expectedLayer.Name {
					t.Errorf("expected %v got %v", *tileLayer.Name, *expectedLayer.Name)
					return
				}

				// features check
				for k, tileLayerFeature := range tileLayer.Features {
					expectedTileLayerFeature := expectedLayer.Features[k]

					if *tileLayerFeature.Id != *expectedTileLayerFeature.Id {
						t.Errorf("expected %v got %v", *tileLayerFeature.Id, *expectedTileLayerFeature.Id)
						return
					}

					// the vector tile layer tags output is not always consistent since it's generated from a map.
					// because of that we're going to check everything but the tags values

					// if !reflect.DeepEqual(tileLayerFeature.Tags, expectedTileLayerFeature.Tags) {
					//  t.Errorf("expected %v got %v", tileLayerFeature.Tags, expectedTileLayerFeature.Tags)
					// 	return
					// }

					if *tileLayerFeature.Type != *expectedTileLayerFeature.Type {
						t.Errorf("expected %v got %v", *tileLayerFeature.Type, *expectedTileLayerFeature.Type)
						return
					}

					if !reflect.DeepEqual(tileLayerFeature.Geometry, expectedTileLayerFeature.Geometry) {
						t.Errorf("expected %v got %v", tileLayerFeature.Geometry, expectedTileLayerFeature.Geometry)
						return
					}
				}

				if len(tileLayer.Keys) != len(expectedLayer.Keys) {
					t.Errorf("layer keys length, expected %v got %v", len(expectedLayer.Keys), len(tileLayer.Keys))
					return
				}
				{
					var keysmaps = make(map[string]struct{})
					for _, k := range expectedLayer.Keys {
						keysmaps[k] = struct{}{}
					}
					var ferr bool
					for _, k := range tileLayer.Keys {
						if _, ok := keysmaps[k]; !ok {
							t.Errorf("did not find key, expected %v got nil", k)
							ferr = true
						}
					}
					if ferr {
						return
					}
				}

				if *tileLayer.Extent != *expectedLayer.Extent {
					t.Errorf("expected %v got %v", *tileLayer.Extent, *expectedLayer.Extent)
					return
				}

				if len(expectedLayer.Keys) != len(tileLayer.Keys) {
					t.Errorf("key len expected %v got %v", len(expectedLayer.Keys), len(tileLayer.Keys))
					return

				}

				var gotmap = make(map[string]interface{})
				var expmap = make(map[string]interface{})
				for i, k := range tileLayer.Keys {
					gotmap[k] = tileLayer.Values[i]
				}
				for i, k := range expectedLayer.Keys {
					expmap[k] = expectedLayer.Values[i]
				}

				if !reflect.DeepEqual(expmap, gotmap) {
					t.Errorf("constructed map expected %v got %v", expmap, gotmap)
				}
			}
		}
	}

	tests := map[string]tcase{
		"test_provider": {
			grid: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:     "layer1",
						MinZoom:  0,
						MaxZoom:  2,
						Provider: &test.TileProvider{},
						DefaultTags: map[string]interface{}{
							"foo": "bar",
						},
					},
					{
						Name:     "layer2",
						MinZoom:  1,
						MaxZoom:  5,
						Provider: &test.TileProvider{},
					},
				},
			},
			tile: slippy.Tile{Z: 2, X: 3, Y: 3},
			expected: vectorTile.Tile{
				Layers: []*vectorTile.Tile_Layer{
					{
						Version: p.Uint32(2),
						Name:    p.String("layer1"),
						Features: []*vectorTile.Tile_Feature{
							{
								Id:       p.Uint64(0),
								Tags:     []uint32{0, 0, 1, 1},
								Type:     &polygon,
								Geometry: []uint32{9, 0, 0, 26, 8192, 0, 0, 8192, 8191, 0, 15},
							},
						},
						Keys: []string{"type", "foo"},
						Values: []*vectorTile.Tile_Value{
							{
								StringValue: p.String("debug_buffer_outline"),
							},
							{
								StringValue: p.String("bar"),
							},
						},
						Extent: p.Uint32(vectorTile.Default_Tile_Layer_Extent),
					},
					{
						Version: p.Uint32(2),
						Name:    p.String("layer2"),
						Features: []*vectorTile.Tile_Feature{
							{
								Id:       p.Uint64(0),
								Tags:     []uint32{0, 0},
								Type:     &polygon,
								Geometry: []uint32{9, 0, 0, 26, 8192, 0, 0, 8192, 8191, 0, 15},
							},
						},
						Keys: []string{"type"},
						Values: []*vectorTile.Tile_Value{
							{
								StringValue: p.String("debug_buffer_outline"),
							},
						},
						Extent: p.Uint32(vectorTile.Default_Tile_Layer_Extent),
					},
				},
			},
		},
		"empty_collection": {
			grid: atlas.Map{
				Layers: []atlas.Layer{
					{
						Name:     "empty_geom_collection",
						MinZoom:  0,
						MaxZoom:  2,
						Provider: &emptycollection.TileProvider{},
					},
				},
			},
			tile: slippy.Tile{Z: 2, X: 3, Y: 3},
			expected: vectorTile.Tile{
				Layers: []*vectorTile.Tile_Layer{
					{
						Version:  p.Uint32(2),
						Name:     p.String("empty_geom_collection"),
						Features: []*vectorTile.Tile_Feature{},
						Keys:     []string{},
						Values:   []*vectorTile.Tile_Value{},
						Extent:   p.Uint32(vectorTile.Default_Tile_Layer_Extent),
					},
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
