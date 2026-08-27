package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MapColonies/shigola/cache/memory"
	"github.com/MapColonies/shigola/server"
)

func TestMiddlewareGzipHandler(t *testing.T) {
	type tcase struct {
		uri                     string
		requestHeaders          map[string]string
		expectedResponseHeaders map[string]string
	}

	// our tests don't use the URIPrefix but our server is a singleton so we set
	// it to the default for these tests. Set once, here, rather than inside each
	// parallel subtest: NewRouter reads the same package global, so writing it
	// from every subtest is a data race — one the suite only started reporting
	// when CI began running -race.
	server.URIPrefix = "/"

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			var err error

			// setup a new atlas
			a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)
			cacher, _ := memory.New(nil)
			a.SetCache(cacher)

			// setup a new router
			router := server.NewRouter(a)

			// setup a new request
			r, err := http.NewRequest("GET", tc.uri, nil)
			if err != nil {
				t.Errorf("unexecpted err: %v", err)
				return
			}

			// add test case request headers
			for k, v := range tc.requestHeaders {
				r.Header.Add(k, v)
			}

			// new recorder to capture the response
			w := httptest.NewRecorder()

			// issue the request
			router.ServeHTTP(w, r)

			// check our response for the correct headers
			for k, v := range tc.expectedResponseHeaders {
				h := w.Header().Get(k)
				if h != v {
					t.Errorf("expected header (%v) to have value (%v) got (%v)", k, v, h)
					return
				}
			}

			// handle no requestHeader
			if len(tc.requestHeaders) == 0 {
				encoding := w.Header().Get("Content-Encoding")
				if encoding != "" {
					t.Errorf("expected Content-Encoding to not be set, got (%v)", encoding)
					return
				}
			}
		}
	}

	tests := map[string]tcase{
		"Accept-Encoding: gzip": {
			uri: "/collections/test-map/tiles/WebMercatorQuad/10/3/2",
			requestHeaders: map[string]string{
				"Accept-Encoding": "gzip",
			},
			expectedResponseHeaders: map[string]string{
				"Content-Encoding": "gzip",
			},
		},
		"Accept-Encoding: foo, gzip": {
			uri: "/collections/test-map/tiles/WebMercatorQuad/10/3/2",
			requestHeaders: map[string]string{
				"Accept-Encoding": "foo, gzip",
			},
			expectedResponseHeaders: map[string]string{
				"Content-Encoding": "gzip",
			},
		},
		"Accept-Encoding: gzip;q=0": {
			uri: "/collections/test-map/tiles/WebMercatorQuad/10/3/2",
			requestHeaders: map[string]string{
				"Accept-Encoding": "gzip;q=0",
			},
			expectedResponseHeaders: map[string]string{},
		},
		"Accept-Encoding: *": {
			uri: "/collections/test-map/tiles/WebMercatorQuad/10/3/2",
			requestHeaders: map[string]string{
				"Accept-Encoding": "*",
			},
			expectedResponseHeaders: map[string]string{
				"Content-Encoding": "gzip",
			},
		},
		"Accept-Encoding: foo, *": {
			uri: "/collections/test-map/tiles/WebMercatorQuad/10/3/2",
			requestHeaders: map[string]string{
				"Accept-Encoding": "foo, *",
			},
			expectedResponseHeaders: map[string]string{
				"Content-Encoding": "gzip",
			},
		},
		"Accept-Encoding: *;q=0": {
			uri: "/collections/test-map/tiles/WebMercatorQuad/10/3/2",
			requestHeaders: map[string]string{
				"Accept-Encoding": "*;q=0",
			},
			expectedResponseHeaders: map[string]string{},
		},
		"Accept-Encoding missing": {
			uri:                     "/collections/test-map/tiles/WebMercatorQuad/10/3/2",
			requestHeaders:          map[string]string{},
			expectedResponseHeaders: map[string]string{},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
