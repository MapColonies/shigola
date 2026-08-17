package ogc_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dimfeld/httptreemux"

	"github.com/MapColonies/shigola/server/ogc"
	"github.com/MapColonies/shigola/tms"
)

// TestAPIIsWalkable follows every link the service publishes, starting from the
// landing page, and requires each to resolve.
//
// This is the property the OGC CITE suite checks and the one clients depend on:
// a client is meant to reach every resource by following links, never by
// building URLs. A link that 404s — a wrong prefix, a stale path, a scheme a
// collection does not actually offer — makes the service undiscoverable, and no
// single-resource test catches it, because each resource is fine on its own.
//
// It does not replace running CITE. It is the part of CITE that can run without
// a TeamEngine instance and a deployed server.
func TestAPIIsWalkable(t *testing.T) {
	a := newAtlas(t, tms.WebMercatorQuad, tms.WorldCRS84Quad)
	r := newRouterFor(t, a)

	const root = "http://tegola.io"

	// Visited URLs, so a self link or a cycle does not walk forever.
	seen := map[string]bool{}

	var walk func(t *testing.T, href string, depth int)
	walk = func(t *testing.T, href string, depth int) {
		t.Helper()

		if depth > 6 || seen[href] {
			return
		}
		seen[href] = true

		path, ok := strings.CutPrefix(href, root)
		if !ok {
			t.Errorf("link %q does not point at this service", href)
			return
		}

		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("following %q: status = %d, want 200 (body %s)", href, w.Code, w.Body.String())
			return
		}

		// Only JSON documents carry further links.
		if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			return
		}

		for _, link := range linksIn(t, href, w.Body.Bytes()) {
			// A templated link is a URI template, not a URL; it is exercised
			// separately below, since filling it in needs real tile indices.
			if link.Templated {
				continue
			}

			walk(t, link.Href, depth+1)
		}
	}

	walk(t, root+"/", 0)

	// The walk is only meaningful if it actually reached the leaves.
	for _, want := range []string{
		root + "/conformance",
		root + "/collections",
		root + "/collections/osm",
		root + "/collections/osm:water",
		root + "/collections/osm/tiles",
		root + "/collections/osm/tiles/WebMercatorQuad",
		root + "/collections/osm/tiles/WorldCRS84Quad",
		root + "/tileMatrixSets",
		root + "/tileMatrixSets/WebMercatorQuad",
	} {
		if !seen[want] {
			t.Errorf("the walk never reached %q", want)
		}
	}
}

// TestTileTemplateResolves fills in the templated tile link the way a client
// does, and requires the result to be a tile.
//
// The template is the only URL a client builds itself, so its variable names and
// their order are load-bearing: {tileMatrix}/{tileRow}/{tileCol} substituted in
// the wrong order returns another tile, or a 400, with nothing to say why.
func TestTileTemplateResolves(t *testing.T) {
	a := newAtlas(t, tms.WorldCRS84Quad)
	r := newRouterFor(t, a)

	var tileset ogc.TileSetMetadata
	if w := get(t, r, "/collections/osm/tiles/WorldCRS84Quad", &tileset); w.Code != http.StatusOK {
		t.Fatalf("tileset status = %d, want 200", w.Code)
	}

	var template string
	for _, link := range tileset.Links {
		if link.Rel == "item" && link.Templated {
			template = link.Href
		}
	}

	if template == "" {
		t.Fatal("the tileset has no templated item link")
	}

	// Row 0, column 1 of matrix 1: valid in WorldCRS84Quad, whose z1 matrix is
	// four columns by two rows. Transposing them is also valid here, which is
	// why the limits below are checked rather than just the status.
	filled := strings.NewReplacer(
		"{tileMatrix}", "1",
		"{tileRow}", "0",
		"{tileCol}", "3",
	).Replace(template)

	path, ok := strings.CutPrefix(filled, "http://tegola.io")
	if !ok {
		t.Fatalf("template %q does not point at this service", template)
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("filled template %q: status = %d, want 200 (body %s)", path, w.Code, w.Body.String())
	}

	if got := w.Header().Get("Content-Type"); got != ogc.MediaTypeMVT {
		t.Errorf("Content-Type = %q, want %q", got, ogc.MediaTypeMVT)
	}

	// Column 3 exists at z1 and row 3 does not, so a client that swapped the
	// two would be told, rather than served a neighbouring tile.
	transposed := strings.NewReplacer(
		"{tileMatrix}", "1",
		"{tileRow}", "3",
		"{tileCol}", "0",
	).Replace(template)

	path, _ = strings.CutPrefix(transposed, "http://tegola.io")
	req = httptest.NewRequest(http.MethodGet, path, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("transposed template %q: status = %d, want 400", path, w.Code)
	}
}

