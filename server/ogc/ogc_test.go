package ogc_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/dimfeld/httptreemux"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/server/ogc"
	"github.com/MapColonies/shigola/tms"
)

// newRouter mounts the OGC surface the way the server does, so the tests
// exercise the routes as registered rather than the handlers in isolation.
func newRouter(t *testing.T, uriPrefix string) *httptreemux.TreeMux {
	t.Helper()

	svc := ogc.New(ogc.Config{
		Atlas:     &atlas.Atlas{},
		URLRoot:   func(*http.Request) *url.URL { return &url.URL{Scheme: "http", Host: "tegola.io"} },
		URIPrefix: uriPrefix,
	})

	r := httptreemux.New()
	group := r.NewGroup(uriPrefix)
	for _, route := range svc.Routes() {
		group.UsingContext().Handler(route.Method, route.Path, route.Handler)
	}

	return r
}

// get issues a request against the mounted surface and decodes the JSON body.
func get(t *testing.T, r *httptreemux.TreeMux, uri string, into any) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, uri, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if into != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			t.Fatalf("decoding %v: %v (body %s)", uri, err, w.Body.String())
		}
	}

	return w
}

func TestLandingPage(t *testing.T) {
	var doc ogc.LandingPage
	w := get(t, newRouter(t, "/"), "/", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if got := w.Header().Get("Content-Type"); got != ogc.MediaTypeJSON {
		t.Errorf("Content-Type = %q, want %q", got, ogc.MediaTypeJSON)
	}

	// Every relation a client needs to bootstrap from the landing page alone.
	want := map[string]string{
		"self":         "http://tegola.io/",
		"service-desc": "http://tegola.io/api",
		"conformance":  "http://tegola.io/conformance",
		"data":         "http://tegola.io/collections",
		"http://www.opengis.net/def/rel/ogc/1.0/tiling-schemes": "http://tegola.io/tileMatrixSets",
	}

	got := map[string]string{}
	for _, link := range doc.Links {
		got[link.Rel] = link.Href
	}

	for rel, href := range want {
		if got[rel] != href {
			t.Errorf("link %q = %q, want %q", rel, got[rel], href)
		}
	}
}

// TestLandingPageURIPrefix covers a service behind a reverse proxy: every link
// must carry the prefix, or a client follows them straight off the service.
func TestLandingPageURIPrefix(t *testing.T) {
	var doc ogc.LandingPage
	w := get(t, newRouter(t, "/tegola"), "/tegola/", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	for _, link := range doc.Links {
		if link.Rel == "self" {
			continue
		}

		if want := "http://tegola.io/tegola/"; len(link.Href) < len(want) || link.Href[:len(want)] != want {
			t.Errorf("link %q = %q, want it under %q", link.Rel, link.Href, want)
		}
	}
}

func TestConformance(t *testing.T) {
	var doc ogc.Conformance
	w := get(t, newRouter(t, "/"), "/conformance", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// The classes ADR-0004 commits v1 to. oas30 is absent until /api exists:
	// a declared class the service does not satisfy is worse than a missing one.
	for _, want := range []string{
		"http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/core",
		"http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/landingPage",
		"http://www.opengis.net/spec/ogcapi-common-2/1.0/conf/collections",
		"http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/json",
		"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/core",
		"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/tileset",
		"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/tilesets-list",
		"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/mvt",
		"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/geodata-tilesets",
	} {
		if !slices.Contains(doc.ConformsTo, want) {
			t.Errorf("conformsTo is missing %q", want)
		}
	}
}

func TestTileMatrixSets(t *testing.T) {
	var doc ogc.TileMatrixSets
	w := get(t, newRouter(t, "/"), "/tileMatrixSets", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	ids := make([]string, 0, len(doc.TileMatrixSets))
	for _, item := range doc.TileMatrixSets {
		ids = append(ids, item.ID)
	}

	for _, want := range []string{tms.WebMercatorQuad, tms.WorldCRS84Quad, tms.WGS1984Quad} {
		if !slices.Contains(ids, want) {
			t.Errorf("tileMatrixSets is missing %q, got %v", want, ids)
		}
	}

	// A scheme this build cannot serve must not be advertised: a client would
	// follow it to a tileset the server then refuses.
	for _, gated := range []string{"NZTM2000Quad", "CDB1GlobalGrid"} {
		if slices.Contains(ids, gated) {
			t.Errorf("tileMatrixSets advertises gated scheme %q", gated)
		}
	}

	for _, item := range doc.TileMatrixSets {
		if len(item.Links) == 0 {
			t.Errorf("%v has no links", item.ID)
			continue
		}
		if want := "http://tegola.io/tileMatrixSets/" + item.ID; item.Links[0].Href != want {
			t.Errorf("%v self link = %q, want %q", item.ID, item.Links[0].Href, want)
		}
	}
}

func TestTileMatrixSet(t *testing.T) {
	r := newRouter(t, "/")

	t.Run("an active scheme is served verbatim", func(t *testing.T) {
		var doc map[string]any
		w := get(t, r, "/tileMatrixSets/WorldCRS84Quad", &doc)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		if doc["id"] != tms.WorldCRS84Quad {
			t.Errorf("id = %v, want %v", doc["id"], tms.WorldCRS84Quad)
		}

		// the OGC document's own fields, not a re-marshalling of our model
		if _, ok := doc["tileMatrices"]; !ok {
			t.Errorf("document has no tileMatrices: %v", doc)
		}
	})

	t.Run("an unknown scheme is 404", func(t *testing.T) {
		if w := get(t, r, "/tileMatrixSets/NoSuchQuad", nil); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	// Gated schemes are registered but unservable, and must not leak through as
	// though they were available.
	t.Run("a gated scheme is 404", func(t *testing.T) {
		if w := get(t, r, "/tileMatrixSets/NZTM2000Quad", nil); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func TestNegotiation(t *testing.T) {
	r := newRouter(t, "/")

	tests := map[string]struct {
		uri     string
		accept  string
		status  int
		content string
	}{
		"default is json": {
			uri: "/conformance", status: http.StatusOK, content: ogc.MediaTypeJSON,
		},
		"f=json": {
			uri: "/conformance?f=json", status: http.StatusOK, content: ogc.MediaTypeJSON,
		},
		"an unsupported f is a bad request, not a silent default": {
			uri: "/conformance?f=html", status: http.StatusBadRequest,
		},
		"Accept json": {
			uri: "/conformance", accept: "application/json", status: http.StatusOK, content: ogc.MediaTypeJSON,
		},
		// What a browser sends. It cannot be satisfied, and answering with the
		// default beats a 406 no client would recover from.
		"Accept html falls back to the default": {
			uri:    "/conformance",
			accept: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			status: http.StatusOK, content: ogc.MediaTypeJSON,
		},

		// /api has a single representation, but the rule is the surface's, not
		// each resource's: it negotiates like everything else. Its media type is
		// the OpenAPI profile rather than plain JSON, which is what "json"
		// means for this resource.
		"/api default": {
			uri: "/api", status: http.StatusOK, content: ogc.MediaTypeOpenAPI,
		},
		"/api f=json": {
			uri: "/api?f=json", status: http.StatusOK, content: ogc.MediaTypeOpenAPI,
		},
		"/api rejects an unsupported f like every other route": {
			uri: "/api?f=html", status: http.StatusBadRequest,
		},
		"/api rejects the mvt alias it cannot serve": {
			uri: "/api?f=pbf", status: http.StatusBadRequest,
		},
		"/api Accept html falls back to the default": {
			uri:    "/api",
			accept: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			status: http.StatusOK, content: ogc.MediaTypeOpenAPI,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.uri, nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.status, w.Body.String())
			}

			if tc.content != "" && w.Header().Get("Content-Type") != tc.content {
				t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), tc.content)
			}
		})
	}
}
