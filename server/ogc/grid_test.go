package ogc_test

import (
	"net/http"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/server/ogc"
	"github.com/MapColonies/shigola/tms"
)

// TestGridlessMapServesNoTiles pins the half of MAPCO-11486 that is observable
// over HTTP: a map listing no tiling scheme offers no tileset and serves no
// tile.
//
// The surface used to answer such a map in WebMercatorQuad, because every read
// of a map's grids ended in atlas.DefaultTileGrid — which panics if the grid
// cannot be resolved, and so put a panic on the request path. Reading no grids
// as "none" rather than "the default one" is what takes it off that path. It
// also stops the server inventing a scheme the operator never configured: a
// tile served under a grid nothing declared is one no tileset document
// describes.
//
// Registration cannot produce such a map — it fills the list with every
// available grid when the config omits the key — so this is a guard on the seam,
// not a behaviour anyone is expected to configure.
func TestGridlessMapServesNoTiles(t *testing.T) {
	m := atlas.NewWebMercatorMap("gridless")
	m.TileMatrixSets = nil

	a := &atlas.Atlas{}
	a.AddMap(m)
	r := newRouterFor(t, a)

	var doc ogc.TileSets
	if w := get(t, r, "/collections/gridless/tiles", &doc); w.Code != http.StatusOK {
		t.Fatalf("GET /collections/gridless/tiles status = %d, want 200", w.Code)
	}

	if len(doc.Tilesets) != 0 {
		t.Errorf("tilesets = %d, want 0; a map naming no scheme offers none", len(doc.Tilesets))
	}

	for _, uri := range []string{
		"/collections/gridless/tiles/" + tms.WebMercatorQuad,
		"/collections/gridless/tiles/" + tms.WebMercatorQuad + "/0/0/0",
	} {
		if w := get(t, r, uri, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %v status = %d, want 404", uri, w.Code)
		}
	}
}