// linksIn pulls the links out of any of this service's JSON documents.
//
// The documents differ in shape — some carry links at the top level, some in a
// nested array of resources — so this reads whichever are present rather than
// decoding into a specific type.
func linksIn(t *testing.T, href string, body []byte) []ogc.Link {
	t.Helper()

	var doc struct {
		Links       []ogc.Link `json:"links"`
		Collections []struct {
			Links []ogc.Link `json:"links"`
		} `json:"collections"`
		Tilesets []struct {
			Links []ogc.Link `json:"links"`
		} `json:"tilesets"`
		TileMatrixSets []struct {
			Links []ogc.Link `json:"links"`
		} `json:"tileMatrixSets"`
	}

	if err := json.Unmarshal(body, &doc); err != nil {
		// A tiling scheme definition is a document of another shape entirely,
		// and carries no links; that is not a failure.
		return nil
	}

	links := doc.Links
	for _, c := range doc.Collections {
		links = append(links, c.Links...)
	}
	for _, ts := range doc.Tilesets {
		links = append(links, ts.Links...)
	}
	for _, tms := range doc.TileMatrixSets {
		links = append(links, tms.Links...)
	}

	return links
}

// newRouterAt mounts the surface under a path prefix, for the prefix walk.
func newRouterAt(t *testing.T, prefix string) *httptreemux.TreeMux {
	t.Helper()

	svc := ogc.New(ogc.Config{
		Atlas:     newAtlas(t, tms.WebMercatorQuad),
		URLRoot:   func(*http.Request) *url.URL { return &url.URL{Scheme: "http", Host: "tegola.io"} },
		URIPrefix: prefix,
	})

	r := httptreemux.New()
	group := r.NewGroup(prefix)
	for _, route := range svc.Routes() {
		group.UsingContext().Handler(route.Method, route.Path, route.Handler)
	}

	return r
}

// TestAPIIsWalkableBehindPrefix repeats the walk for a service mounted behind a
// reverse-proxy prefix, where a link that forgot the prefix resolves off the
// service entirely.
func TestAPIIsWalkableBehindPrefix(t *testing.T) {
	r := newRouterAt(t, "/tegola")

	const root = "http://tegola.io/tegola"

	seen := map[string]bool{}

	var walk func(href string, depth int)
	walk = func(href string, depth int) {
		if depth > 6 || seen[href] {
			return
		}
		seen[href] = true

		path, ok := strings.CutPrefix(href, "http://tegola.io")
		if !ok {
			t.Errorf("link %q does not point at this service", href)
			return
		}

		if !strings.HasPrefix(path, "/tegola") {
			t.Errorf("link %q drops the mount prefix", href)
			return
		}

		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("following %q: status = %d, want 200", href, w.Code)
			return
		}

		if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			return
		}

		for _, link := range linksIn(t, href, w.Body.Bytes()) {
			if link.Templated {
				continue
			}

			walk(link.Href, depth+1)
		}
	}

	walk(root+"/", 0)

	if !seen[root+"/collections/osm/tiles/WebMercatorQuad"] {
		t.Errorf("the walk never reached the tileset behind the prefix; saw %d urls", len(seen))
	}
}
