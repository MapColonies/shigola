package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/MapColonies/shigola/server"
)

// TestOGCMount covers the landing page taking over "/".
//
// ADR-0003 originally recorded this as a trade: the landing page took the root
// and displaced the embedded viewer to /viewer. The viewer is gone, so what is
// left of that decision is only the first half -- and the /viewer path it was
// moved to must now be served by nothing at all.
func TestOGCMount(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)

	t.Run("the landing page is served at the root", func(t *testing.T) {
		w, _, err := doRequest(t, a, http.MethodGet, "/", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		var doc struct {
			Title string `json:"title"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decoding landing page: %v (body %s)", err, w.Body.String())
		}

		if len(doc.Links) == 0 {
			t.Error("landing page has no links")
		}
	})

	// Both paths, because the viewer answered on both: /viewer redirected to
	// /viewer/, and the catch-all under it served the embedded assets. A removal
	// that left either one registered would still be serving a viewer.
	t.Run("no viewer route is registered", func(t *testing.T) {
		for _, path := range []string{"/viewer", "/viewer/", "/viewer/index.html"} {
			w, _, err := doRequest(t, a, http.MethodGet, path, nil)
			if err != nil {
				t.Fatalf("request %s: %v", path, err)
			}

			if w.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want 404", path, w.Code)
			}
		}
	})

	// There is no longer a native surface for the mount to displace: the
	// capabilities, style and tile routes it once shared the router with are all
	// gone (MAPCO-11483, MAPCO-11485, MAPCO-11484). What is left to check is that
	// the surface taking the root still serves a tile from it, which is the whole
	// point of taking the root at all.
	t.Run("a tile is served under the mounted surface", func(t *testing.T) {
		w, _, err := doRequest(t, a, http.MethodGet, ogcTileURI, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		if w.Code != http.StatusOK {
			t.Errorf("%v status = %d, want 200", ogcTileURI, w.Code)
		}
	})
}

// TestOGCMountURIPrefix covers the surface behind a reverse proxy prefix.
func TestOGCMountURIPrefix(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/tegola"
	defer func() { server.URIPrefix = "/" }()

	a := newTestMapWithLayers(testLayer1)

	req := httptest.NewRequest(http.MethodGet, "/tegola/conformance", nil)
	w := httptest.NewRecorder()
	server.NewRouter(a).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}

	var doc struct {
		ConformsTo []string `json:"conformsTo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decoding conformance: %v", err)
	}

	if len(doc.ConformsTo) == 0 {
		t.Error("conformance declaration is empty")
	}
}
