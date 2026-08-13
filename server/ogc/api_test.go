package ogc_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-spatial/tegola/server/ogc"
	"github.com/go-spatial/tegola/tms"
)

// TestAPI covers /api, the OpenAPI definition the landing page's service-desc
// link points at. Serving it is what lets the service claim oas30.
func TestAPI(t *testing.T) {
	r := newRouterFor(t, newAtlas(t))

	var doc map[string]any
	w := get(t, r, "/api", &doc)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}

	// The media type OGC requires for an OpenAPI 3.0 definition. A plain
	// application/json body is not enough for a client sniffing the type.
	if got := w.Header().Get("Content-Type"); got != ogc.MediaTypeOpenAPI {
		t.Errorf("Content-Type = %q, want %q", got, ogc.MediaTypeOpenAPI)
	}

	version, _ := doc["openapi"].(string)
	if !strings.HasPrefix(version, "3.0") {
		t.Errorf("openapi = %q, want a 3.0.x version", version)
	}

	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths = %v, want an object", doc["paths"])
	}

	// Every resource the service serves must be described, or a client
	// generated from this document cannot reach it.
	for _, want := range []string{
		"/",
		"/api",
		"/conformance",
		"/collections",
		"/collections/{collectionId}",
		"/collections/{collectionId}/tiles",
		"/collections/{collectionId}/tiles/{tileMatrixSetId}",
		"/collections/{collectionId}/tiles/{tileMatrixSetId}/{tileMatrix}/{tileRow}/{tileCol}",
		"/tileMatrixSets",
		"/tileMatrixSets/{tileMatrixSetId}",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("paths is missing %q", want)
		}
	}
}

// TestAPIServerURL covers the one part of the document that cannot be static:
// where the service actually is. A client reading a definition that points at
// the wrong host, or at the root of a service mounted under a prefix, sends
// every request to the wrong place.
func TestAPIServerURL(t *testing.T) {
	var doc struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}

	w := get(t, newRouterFor(t, newAtlas(t)), "/api", &doc)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if len(doc.Servers) == 0 {
		t.Fatal("no servers entry")
	}

	// No trailing slash: OpenAPI appends each path, and every path in the
	// document begins with one, so a trailing slash here yields "//conformance".
	if doc.Servers[0].URL != "http://tegola.io" {
		t.Errorf("servers[0].url = %q, want %q", doc.Servers[0].URL, "http://tegola.io")
	}
}

// TestConformanceDeclaresOAS30 pins the claim /api unlocks. A class is only
// claimed once the service satisfies it, so this belongs with /api and not
// before it.
func TestConformanceDeclaresOAS30(t *testing.T) {
	var doc ogc.Conformance
	if w := get(t, newRouterFor(t, newAtlas(t)), "/conformance", &doc); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	want := "http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/oas30"
	if !slices.Contains(doc.ConformsTo, want) {
		t.Errorf("conformsTo is missing %q: %v", want, doc.ConformsTo)
	}
}

// TestAPIDescribesEveryServedRoute is the check that keeps a hand-maintained
// document honest: every route the service registers must appear in it.
//
// The comparison is on the route table itself, so adding a route without
// documenting it fails here rather than at CITE time.
func TestAPIDescribesEveryServedRoute(t *testing.T) {
	var doc struct {
		Paths map[string]any `json:"paths"`
	}

	if w := get(t, newRouterFor(t, newAtlas(t)), "/api", &doc); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	svc := ogc.New(ogc.Config{Atlas: newAtlas(t)})
	for _, route := range svc.Routes() {
		path := openAPIPath(route.Path)
		if _, ok := doc.Paths[path]; !ok {
			t.Errorf("route %q is not described in /api (looked for %q)", route.Path, path)
		}
	}
}

// openAPIPath rewrites a httptreemux route into its OpenAPI template form:
// ":collection_id" becomes "{collectionId}".
func openAPIPath(route string) string {
	segments := strings.Split(route, "/")
	for i, segment := range segments {
		if !strings.HasPrefix(segment, ":") {
			continue
		}

		parts := strings.Split(strings.TrimPrefix(segment, ":"), "_")
		for j := 1; j < len(parts); j++ {
			parts[j] = strings.ToUpper(parts[j][:1]) + parts[j][1:]
		}

		segments[i] = "{" + strings.Join(parts, "") + "}"
	}

	return strings.Join(segments, "/")
}

// TestAPIIsValidJSON guards the embedded document itself: a hand-maintained
// file is one stray comma away from being unservable, and the failure would
// otherwise only surface as a broken client.
func TestAPIIsValidJSON(t *testing.T) {
	w := get(t, newRouterFor(t, newAtlas(t, tms.WebMercatorQuad)), "/api", nil)

	var doc any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the embedded OpenAPI document is not valid JSON: %v", err)
	}
}

// TestAPIOperationIds pins the operation ids OGC API - Tiles fixes by name.
//
// Requirement /req/oas30/operation-id and its Table 11 name the operations an
// OpenAPI definition must declare, and the CITE suite checks them literally. A
// descriptive-but-invented id — getCollectionTile, say — reads fine and fails
// conformance, with a message that names no path.
func TestAPIOperationIds(t *testing.T) {
	var doc struct {
		Paths map[string]struct {
			Get struct {
				OperationID string `json:"operationId"`
			} `json:"get"`
		} `json:"paths"`
	}

	if w := get(t, newRouterFor(t, newAtlas(t)), "/api", &doc); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Table 11 scopes an operation id as "scope.operation", and CITE checks the
	// tile one by substring: an id must contain ".getTile", leading dot and all.
	// A bare "getTile" fails, which is what a plain reading of the table would
	// produce — so the exact ids are pinned here, verified against a CITE run.
	want := map[string]string{
		"/collections/{collectionId}/tiles":                                                    "collection.getTileSetsList",
		"/collections/{collectionId}/tiles/{tileMatrixSetId}":                                  "collection.getTileSet",
		"/collections/{collectionId}/tiles/{tileMatrixSetId}/{tileMatrix}/{tileRow}/{tileCol}": "collection.getTile",
	}

	for path, operationID := range want {
		if got := doc.Paths[path].Get.OperationID; got != operationID {
			t.Errorf("operationId for %v = %q, want %q", path, got, operationID)
		}
	}

	if got := doc.Paths["/collections/{collectionId}/tiles/{tileMatrixSetId}/{tileMatrix}/{tileRow}/{tileCol}"].Get.OperationID; !strings.Contains(got, ".getTile") {
		t.Errorf("the tile operationId %q does not contain \".getTile\", which CITE checks literally", got)
	}
}
