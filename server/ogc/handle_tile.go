package ogc

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/dimfeld/httptreemux"
	"github.com/go-spatial/geom/slippy"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/observability"
	"github.com/MapColonies/shigola/tms"
)

// HandleTile serves one vector tile of one collection in one tiling scheme.
//
// The path is {tileMatrix}/{tileRow}/{tileCol} — z/y/x, transposed from tegola's
// native z/x/y. Reading the segments in the wrong order silently serves the
// wrong tile, so they are named for what they are throughout.
func (s *Service) HandleTile(w http.ResponseWriter, r *http.Request) {
	if _, err := negotiate(r, FormatMVT); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	params := httptreemux.ContextParams(r.Context())

	c, err := s.collection(params["collection_id"])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	grid, err := s.collectionGrid(c, params["tile_matrix_set_id"])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	tile, err := parseTilePath(grid, params["tile_matrix"], params["tile_row"], params["tile_col"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// The collection's map may offer several schemes; this request named one,
	// and grid is handed to the encode and to the cache key below so that both
	// use it. Nothing on this path reads a default off the map.
	m := c.Map.FilterLayersByZoom(slippy.Zoom(tile.Z))
	if len(m.Layers) == 0 {
		// The tileset exists, this zoom simply holds nothing. An empty tile is
		// the honest answer: a 404 would tell a client the tileset is wrong.
		writeEmptyTile(w)
		return
	}

	key, err := cache.NewKey(grid, c.Map.Name, c.LayerName, uint(tile.Z), uint(tile.X), uint(tile.Y))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// The same key `shigola cache seed` writes, so a seeded tile is served
	// rather than generated a second time.
	//
	// Only for a request the key can actually describe: see cacheable.
	cacher := s.cfg.Atlas.GetCache()
	if !cacheable(r) {
		cacher = nil
	}
	if cacher != nil {
		if cached, hit, err := cacher.Get(r.Context(), &key); err != nil {
			logf("ogc: reading from cache: %v", err)
		} else if hit {
			w.Header().Set("Shigola-Cache", "HIT")
			writeTile(w, cached)
			return
		}
	}

	ctx := context.WithValue(r.Context(), observability.ObserveVarMapName, m.Name)

	body, err := m.Encode(ctx, grid, slippy.Tile{Z: slippy.Zoom(tile.Z), X: uint(tile.X), Y: uint(tile.Y)}, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "operation was canceled") {
			return
		}

		writeError(w, http.StatusInternalServerError, fmt.Errorf("marshalling tile: %w", err))
		return
	}

	if cacher != nil {
		w.Header().Set("Shigola-Cache", "MISS")
	}

	writeTile(w, body)

	if cacher != nil {
		cacheAfterResponse(w, r, cacher, &key, body)
	}
}

// cacheAfterResponse writes a tile to the cache once the client has it.
//
// The order matters, and so does the flush. net/http buffers the response, so a
// tile small enough to sit in that buffer does not leave the process until the
// handler returns -- which means a cache write on the way out delays the client
// by however long the slowest tier takes, even though nothing the client asked
// for depends on it.
//
// This was the native tile-cache middleware's job until MAPCO-11484 deleted it,
// and it moves here with the caching itself. Every registered cache writes
// through a bounded pool that returns immediately, so in a normal deployment
// the write is already off the response path; doing it in this order is what
// makes that a property of this handler rather than of how the cache happened to
// be assembled.
func cacheAfterResponse(w http.ResponseWriter, r *http.Request, cacher cache.Interface, key *cache.Key, body []byte) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	} else {
		// Fires once per process: a response writer that cannot be flushed is a
		// property of the assembled middleware stack, not of one request, so a
		// line per request would say nothing extra.
		warnUnflushable.Do(func() {
			logf("ogc: %T is not an http.Flusher, so tile bytes wait for the handler to return", w)
		})
	}

	// A canceled request has no client left to serve it, and its context is the
	// one the write would use.
	if r.Context().Err() != nil {
		return
	}

	if err := cacher.Set(r.Context(), key, body); err != nil {
		logf("ogc: writing to cache: %v", err)
	}
}

var warnUnflushable sync.Once

