package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/MapColonies/shigola/server"
)

// TestNativeRoutesRemoved covers the removal of the routes under the native map
// prefix. Tiles are served only through the OGC collections surface.
//
// As with the capabilities and viewer removals, this asks the router rather than
// the package: deleting a handler is a compile error, but leaving a route
// registered is not.
func TestNativeRoutesRemoved(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)

	// MAPCO-11485. Styling is a separate specification this server does not
	// implement, so clients supply their own style documents.
	//
	// Both spellings the handler answered on: it split the extension off the
	// map_name parameter itself, so /style.json was matched by the route and the
	// map name read from what came before the dot.
	t.Run("no map style route is registered", func(t *testing.T) {
		for _, path := range []string{
			"/maps/" + testMapName + "/style.json",
			"/maps/" + testMapName + "/style",
		} {
			w, _, err := doRequest(t, a, http.MethodGet, path, nil)
			if err != nil {
				t.Fatalf("request %v: %v", path, err)
			}

			if w.Code != http.StatusNotFound {
				t.Errorf("GET %v status = %d, want 404", path, w.Code)
			}
		}
	})

	// MAPCO-11484. Both native tile routes, with and without the layer segment,
	// and with and without the extension the handler trimmed off the y
	// parameter. A removal that unregistered the whole-map route and left the
	// per-layer one would still be serving tiles at a second, undocumented URL.
	t.Run("no native tile route is registered", func(t *testing.T) {
		for _, path := range []string{
			"/maps/" + testMapName + "/4/2/3",
			"/maps/" + testMapName + "/4/2/3.pbf",
			"/maps/" + testMapName + "/" + testLayer1.MVTName() + "/4/2/3",
			"/maps/" + testMapName + "/" + testLayer1.MVTName() + "/4/2/3.pbf",
		} {
			w, _, err := doRequest(t, a, http.MethodGet, path, nil)
			if err != nil {
				t.Fatalf("request %v: %v", path, err)
			}

			if w.Code != http.StatusNotFound {
				t.Errorf("GET %v status = %d, want 404", path, w.Code)
			}
		}
	})

	// The same tile is served by the surface that replaced them, so these 404s
	// are a moved surface rather than a lost one.
	t.Run("the OGC route serves the tile instead", func(t *testing.T) {
		w, _, err := doRequest(t, a, http.MethodGet, ogcTileURI, nil)
		if err != nil {
			t.Fatalf("request %v: %v", ogcTileURI, err)
		}

		if w.Code != http.StatusOK {
			t.Errorf("GET %v status = %d, want 200", ogcTileURI, w.Code)
		}
	})
}

// TestCORS covers the CORS preflight the OPTIONS handler answers for every
// registered route. It was covered through the native tile and style routes
// until those were removed; the headers are the router's, not any handler's, so
// an OGC route exercises the same code.
func TestCORS(t *testing.T) {
	tests := map[string]CORSTestCase{
		"tile":         {uri: ogcTileURI},
		"landing page": {uri: "/"},
		"collections":  {uri: "/collections"},
	}

	for name, tc := range tests {
		t.Run(name, CORSTest(tc))
	}
}
