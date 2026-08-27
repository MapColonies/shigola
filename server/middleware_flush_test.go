package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tms"
)

// ogcTileURI is one tile of the whole-map collection, in the map's own scheme.
//
// z/y/x, transposed from the native routes this replaced: the tile below is
// z=4 x=2 y=3.
var ogcTileURI = "/collections/" + testMapName + "/tiles/WebMercatorQuad/4/3/2"

// ogcTileKey is the cache key that tile is filed under.
func ogcTileKey(t *testing.T) *cache.Key {
	t.Helper()

	grid, err := tms.Get("WebMercatorQuad")
	if err != nil {
		t.Fatalf("tms.Get: %v", err)
	}

	key, err := cache.NewKey(grid, testMapName, "", 4, 2, 3)
	if err != nil {
		t.Fatalf("cache.NewKey: %v", err)
	}

	return &key
}

// TestFlushBeforeCacheWrite is the assertion that passes vacuously if written
// naively.
//
// A type-level check — that the response writer implements http.Flusher —
// compiles happily while the flush no-ops in the assembled stack, which is the
// defect this exists to catch. So it drives a real request through the router as
// the server builds it, over a real connection, with a cache whose Set blocks,
// and asserts the client already has the tile.
//
// The property was the native tile-cache middleware's until MAPCO-11484 deleted
// it, and moved to ogc.cacheAfterResponse with the caching itself. What is under
// test is unchanged: a slow tier must not delay a tile the client is waiting for.
//
// Both Accept-Encoding cases run, and that is not thoroughness. GZipHandler
// hands the tile handler the original ResponseWriter for a gzip client and a
// *gzipDecompressResponseWriter for everyone else — so the gzip case passes with
// or without a working flush, and would hide the bug on its own.
func TestFlushBeforeCacheWrite(t *testing.T) {
	type tcase struct {
		acceptEncoding string
		// clientGetsGzip says whether the body arrives still compressed.
		clientGetsGzip bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			// A cache that blocks in Set. Nothing releases it until the client
			// has its bytes, so if the flush does not work the read below
			// cannot complete.
			gate := faketier.NewGate()
			defer gate.Release()

			tier := faketier.New("blocking")
			tier.GateOn(faketier.OpSet, gate)

			server.HostName = &url.URL{Host: serverHostName}
			server.URIPrefix = "/"

			a := newTestMapWithLayers(testLayer1)
			a.SetCache(tier)

			srv := httptest.NewServer(server.NewRouter(a))
			defer srv.Close()

			req, err := http.NewRequest(http.MethodGet, srv.URL+ogcTileURI, nil)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			// Set it explicitly so the transport neither adds its own nor
			// transparently decompresses the response.
			req.Header.Set("Accept-Encoding", tc.acceptEncoding)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			resp, err := http.DefaultClient.Do(req.WithContext(ctx))
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			// Read only the first chunk. Reading to EOF would wait for the
			// handler to return, which is exactly what this must not require.
			type readResult struct {
				n   int
				buf []byte
				err error
			}
			read := make(chan readResult, 1)
			go func() {
				buf := make([]byte, 4096)
				n, err := resp.Body.Read(buf)
				read <- readResult{n: n, buf: buf[:max(n, 0)], err: err}
			}()

			var got readResult
			select {
			case got = <-read:
			case <-time.After(5 * time.Second):
				t.Fatal("the client got no bytes while the cache write was blocked — the response was not flushed")
			}

			if got.n == 0 {
				t.Fatalf("read returned no bytes: %v", got.err)
			}

			// gzip's magic bytes, either declared to this client or already
			// removed on its behalf.
			if hasGzipMagic := bytes.HasPrefix(got.buf, []byte{0x1f, 0x8b}); hasGzipMagic != tc.clientGetsGzip {
				t.Errorf("body starts %x; gzipped = %v, want %v", got.buf[:min(2, len(got.buf))], hasGzipMagic, tc.clientGetsGzip)
			}

			// Sanity: the write really was still blocked throughout.
			gate.WaitEntered(1)
			if _, ok := tier.Value(ogcTileKey(t)); ok {
				t.Error("the cache write completed, so this proved nothing")
			}
		}
	}

	tests := map[string]tcase{
		// GZipHandler passes the original ResponseWriter straight through.
		"gzip": {acceptEncoding: "gzip", clientGetsGzip: true},
		// GZipHandler substitutes *gzipDecompressResponseWriter, which had no
		// Flush at all — this is the case the fix is for, and it is ordinary
		// traffic rather than a corner case.
		"gzip refused": {acceptEncoding: "gzip;q=0", clientGetsGzip: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestResponseWritersAreFlushers is the cheap check. It is not a substitute for
// the test above — it is what passed while the flush no-opped — but it fails
// fast and names the type if the wrapper loses its Flush.
func TestResponseWritersAreFlushers(t *testing.T) {
	var gzipWriter http.ResponseWriter

	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gzipWriter = w
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, ogcTileURI, nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0")
	server.GZipHandler(probe).ServeHTTP(httptest.NewRecorder(), req)

	if _, ok := gzipWriter.(http.Flusher); !ok {
		t.Errorf("%T is not an http.Flusher, so the tile handler's flush no-ops for every non-gzip client", gzipWriter)
	}
}

// TestFlushWithoutAFlushableWriter — a response writer that cannot be flushed
// must degrade to serving the tile on the handler's return rather than
// panicking.
func TestFlushWithoutAFlushableWriter(t *testing.T) {
	server.HostName = &url.URL{Host: serverHostName}
	server.URIPrefix = "/"

	a := newTestMapWithLayers(testLayer1)
	a.SetCache(faketier.New("noop"))

	req := httptest.NewRequest(http.MethodGet, ogcTileURI, nil)
	w := &unflushableWriter{ResponseRecorder: httptest.NewRecorder()}

	server.NewRouter(a).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, expected 200", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("no body was written")
	}
}

// unflushableWriter hides httptest.ResponseRecorder's Flush.
type unflushableWriter struct {
	*httptest.ResponseRecorder
}

func (w *unflushableWriter) Header() http.Header { return w.ResponseRecorder.Header() }
func (w *unflushableWriter) Write(b []byte) (int, error) {
	return w.ResponseRecorder.Write(b)
}
func (w *unflushableWriter) WriteHeader(code int) { w.ResponseRecorder.WriteHeader(code) }

var _ io.Writer = (*unflushableWriter)(nil)
