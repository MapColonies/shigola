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

	"github.com/dimfeld/httptreemux"
	"github.com/go-spatial/geom/slippy"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/observability"
	"github.com/go-spatial/tegola/tms"
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
	// so the encode and the cache key must both use it rather than the map's
	// default.
	m := c.Map
	m.TileMatrixSets = []*tms.TileMatrixSet{grid}

	m = m.FilterLayersByZoom(slippy.Zoom(tile.Z))
	if len(m.Layers) == 0 {
		// The tileset exists, this zoom simply holds nothing. An empty tile is
		// the honest answer: a 404 would tell a client the tileset is wrong.
		writeEmptyTile(w)
		return
	}

	key := cache.NewKey(grid, c.Map.Name, c.LayerName, uint(tile.Z), uint(tile.X), uint(tile.Y))

	// The same key the native routes use, so a tile seeded or served through
	// /maps/... is served here too rather than generated a second time.
	cacher := s.cfg.Atlas.GetCache()
	if cacher != nil {
		if cached, hit, err := cacher.Get(r.Context(), &key); err != nil {
			logf("ogc: reading from cache: %v", err)
		} else if hit {
			w.Header().Set("Tegola-Cache", "HIT")
			writeTile(w, cached)
			return
		}
	}

	ctx := context.WithValue(r.Context(), observability.ObserveVarMapName, m.Name)

	body, err := m.Encode(ctx, slippy.Tile{Z: slippy.Zoom(tile.Z), X: uint(tile.X), Y: uint(tile.Y)}, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "operation was canceled") {
			return
		}

		writeError(w, http.StatusInternalServerError, fmt.Errorf("marshalling tile: %w", err))
		return
	}

	if cacher != nil {
		w.Header().Set("Tegola-Cache", "MISS")
		if err := cacher.Set(r.Context(), &key, body); err != nil {
			logf("ogc: writing to cache: %v", err)
		}
	}

	writeTile(w, body)
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

	cols, rows, err := grid.MatrixSize(z)
	if err != nil {
		return tms.Tile{}, fmt.Errorf("tile matrix set %v has no tile matrix %v", grid.ID(), tileMatrix)
	}

	row, err := strconv.ParseInt(tileRow, 10, 64)
	if err != nil || row < 0 || row >= rows {
		return tms.Tile{}, fmt.Errorf("invalid tileRow (%v); tile matrix %v has %d rows", tileRow, tileMatrix, rows)
	}

	col, err := strconv.ParseInt(tileCol, 10, 64)
	if err != nil || col < 0 || col >= cols {
		return tms.Tile{}, fmt.Errorf("invalid tileCol (%v); tile matrix %v has %d columns", tileCol, tileMatrix, cols)
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