// cacheable reports whether a request's tile may be read from or written to the
// cache.
//
// The key is {scheme}/{map}/{layer}/{z}/{x}/{y}: it says nothing about the query
// string. That is sound only while nothing in the query string can change the
// bytes — so a request carrying anything this surface does not own is served
// uncached rather than answered from, or allowed to populate, an entry that
// cannot describe it.
//
// "f" is excluded because it is this surface's own format selector, and every
// value it accepts names the same media type and yields the same bytes.
//
// The removed native routes took the same position more bluntly: their
// middleware skipped the cache for any query string at all, because a shigola
// map can declare query parameters that change what a tile contains (see
// atlas.SeedMapTile, which refuses to seed such a map for the same reason).
// Nothing reads those parameters now -- the route that did went with the
// middleware -- and this surface passes none to Encode, so no request can reach
// a different rendering. Serving a parameterised request uncached is what keeps
// that safe if it ever changes: tiles must not already be pooled under a key
// that ignores it.
func cacheable(r *http.Request) bool {
	for name := range r.URL.Query() {
		if name != "f" {
			return false
		}
	}

	return true
}

// parseTilePath reads an OGC tile path and checks it against the scheme's matrix
// at that zoom.
//
// Rows and columns are bounded independently: a tiling scheme need not be a
// square pyramid — WorldCRS84Quad is twice as wide as it is tall.
func parseTilePath(grid *tms.TileMatrixSet, tileMatrix, tileRow, tileCol string) (tms.Tile, error) {
	z, err := strconv.Atoi(tileMatrix)
	if err != nil {
		return tms.Tile{}, fmt.Errorf("invalid tileMatrix (%v)", tileMatrix)
	}

	// Parsed before either is range-checked, so that a non-numeric row and an
	// out-of-range one are told apart: ValidateTile bounds integers, it does not
	// read them.
	row, err := strconv.ParseInt(tileRow, 10, 64)
	if err != nil {
		return tms.Tile{}, fmt.Errorf("invalid tileRow (%v)", tileRow)
	}

	col, err := strconv.ParseInt(tileCol, 10, 64)
	if err != nil {
		return tms.Tile{}, fmt.Errorf("invalid tileCol (%v)", tileCol)
	}

	if err := grid.ValidateTile(z, col, row); err != nil {
		var outside tms.ErrTileOutsideMatrix
		if !errors.As(err, &outside) {
			return tms.Tile{}, fmt.Errorf("tile matrix set %v has no tile matrix %v", grid.ID(), tileMatrix)
		}

		// Reported in the standard's vocabulary: this surface calls them rows
		// and columns, and the client asked in those terms.
		if outside.Axis == tms.AxisY {
			return tms.Tile{}, fmt.Errorf("invalid tileRow (%v); tile matrix %v has %d rows", tileRow, tileMatrix, outside.Rows)
		}

		return tms.Tile{}, fmt.Errorf("invalid tileCol (%v); tile matrix %v has %d columns", tileCol, tileMatrix, outside.Cols)
	}

	return tms.Tile{Z: z, X: col, Y: row}, nil
}

// writeTile serves encoded tile bytes.
//
// The bytes are gzipped — atlas.Map.Encode compresses, and the cache stores what
// it produced — which the server's gzip middleware either declares with a
// Content-Encoding header or decompresses, according to the request.
func writeTile(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", MediaTypeMVT)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body); err != nil {
		logf("ogc: writing tile: %v", err)
	}
}

// emptyTile is a valid, empty, gzipped MVT: what a tileset serves where it holds
// no data at the requested zoom.
var emptyTile = gzipEmptyMVT()

func writeEmptyTile(w http.ResponseWriter) { writeTile(w, emptyTile) }

// gzipEmptyMVT builds the gzipped encoding of an MVT tile with no layers.
//
// An empty protobuf message is zero bytes, so this is gzip framing around
// nothing — which is exactly what atlas.Map.Encode produces for a map with no
// layers at a zoom, and what a client's decoder expects.
func gzipEmptyMVT() []byte {
	var buf bytes.Buffer

	w := gzip.NewWriter(&buf)
	if err := w.Close(); err != nil {
		// Writing to a bytes.Buffer cannot fail.
		panic("ogc: encoding the empty tile: " + err.Error())
	}

	return buf.Bytes()
}
