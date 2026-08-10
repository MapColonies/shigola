package server_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-spatial/geom/encoding/mvt"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/internal/faketier"
	"github.com/go-spatial/tegola/server"
)

// gzipTile is what the tile handler writes: already-gzipped MVT bytes.
func gzipTile(t *testing.T, body string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return buf.Bytes()
}

// TestFlushBeforeCacheWrite is the assertion that passes vacuously if written
// naively.
//
// A type-level check — var _ http.Flusher = (*tileCacheResponseWriter)(nil) —
// compiles happily while the flush no-ops in the assembled stack, which is the
// defect this exists to catch. So it drives a real request through
// HeadersHandler(GZipHandler(TileCacheHandler(...))) over a real connection,
// with a cache whose Set blocks, and asserts the client already has the tile.
//
// Both Accept-Encoding cases run, and that is not thoroughness. GZipHandler
// hands the cache middleware the original ResponseWriter for a gzip client and
// a *gzipDecompressResponseWriter for everyone else — so the gzip case passes
// with or without the second Flush, and would hide the bug on its own.
func TestFlushBeforeCacheWrite(t *testing.T) {
	type tcase struct {
		acceptEncoding string
		// clientGetsGzip says whether the body arrives still compressed.
		clientGetsGzip bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			const body = "a tile, of sorts"
			tile := gzipTile(t, body)

			// A cache that blocks in Set. Nothing releases it until the client
			// has its bytes, so if the flush does not work the read below
			// cannot complete.
			gate := faketier.NewGate()
			defer gate.Release()

			tier := faketier.New("blocking")
			tier.GateOn(faketier.OpSet, gate)

			a := &atlas.Atlas{}
			a.SetCache(tier)

			tileHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", mvt.MimeType)
				w.WriteHeader(http.StatusOK)
				w.Write(tile)
			})

			handler := server.HeadersHandler(server.GZipHandler(server.TileCacheHandler(a, tileHandler)))

			srv := httptest.NewServer(handler)
			defer srv.Close()

			req, err := http.NewRequest(http.MethodGet, srv.URL+"/maps/osm/1/1/1.pbf", nil)
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

			// Read only the first chunk. Reading to EOF would wait for the
			// handler to return, which is exactly what this must not require.
			type readResult struct {
				n   int
				buf []byte
				err error
			}
			read := make(chan readResult, 1)
			go func() {
				buf := make([]byte, len(tile)+len(body))
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

			// The bytes are the tile, in whichever form this client asked for.
			if tc.clientGetsGzip {
				if !bytes.HasPrefix(got.buf, tile[:2]) {
					t.Errorf("expected gzipped bytes, got %q", got.buf)
				}
			} else if !bytes.Contains(got.buf, []byte(body)) {
				t.Errorf("expected the decompressed tile, got %q", got.buf)
			}

			// Sanity: the write really was still blocked throughout.
			gate.WaitEntered(1)
			if _, ok := tier.Value(&cache.Key{MapName: "osm", Z: 1, X: 1, Y: 1}); ok {
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
// fast and names the type if either wrapper loses its Flush.
func TestResponseWritersAreFlushers(t *testing.T) {
	a := &atlas.Atlas{}
	a.SetCache(faketier.New("noop"))

	var (
		cacheWriter http.ResponseWriter
		gzipWriter  http.ResponseWriter
	)

	tileHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cacheWriter = w
		w.Header().Set("Content-Type", mvt.MimeType)
		w.WriteHeader(http.StatusOK)
		w.Write(gzipTile(t, "tile"))
	})

	gzipProbe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gzipWriter = w
		server.TileCacheHandler(a, tileHandler).ServeHTTP(w, r)
	})

	req := httptest.NewRequest(http.MethodGet, "/maps/osm/1/1/1.pbf", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0")
	server.GZipHandler(gzipProbe).ServeHTTP(httptest.NewRecorder(), req)

	if _, ok := gzipWriter.(http.Flusher); !ok {
		t.Errorf("%T is not an http.Flusher, so the tile cache middleware's flush no-ops for every non-gzip client", gzipWriter)
	}
	if _, ok := cacheWriter.(http.Flusher); !ok {
		t.Errorf("%T is not an http.Flusher", cacheWriter)
	}
}

// TestFlushWithoutAFlushableWriter — a response writer that cannot be flushed
// must degrade to today's behaviour rather than panicking, and say so once.
func TestFlushWithoutAFlushableWriter(t *testing.T) {
	a := &atlas.Atlas{}
	a.SetCache(faketier.New("noop"))

	tileHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", mvt.MimeType)
		w.WriteHeader(http.StatusOK)
		w.Write(gzipTile(t, "tile"))
	})

	req := httptest.NewRequest(http.MethodGet, "/maps/osm/1/1/1.pbf", nil)
	w := &unflushableWriter{ResponseRecorder: httptest.NewRecorder()}

	server.TileCacheHandler(a, tileHandler).ServeHTTP(w, req)

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
