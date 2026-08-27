package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/MapColonies/shigola/server"
)

// TestCapabilitiesRemoved covers the removal of the two non-standard capabilities
// endpoints (MAPCO-11483). Service and map discovery are the OGC landing page,
// /conformance and /collections, which cover the same ground in standard form.
//
// A removal that only deleted the handlers would still leave the routes
// registered and answering, so this asks the router, not the package.
//
// The style endpoint that had to stop naming a capabilities URL is itself gone
// (MAPCO-11485), so what was asserted about its source is gone with it.
func TestCapabilitiesRemoved(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)

	// Every spelling either endpoint answered on. /capabilities/:map_name matched
	// with or without an extension, because the handler split the extension off
	// the parameter itself rather than routing on it.
	t.Run("no capabilities route is registered", func(t *testing.T) {
		paths := []string{
			"/capabilities",
			"/capabilities/" + testMapName,
			"/capabilities/" + testMapName + ".json",
		}

		for _, path := range paths {
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
