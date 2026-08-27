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
}
