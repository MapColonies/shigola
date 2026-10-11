package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/server/admin"
)

func (f *fixture) get(path string) *httptest.ResponseRecorder {
	f.t.Helper()

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	return rec
}

func TestOpenAPIDescribesEveryAdminRoute(t *testing.T) {
	f := newFixture(t, "s3cret", atlas.NewWebMercatorMap("osm"))

	rec := f.get("/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200 without a token", rec.Code)
	}

	var doc struct {
		OpenAPI    string                    `json:"openapi"`
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type   string `json:"type"`
				Scheme string `json:"scheme"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Errorf("openapi: got %q, expected a 3.x document", doc.OpenAPI)
	}

	for _, r := range admin.Routes() {
		if _, ok := doc.Paths[r.Path][strings.ToLower(r.Method)]; !ok {
			t.Errorf("%v %v is served but not described", r.Method, r.Path)
		}
	}

	bearer := false
	for _, s := range doc.Components.SecuritySchemes {
		bearer = bearer || (s.Type == "http" && s.Scheme == "bearer")
	}
	if !bearer {
		t.Error("the document declares no bearer security scheme")
	}
}

func TestDocsServesSwaggerUIFromTheBinary(t *testing.T) {
	f := newFixture(t, "s3cret", atlas.NewWebMercatorMap("osm"))

	rec := f.get("/docs")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/docs: got %d %q, expected 200 html without a token", rec.Code, rec.Header().Get("Content-Type"))
	}

	refs := regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(rec.Body.String(), -1)
	if len(refs) == 0 {
		t.Fatal("/docs references no scripts or styles")
	}
	for _, ref := range refs {
		if strings.Contains(ref[1], "://") || strings.HasPrefix(ref[1], "//") {
			t.Errorf("/docs loads %q from another host; it must work air-gapped", ref[1])
			continue
		}
		if asset := f.get(ref[1]); asset.Code != http.StatusOK || asset.Body.Len() == 0 {
			t.Errorf("%v: got %d with %d bytes, expected it served", ref[1], asset.Code, asset.Body.Len())
		}
	}
}
