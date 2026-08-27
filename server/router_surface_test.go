package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/MapColonies/shigola/server"
)

// TestRouterSurface covers every resource NewRouter registers being reachable
// through it.
//
// It asks the router rather than the handlers: a handler that is never
// registered, or registered at a path with a typo, is not a compile error, and
// the ogc package's own tests mount its routes themselves rather than through
// NewRouter -- so this is the only place the assembled surface is exercised.
func TestRouterSurface(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)

	for _, path := range []string{
		"/",
		"/api",
		"/conformance",
		"/collections",
		"/collections/" + testMapName,
		"/collections/" + testMapName + "/tiles",
		"/collections/" + testMapName + "/tiles/WebMercatorQuad",
		ogcTileURI,
		"/tileMatrixSets",
		"/tileMatrixSets/WebMercatorQuad",
	} {
		w, _, err := doRequest(t, a, http.MethodGet, path, nil)
		if err != nil {
			t.Fatalf("request %v: %v", path, err)
		}

		if w.Code != http.StatusOK {
			t.Errorf("GET %v status = %d, want 200", path, w.Code)
		}
	}
}
