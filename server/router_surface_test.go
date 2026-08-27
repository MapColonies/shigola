package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/MapColonies/shigola/server"
)

// TestRouterSurface pins what NewRouter registers: the OGC API - Tiles
// resources, and nothing else.
//
// Written as a closed rule rather than as a list of paths that used to work.
// Every route the server registers begins with one of a handful of literal
// segments, so "anything else is a 404" is a statement about the surface that
// keeps holding for whatever anyone adds at the root later. The paths this fork
// used to answer on -- /capabilities, /maps/..., /viewer -- are instances of that
// rule rather than the point of the test, which is why they are not each given a
// test of their own.
//
// Both halves are needed. The 404 half alone would pass against a router that
// registered nothing at all, and deleting a handler is a compile error while
// leaving a route registered is silent -- so this asks the router, not the
// package.
func TestRouterSurface(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)

	t.Run("the OGC resources are served", func(t *testing.T) {
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
	})

	// The spellings are not arbitrary. /capabilities/:map_name and
	// /maps/:map_name/style.json both matched with and without an extension,
	// because each handler split the extension off its own parameter rather than
	// routing on it; the tile routes came in a whole-map and a per-layer form, so
	// unregistering one and missing the other would have been silent.
	t.Run("nothing outside it answers", func(t *testing.T) {
		for _, path := range []string{
			"/capabilities",
			"/capabilities/" + testMapName,
			"/capabilities/" + testMapName + ".json",
			"/maps/" + testMapName + "/style",
			"/maps/" + testMapName + "/style.json",
			"/maps/" + testMapName + "/4/2/3",
			"/maps/" + testMapName + "/4/2/3.pbf",
			"/maps/" + testMapName + "/" + testLayer1.MVTName() + "/4/2/3",
			"/maps/" + testMapName + "/" + testLayer1.MVTName() + "/4/2/3.pbf",
			"/viewer",
			"/viewer/",
			"/viewer/index.html",
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
