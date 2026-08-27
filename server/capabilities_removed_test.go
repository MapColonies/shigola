package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
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
// The debug style's own source, which this change also had to preserve, is
// covered by TestHandleMapStyleDebug rather than here: it is a property of the
// style endpoint, not evidence about the capabilities routes.
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

	// The style endpoint outlives this change (MAPCO-11485 removes it) and named
	// the capabilities document as its vector source. A removal that left that
	// URL in place would serve a document pointing at a 404, which is the same
	// dangling reference the docs are being cleaned of.
	t.Run("the map style document does not point at a capabilities URL", func(t *testing.T) {
		w, _, err := doRequest(t, a, http.MethodGet, "/maps/"+testMapName+"/style.json", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		var doc struct {
			Sources map[string]struct {
				URL string `json:"url"`
			} `json:"sources"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decoding style: %v (body %s)", err, w.Body.String())
		}

		source, ok := doc.Sources[testMapName]
		if !ok {
			t.Fatalf("style has no source for %q: %s", testMapName, w.Body.String())
		}

		if strings.Contains(source.URL, "/capabilities") {
			t.Errorf("style source URL = %q, still points at the capabilities endpoint", source.URL)
		}

		// Not just "not capabilities": the URL has to name something served.
		u, err := url.Parse(source.URL)
		if err != nil {
			t.Fatalf("parsing style source URL %q: %v", source.URL, err)
		}

		w, _, err = doRequest(t, a, http.MethodGet, u.RequestURI(), nil)
		if err != nil {
			t.Fatalf("request %v: %v", u.RequestURI(), err)
		}
		if w.Code != http.StatusOK {
			t.Errorf("GET %v (the style's source URL) status = %d, want 200", u.RequestURI(), w.Code)
		}
	})
}
