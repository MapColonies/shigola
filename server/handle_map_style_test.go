package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"testing"

	"github.com/MapColonies/shigola/mapbox/style"
	providerDebug "github.com/MapColonies/shigola/provider/debug"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-test/deep"
)

func TestHandleMapStyle(t *testing.T) {
	type tcase struct {
		handler        http.Handler
		uriPrefix      string
		uri            string
		uriPattern     string
		serverHostName string
		expected       style.Root
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			var err error

			// config params this test relies on
			server.HostName = nil
			if tc.serverHostName != "" {
				server.HostName = &url.URL{
					Host: tc.serverHostName,
				}
			}

			if tc.uriPrefix != "" {
				server.URIPrefix = tc.uriPrefix
			} else {
				server.URIPrefix = "/"
			}

			resp, _, err := doRequest(t, nil, http.MethodGet, tc.uri, nil)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if resp.Code != http.StatusOK {
				t.Fatalf("handler returned wrong status code: got (%d) expected (%d)", resp.Code, http.StatusOK)
			}

			// read the response body
			var output style.Root
			if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
				t.Fatalf("unable to unmarshal JSON response body: %s", err)
			}

			if diff := deep.Equal(output, tc.expected); diff != nil {
				t.Fatalf("output does not match expected. diff %s", diff)
			}
		}
	}

	tests := map[string]tcase{
		"default": {
			handler:        server.HandleMapStyle{},
			uri:            path.Join("/maps", testMapName, "style.json"),
			uriPattern:     "/maps/:map_name/style.json",
			serverHostName: serverHostName,
			expected: style.Root{
				Name:    testMapName,
				Version: style.Version,
				Center:  [2]float64{testMapCenter[0], testMapCenter[1]},
				Zoom:    testMapCenter[2],
				Sources: map[string]style.Source{
					testMapName: {
						Type: style.SourceTypeVector,
						URL: (&url.URL{
							Scheme:   "http",
							Host:     serverHostName,
							Path:     path.Join(server.URIPrefix, "collections", testMapName, "tiles", tms.WebMercatorQuad),
							RawQuery: "f=tilejson",
						}).String(),
					},
				},
				Layers: []style.Layer{
					{
						ID:          testLayer1.MVTName(),
						Source:      testMapName,
						SourceLayer: testLayer1.MVTName(),
						Type:        style.LayerTypeCircle,
						Layout: &style.LayerLayout{
							Visibility: "visible",
						},
						Paint: &style.LayerPaint{
							CircleRadius: 3,
							CircleColor:  "#56f8aa",
						},
					},
					{
						ID:          testLayer2.MVTName(),
						Source:      testMapName,
						SourceLayer: testLayer2.MVTName(),
						Type:        style.LayerTypeLine,
						Layout: &style.LayerLayout{
							Visibility: "visible",
						},
						Paint: &style.LayerPaint{
							LineColor: "#9d70ab",
						},
					},
				},
			},
		},
		"uri prefix set": {
			handler:        server.HandleMapStyle{},
			uriPrefix:      "/tegola",
			uri:            path.Join("/tegola", "maps", testMapName, "style.json"),
			uriPattern:     "/tegola/maps/:map_name/style.json",
			serverHostName: serverHostName,
			expected: style.Root{
				Name:    testMapName,
				Version: style.Version,
				Center:  [2]float64{testMapCenter[0], testMapCenter[1]},
				Zoom:    testMapCenter[2],
				Sources: map[string]style.Source{
					testMapName: {
						Type: style.SourceTypeVector,
						URL: (&url.URL{
							Scheme:   "http",
							Host:     serverHostName,
							Path:     path.Join(server.URIPrefix, "tegola", "collections", testMapName, "tiles", tms.WebMercatorQuad),
							RawQuery: "f=tilejson",
						}).String(),
					},
				},
				Layers: []style.Layer{
					{
						ID:          testLayer1.MVTName(),
						Source:      testMapName,
						SourceLayer: testLayer1.MVTName(),
						Type:        style.LayerTypeCircle,
						Layout: &style.LayerLayout{
							Visibility: "visible",
						},
						Paint: &style.LayerPaint{
							CircleRadius: 3,
							CircleColor:  "#56f8aa",
						},
					},
					{
						ID:          testLayer2.MVTName(),
						Source:      testMapName,
						SourceLayer: testLayer2.MVTName(),
						Type:        style.LayerTypeLine,
						Layout: &style.LayerLayout{
							Visibility: "visible",
						},
						Paint: &style.LayerPaint{
							LineColor: "#9d70ab",
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

func TestHandleMapStyleDebug(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	resp, _, err := doRequest(t, nil, http.MethodGet, "/maps/"+testMapName+"/style.json?debug=true", nil)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("handler returned status %d, want %d", resp.Code, http.StatusOK)
	}

	var output style.Root
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		t.Fatalf("unable to unmarshal JSON response body: %s", err)
	}

	source, ok := output.Sources[testMapName]
	if !ok {
		t.Fatalf("style has no source for %q", testMapName)
	}
	if source.URL != "" {
		t.Errorf("debug style source URL = %q, want an inline tile template", source.URL)
	}
	if diff := deep.Equal(source.Tiles, []string{
		"http://" + serverHostName + "/maps/" + testMapName + "/{z}/{x}/{y}.pbf?debug=true",
	}); diff != nil {
		t.Errorf("debug style tiles differ: %s", diff)
	}

	layerIDs := make([]string, 0, len(output.Layers))
	for _, layer := range output.Layers {
		layerIDs = append(layerIDs, layer.ID)
	}
	if diff := deep.Equal(layerIDs, []string{
		testLayer1.MVTName(),
		testLayer2.MVTName(),
		providerDebug.LayerDebugTileOutline,
		providerDebug.LayerDebugTileCenter,
	}); diff != nil {
		t.Errorf("debug style layer IDs differ: %s", diff)
	}
}

func TestHandleMapStyleCORS(t *testing.T) {
	tests := map[string]CORSTestCase{
		// Leading slash, as every other CORS case has: path.Join drops it, and
		// the request then only matched because the viewer's catch-all sat at
		// the service root. The OGC landing page took that root over, so an
		// unrooted path now gets httptreemux's clean-path redirect instead.
		"1": {
			uri: "/" + path.Join("maps", testMapName, "style.json"),
		},
	}

	for name, tc := range tests {
		t.Run(name, CORSTest(tc))
	}
}
