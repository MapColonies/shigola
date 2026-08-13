package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-spatial/tegola/server"
)

// TestOGCMount covers the one non-additive change of the OGC work (ADR-0003):
// the landing page takes over "/", and the viewer moves to /viewer.
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

	// The viewer's assets are referenced relatively, so they only resolve from a
	// URL ending in a slash.
	t.Run("the viewer redirects to its own directory", func(t *testing.T) {
		w, _, err := doRequest(t, a, http.MethodGet, "/viewer", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		if w.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", w.Code)
		}

		if got := w.Header().Get("Location"); got != "/viewer/" {
			t.Errorf("Location = %q, want %q", got, "/viewer/")
		}
	})

	t.Run("the native routes are untouched", func(t *testing.T) {
		w, _, err := doRequest(t, a, http.MethodGet, "/capabilities", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		if w.Code != http.StatusOK {
			t.Errorf("/capabilities status = %d, want 200", w.Code)
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
