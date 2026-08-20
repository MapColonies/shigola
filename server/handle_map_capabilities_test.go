package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/mapbox/tilejson"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tms"
)

func TestHandleMapCapabilities(t *testing.T) {
	type tcase struct {
		handler   http.Handler
		hostName  *url.URL
		port      string
		uri       string
		reqMethod string
		expected  tilejson.TileJSON
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			var err error

			server.HostName = tc.hostName
			server.Port = tc.port

			// setup a new router. this handles parsing our URL wildcards (i.e. :map_name, :z, :x, :y)
			router := server.NewRouter(nil)

			r, err := http.NewRequest(tc.reqMethod, tc.uri, nil)
			if err != nil {
				t.Fatal(err)
				return
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)

			if w.Code != http.StatusOK {
				t.Errorf("handler returned wrong status code: got (%v) expected (%v)", w.Code, http.StatusOK)
				return
			}

			bytes, err := io.ReadAll(w.Body)
			if err != nil {
				t.Errorf("err reading response body: %v", err)
				return
			}

			var tileJSON tilejson.TileJSON
			// read the response body
			if err := json.Unmarshal(bytes, &tileJSON); err != nil {
				t.Errorf("unable to unmarshal JSON response body: %v", err)
				return
			}

			if !reflect.DeepEqual(tc.expected, tileJSON) {
				t.Errorf("response body and expected do not match \n%+v\n%+v", tc.expected, tileJSON)
				return
			}
		}
	}

	tests := map[string]tcase{
		"happy path": {
			handler:   server.HandleCapabilities{},
			hostName:  nil,
			uri:       "http://localhost:8080/capabilities/test-map.json",
			reqMethod: http.MethodGet,
			expected: tilejson.TileJSON{
				Attribution:     &testMapAttribution,
				Bounds:          [4]float64{-180.0, -85.0511, 180.0, 85.0511},
				Center:          testMapCenter,
				CRS:             atlas.DefaultTileGrid().CRSURI(),
				Format:          server.TileURLFileFormat,
				MinZoom:         testLayer1.MinZoom,
				MaxZoom:         testLayer3.MaxZoom, //	the max zoom for the test group is in layer 3
				Name:            &testMapName,
				Description:     nil,
				Scheme:          tilejson.SchemeXYZ,
				TileMatrixSetID: atlas.DefaultTileGrid().ID(),
				TileJSON:        tilejson.Version,
				Tiles: []string{
					server.TileURLTemplate{
						Scheme:  "http",
						Host:    "localhost:8080",
						MapName: "test-map",
					}.String(),
				},
				Grids:    []string{},
				Data:     []string{},
				Version:  "1.0.0",
				Template: nil,
				Legend:   nil,
				VectorLayers: []tilejson.VectorLayer{
					{
						Version:      2,
						Extent:       4096,
						ID:           testLayer1.MVTName(),
						Name:         testLayer1.MVTName(),
						GeometryType: tilejson.GeomTypePoint,
						MinZoom:      testLayer1.MinZoom,
						MaxZoom:      testLayer3.MaxZoom, // layer 1 and layer 3 share a name in our test so the zoom range includes the entire zoom range
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "localhost:8080",
								MapName:   "test-map",
								LayerName: testLayer1.MVTName(),
							}.String(),
						},
					},
					{
						Version:      2,
						Extent:       4096,
						ID:           testLayer2.MVTName(),
						Name:         testLayer2.MVTName(),
						GeometryType: tilejson.GeomTypeLine,
						MinZoom:      testLayer2.MinZoom,
						MaxZoom:      testLayer2.MaxZoom,
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "localhost:8080",
								MapName:   "test-map",
								LayerName: testLayer2.MVTName(),
							}.String(),
						},
					},
				},
			},
		},
		"with hostname": {
			handler: server.HandleCapabilities{},
			hostName: &url.URL{
				Host: "cdn.tegola.io",
			},
			port:      "none",
			uri:       "http://localhost:8080/capabilities/test-map.json?debug=true",
			reqMethod: http.MethodGet,
			expected: tilejson.TileJSON{
				Attribution:     &testMapAttribution,
				Bounds:          [4]float64{-180.0, -85.0511, 180.0, 85.0511},
				Center:          testMapCenter,
				CRS:             atlas.DefaultTileGrid().CRSURI(),
				Format:          server.TileURLFileFormat,
				MinZoom:         0,
				MaxZoom:         atlas.MaxZoom,
				Name:            &testMapName,
				Description:     nil,
				Scheme:          tilejson.SchemeXYZ,
				TileMatrixSetID: atlas.DefaultTileGrid().ID(),
				TileJSON:        tilejson.Version,
				Tiles: []string{
					server.TileURLTemplate{
						Scheme:  "http",
						Host:    "cdn.tegola.io",
						MapName: "test-map",
						Query: url.Values{
							server.QueryKeyDebug: []string{
								"true",
							},
						},
					}.String(),
				},
				Grids:    []string{},
				Data:     []string{},
				Version:  "1.0.0",
				Template: nil,
				Legend:   nil,
				VectorLayers: []tilejson.VectorLayer{
					{
						Version:      2,
						Extent:       4096,
						ID:           testLayer1.MVTName(),
						Name:         testLayer1.MVTName(),
						GeometryType: tilejson.GeomTypePoint,
						MinZoom:      testLayer1.MinZoom,
						MaxZoom:      testLayer3.MaxZoom, // layer 1 and layer 3 share a name in our test so the zoom range includes the entire zoom range
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: testLayer1.MVTName(),
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
					{
						Version:      2,
						Extent:       4096,
						ID:           testLayer2.MVTName(),
						Name:         testLayer2.MVTName(),
						GeometryType: tilejson.GeomTypeLine,
						MinZoom:      testLayer2.MinZoom,
						MaxZoom:      testLayer2.MaxZoom,
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: testLayer2.MVTName(),
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
					{
						Version:      2,
						Extent:       4096,
						ID:           "debug-tile-outline",
						Name:         "debug-tile-outline",
						GeometryType: tilejson.GeomTypeLine,
						MinZoom:      0,
						MaxZoom:      atlas.MaxZoom,
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: "debug-tile-outline",
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
					{
						Version:      2,
						Extent:       4096,
						ID:           "debug-tile-center",
						Name:         "debug-tile-center",
						GeometryType: tilejson.GeomTypePoint,
						MinZoom:      0,
						MaxZoom:      atlas.MaxZoom,
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: "debug-tile-center",
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
				},
			},
		},
		// https://github.com/go-spatial/tegola/issues/994
		"hostname with scheme": {
			handler: server.HandleCapabilities{},
			hostName: &url.URL{
				// The scheme is determined at request time. if the
				// user sets the scheme on the hostname, it will be
				// ignored
				Scheme: "https",
				Host:   "cdn.tegola.io",
			},
			port:      "none",
			uri:       "http://localhost:8080/capabilities/test-map.json?debug=true",
			reqMethod: http.MethodGet,
			expected: tilejson.TileJSON{
				Attribution:     &testMapAttribution,
				Bounds:          [4]float64{-180.0, -85.0511, 180.0, 85.0511},
				Center:          testMapCenter,
				CRS:             atlas.DefaultTileGrid().CRSURI(),
				Format:          server.TileURLFileFormat,
				MinZoom:         0,
				MaxZoom:         atlas.MaxZoom,
				Name:            &testMapName,
				Description:     nil,
				Scheme:          tilejson.SchemeXYZ,
				TileMatrixSetID: atlas.DefaultTileGrid().ID(),
				TileJSON:        tilejson.Version,
				Tiles: []string{
					server.TileURLTemplate{
						Scheme:  "http",
						Host:    "cdn.tegola.io",
						MapName: "test-map",
						Query: url.Values{
							server.QueryKeyDebug: []string{
								"true",
							},
						},
					}.String(),
				},
				Grids:    []string{},
				Data:     []string{},
				Version:  "1.0.0",
				Template: nil,
				Legend:   nil,
				VectorLayers: []tilejson.VectorLayer{
					{
						Version:      2,
						Extent:       4096,
						ID:           testLayer1.MVTName(),
						Name:         testLayer1.MVTName(),
						GeometryType: tilejson.GeomTypePoint,
						MinZoom:      testLayer1.MinZoom,
						MaxZoom:      testLayer3.MaxZoom, // layer 1 and layer 3 share a name in our test so the zoom range includes the entire zoom range
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: testLayer1.MVTName(),
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
					{
						Version:      2,
						Extent:       4096,
						ID:           testLayer2.MVTName(),
						Name:         testLayer2.MVTName(),
						GeometryType: tilejson.GeomTypeLine,
						MinZoom:      testLayer2.MinZoom,
						MaxZoom:      testLayer2.MaxZoom,
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: testLayer2.MVTName(),
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
					{
						Version:      2,
						Extent:       4096,
						ID:           "debug-tile-outline",
						Name:         "debug-tile-outline",
						GeometryType: tilejson.GeomTypeLine,
						MinZoom:      0,
						MaxZoom:      atlas.MaxZoom,
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: "debug-tile-outline",
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
					{
						Version:      2,
						Extent:       4096,
						ID:           "debug-tile-center",
						Name:         "debug-tile-center",
						GeometryType: tilejson.GeomTypePoint,
						MinZoom:      0,
						MaxZoom:      atlas.MaxZoom,
						Tiles: []string{
							server.TileURLTemplate{
								Scheme:    "http",
								Host:      "cdn.tegola.io",
								MapName:   "test-map",
								LayerName: "debug-tile-center",
								Query: url.Values{
									server.QueryKeyDebug: []string{
										"true",
									},
								},
							}.String(),
						},
					},
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestHandleMapCapabilitiesCORS(t *testing.T) {
	tests := map[string]CORSTestCase{
		"1": {
			uri: "/capabilities/test-map.json",
		},
	}

	for name, tc := range tests {
		t.Run(name, CORSTest(tc))
	}
}

func TestHandleMapCapabilitiesDescribesDefaultTileMatrixSet(t *testing.T) {
	const mapName = "world-crs84-map"
	originalHostName := server.HostName
	server.HostName = &url.URL{Host: serverHostName}
	defer func() { server.HostName = originalHostName }()

	m := atlas.NewWebMercatorMap(mapName)
	grid, err := tms.Get(tms.WorldCRS84Quad)
	if err != nil {
		t.Fatal(err)
	}
	m.TileMatrixSets = []*tms.TileMatrixSet{grid}
	m.Layers = []atlas.Layer{testLayer1}
	atlas.AddMap(m)

	r, err := http.NewRequest(http.MethodGet, "http://localhost:8080/capabilities/"+mapName+".json", nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.NewRouter(nil).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %v, want %v: %v", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		TileJSON        string   `json:"tilejson"`
		CRS             string   `json:"crs"`
		TileMatrixSetID string   `json:"tileMatrixSetId"`
		Tiles           []string `json:"tiles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got.TileJSON != tilejson.Version {
		t.Errorf("tilejson = %q, want %q", got.TileJSON, tilejson.Version)
	}
	if got.CRS != grid.CRSURI() {
		t.Errorf("crs = %q, want %q", got.CRS, grid.CRSURI())
	}
	if got.TileMatrixSetID != grid.ID() {
		t.Errorf("tileMatrixSetId = %q, want %q", got.TileMatrixSetID, grid.ID())
	}
	if len(got.Tiles) != 1 || got.Tiles[0] != "http://tegola.io/maps/"+mapName+"/{z}/{x}/{y}.pbf" {
		t.Errorf("tiles = %v, want the native z/x/y template", got.Tiles)
	}
}
