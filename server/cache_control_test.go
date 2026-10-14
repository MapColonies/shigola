package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MapColonies/shigola/cache/memory"
	"github.com/MapColonies/shigola/server"
)

const tileURI = "/collections/test-map/tiles/WebMercatorQuad/10/3/2"

func cacheControlOf(t *testing.T, router http.Handler, uri string) (int, string) {
	t.Helper()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, uri, nil))

	return w.Code, w.Header().Get("Cache-Control")
}

func cacheControlRouter(t *testing.T, tiles, metadata string, headers map[string]string) http.Handler {
	t.Helper()

	savedTiles, savedMetadata, savedHeaders := server.TileCacheControl, server.MetadataCacheControl, server.Headers
	t.Cleanup(func() {
		server.TileCacheControl, server.MetadataCacheControl, server.Headers = savedTiles, savedMetadata, savedHeaders
	})
	server.TileCacheControl, server.MetadataCacheControl, server.Headers = tiles, metadata, headers

	a := newTestMapWithLayers(testLayer1, testLayer2, testLayer3)
	cacher, _ := memory.New(nil)
	a.SetCache(cacher)

	return server.NewRouter(a)
}

func TestCacheControlIsSetPerSurface(t *testing.T) {
	router := cacheControlRouter(t, "public, max-age=60", "public, max-age=300", map[string]string{})

	// the cache write is detached, so keep asking until the tile is a HIT
	seen := map[string]bool{}
	for deadline := time.Now().Add(2 * time.Second); !seen["HIT"] && time.Now().Before(deadline); {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tileURI, nil))

		pass := w.Header().Get("Shigola-Cache")
		seen[pass] = true
		if cc := w.Header().Get("Cache-Control"); w.Code != http.StatusOK || cc != "public, max-age=60" {
			t.Fatalf("tile (%v): got %d %q, expected 200 with the tile value", pass, w.Code, cc)
		}
	}
	if !seen["MISS"] || !seen["HIT"] {
		t.Errorf("tile: saw %v, expected both a MISS and a HIT", seen)
	}
	for _, uri := range []string{"/", "/collections", "/collections/test-map", "/tileMatrixSets", "/api"} {
		if code, cc := cacheControlOf(t, router, uri); code != http.StatusOK || cc != "public, max-age=300" {
			t.Errorf("%v: got %d %q, expected 200 with the metadata value", uri, code, cc)
		}
	}
}

func TestErrorResponsesAreNotCacheable(t *testing.T) {
	router := cacheControlRouter(t, "public, max-age=60", "public, max-age=300",
		map[string]string{"Cache-Control": "s-maxage=300"})

	for _, uri := range []string{
		"/collections/nope/tiles/WebMercatorQuad/10/3/2",         // 404
		"/collections/test-map/tiles/WebMercatorQuad/10/99999/2", // 400
		"/collections/nope",
	} {
		code, cc := cacheControlOf(t, router, uri)
		if code < 400 {
			t.Fatalf("%v: got %d, expected an error status", uri, code)
		}
		if cc != "no-store" {
			t.Errorf("%v: got Cache-Control %q on a %d, expected no-store", uri, cc, code)
		}
	}
}

func TestCacheControlDefaults(t *testing.T) {
	t.Run("nothing configured", func(t *testing.T) {
		router := cacheControlRouter(t, "", "", map[string]string{})

		if _, cc := cacheControlOf(t, router, tileURI); cc != server.DefaultTileCacheControl {
			t.Errorf("tile: got %q, expected the default %q", cc, server.DefaultTileCacheControl)
		}
		if _, cc := cacheControlOf(t, router, "/collections"); cc != "" {
			t.Errorf("metadata: got %q, expected none, as before", cc)
		}
	})

	t.Run("a generic Cache-Control header keeps working", func(t *testing.T) {
		router := cacheControlRouter(t, "", "", map[string]string{"Cache-Control": "s-maxage=300"})

		for _, uri := range []string{tileURI, "/collections"} {
			if _, cc := cacheControlOf(t, router, uri); cc != "s-maxage=300" {
				t.Errorf("%v: got %q, expected the generic header, as before", uri, cc)
			}
		}
	})
}

func TestCacheControlIsSetWhenTheHandlerWritesNothing(t *testing.T) {
	savedTiles := server.TileCacheControl
	t.Cleanup(func() { server.TileCacheControl = savedTiles })
	server.TileCacheControl = "public, max-age=60"

	silent := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	w := httptest.NewRecorder()
	server.CacheControlHandler(true, silent).ServeHTTP(w, httptest.NewRequest(http.MethodGet, tileURI, nil))

	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("got %q, expected the tile value on an empty 200", cc)
	}
}
