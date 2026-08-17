package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MapColonies/shigola/cache/memory"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tms"
)

func TestMiddlewareTileCacheHandler(t *testing.T) {
	type tcase struct {
		uri       string
		uriPrefix string
		gridID    string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			var err error

			if tc.uriPrefix != "" {
				server.URIPrefix = tc.uriPrefix
			} else {
				server.URIPrefix = "/"
			}

			a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)
			if tc.gridID == tms.WorldCRS84Quad {
				layer := testLayer1
				layer.MinZoom = 0
				layer.MaxZoom = 20
				a = newTestMapWithGrid(tms.WorldCRS84Quad, layer)
			}
			cacher, _ := memory.New(nil)
			a.SetCache(cacher)

			w, router, err := doRequest(t, a, http.MethodGet, tc.uri, nil)
			if err != nil {
				t.Errorf("error making request, expected nil got %v", err)
				return
			}

			// first response we expect the cache to MISS
			if w.Header().Get("Shigola-Cache") != "MISS" {
				t.Errorf("header Shigola-Cache, expected MISS got %v", w.Header().Get("Shigola-Cache"))
				return
			}

			// play the request again to get a HIT
			r, err := http.NewRequest("GET", tc.uri, nil)
			if err != nil {
				t.Errorf("error making request, expected nil got %v", err)
				return
			}

			w = httptest.NewRecorder()
			router.ServeHTTP(w, r)

			if w.Header().Get("Shigola-Cache") != "HIT" {
				t.Errorf("Tegoal-Cache, expected HIT got %v", w.Header().Get("Shigola-Cache"))
				return
			}
		}
	}

	tests := map[string]tcase{
		"map": {
			uri: "/maps/test-map/10/2/3.pbf",
		},
		"map layer": {
			uri: "/maps/test-map/test-layer/4/2/3.pbf",
		},
		"map and uri prefix": {
			uri:       "/tegola/maps/test-map/10/2/3.pbf",
			uriPrefix: "/tegola",
		},
		"map layer and uri prefix": {
			uri:       "/tegola/maps/test-map/test-layer/4/2/3.pbf",
			uriPrefix: "/tegola",
		},
		"world crs84 high x": {
			uri:    "/maps/test-map/test-layer/16/78212/21154.pbf",
			gridID: tms.WorldCRS84Quad,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestMiddlewareTileCacheHandlerIgnoreParams(t *testing.T) {
	type tcase struct {
		uri       string
		uriPrefix string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			var err error

			if tc.uriPrefix != "" {
				server.URIPrefix = tc.uriPrefix
			} else {
				server.URIPrefix = "/"
			}

			a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)
			cacher, _ := memory.New(nil)
			a.SetCache(cacher)

			w, router, err := doRequest(t, a, http.MethodGet, tc.uri, nil)
			if err != nil {
				t.Errorf("error making request, expected nil got %v", err)
				return
			}

			// we expect the cache to not being used
			if w.Header().Get("Shigola-Cache") != "" {
				t.Errorf("no header Shigola-Cache is expected, got %v", w.Header().Get("Shigola-Cache"))
				return
			}

			// play the request again
			r, err := http.NewRequest("GET", tc.uri, nil)
			if err != nil {
				t.Errorf("error making request, expected nil got %v", err)
				return
			}

			w = httptest.NewRecorder()
			router.ServeHTTP(w, r)

			if w.Header().Get("Shigola-Cache") != "" {
				t.Errorf("no header Shigola-Cache is expected, got %v", w.Header().Get("Shigola-Cache"))
				return
			}
		}
	}

	tests := map[string]tcase{
		"map params": {
			uri: "/maps/test-map/10/2/3.pbf?param=value",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
