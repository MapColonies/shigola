package ogc_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/dimfeld/httptreemux"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/server/ogc"
)

// newRouterWithVersion mounts the surface with a known build version, which
// newRouter deliberately leaves unset.
func newRouterWithVersion(t *testing.T, version string) *httptreemux.TreeMux {
	t.Helper()

	svc := ogc.New(ogc.Config{
		Atlas:   &atlas.Atlas{},
		URLRoot: func(*http.Request) *url.URL { return &url.URL{Scheme: "http", Host: "tegola.io"} },
		Version: version,
	})

	r := httptreemux.New()
	group := r.NewGroup("/")
	for _, route := range svc.Routes() {
		group.UsingContext().Handler(route.Method, route.Path, route.Handler)
	}

	return r
}

// TestVersionReported covers the build reaching both places an operator looks:
// the landing page, and the API definition the landing page's service-desc link
// points at.
//
// The version was previously reported by the removed /capabilities endpoint
// (MAPCO-11483), which is the only reason this surface did not report it.
func TestVersionReported(t *testing.T) {
	const version = "v1.2.3-test"

	t.Run("the landing page names the build", func(t *testing.T) {
		var doc ogc.LandingPage
		if w := get(t, newRouterWithVersion(t, version), "/", &doc); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		if doc.ShigolaVersion != version {
			t.Errorf("shigolaVersion = %q, want %q", doc.ShigolaVersion, version)
		}
	})

	// A host that does not know its version must not have one invented for it:
	// "" is not a version, and reporting it as one is worse than saying nothing.
	t.Run("no version configured omits the member", func(t *testing.T) {
		var doc map[string]any
		if w := get(t, newRouterWithVersion(t, ""), "/", &doc); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		if got, ok := doc["shigolaVersion"]; ok {
			t.Errorf("shigolaVersion = %v, want the member absent", got)
		}
	})

	t.Run("the API definition names the build under info", func(t *testing.T) {
		var doc struct {
			Info struct {
				Version        string `json:"version"`
				ShigolaVersion string `json:"x-shigola-version"`
			} `json:"info"`
		}
		if w := get(t, newRouterWithVersion(t, version), "/api", &doc); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		if doc.Info.ShigolaVersion != version {
			t.Errorf("info.x-shigola-version = %q, want %q", doc.Info.ShigolaVersion, version)
		}

		// info.version is the version of the API, fixed by the specification
		// this surface implements. It does not move when the binary is rebuilt.
		if doc.Info.Version == version {
			t.Errorf("info.version = %q, but that is the API version, not the build", doc.Info.Version)
		}
		if doc.Info.Version == "" {
			t.Error("info.version is empty; OpenAPI requires it")
		}
	})

	// The parsed OpenAPI document is a package-level value shared by every
	// request and every service. Writing the version through it rather than
	// through a copy would leak this service's version into the next one's
	// document -- and into a host that set none.
	t.Run("one service's version does not leak into another's", func(t *testing.T) {
		var doc map[string]any
		if w := get(t, newRouterWithVersion(t, version), "/api", &doc); w.Code != http.StatusOK {
			t.Fatalf("priming request status = %d, want 200", w.Code)
		}

		doc = nil
		if w := get(t, newRouterWithVersion(t, ""), "/api", &doc); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		info, ok := doc["info"].(map[string]any)
		if !ok {
			t.Fatalf("info = %v, want an object", doc["info"])
		}
		if got, ok := info["x-shigola-version"]; ok {
			t.Errorf("info.x-shigola-version = %v, want the member absent", got)
		}
	})
}
