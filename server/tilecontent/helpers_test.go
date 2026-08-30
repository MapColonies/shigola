package tilecontent_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/mvttest"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/provider/postgis"
	"github.com/MapColonies/shigola/server"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom"
)

// dataTestEnv gates the tile-content checks.
//
// They have their own switch rather than riding on RUN_POSTGIS_TESTS so that
// exactly one CI job runs them. Sharing that gate would run them a second time
// inside the general test job, where a failure says only that some test failed.
const dataTestEnv = "RUN_DATA_TESTS"

// extent is the tile-space grid every assertion here is expressed in.
const extent = 4096

// mvtBuffer is ST_AsMVTGeom's default buffer, in tile units.
//
// It is 256, not 0. The epic assumed no buffer; the road crossing the tile edge
// is what showed otherwise, arriving clipped at extent+256 rather than at the
// extent.
//
// It decides clipping, not selection. Which row reaches ST_AsMVTGeom at all is
// decided earlier, by the layer SQL's `WHERE geom && !BBOX!` against the
// unbuffered envelope, so a feature outside the tile is excluded however close
// to the edge it sits. The two are easy to conflate and this comment exists
// because an earlier version of it did.
const mvtBuffer = 256

// pgConfig is the connection half of a provider config, from the environment
// the CI job supplies.
func pgConfig() dict.Dict {
	return dict.Dict{
		postgis.ConfigKeyURI: ttools.GetEnvDefault(
			"PGURI",
			"postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable",
		),
		postgis.ConfigKeySSLMode: ttools.GetEnvDefault("PGSSLMODE", "disable"),
	}
}

// newAtlas builds a one-map atlas over the given provider layers, served in
// both tiling schemes.
//
// The map's bounds are the whole globe rather than the default, which stops at
// mercator's own top latitude and would put polar ground outside the map that
// is supposed to serve it.
func newAtlas(t *testing.T, collection string, layers []map[string]any) *atlas.Atlas {
	t.Helper()

	cfg := pgConfig()
	cfg[postgis.ConfigKeyName] = collection + "_provider"
	cfg[postgis.ConfigKeyLayers] = layers

	prvd, err := postgis.NewMVTTileProvider(cfg, nil)
	if err != nil {
		t.Fatalf("building the %v provider: %v", collection, err)
	}

	m := atlas.NewWebMercatorMap(collection)
	m.Bounds = &geom.Extent{-180, -90, 180, 90}
	m.SetMVTProvider(collection+"_provider", prvd)

	m.TileMatrixSets = nil
	for _, id := range []string{tms.WebMercatorQuad, tms.WorldCRS84Quad} {
		grid, err := tms.Get(id)
		if err != nil {
			t.Fatalf("tms.Get(%v) = %v, want nil", id, err)
		}
		m.TileMatrixSets = append(m.TileMatrixSets, grid)
	}

	for _, l := range layers {
		name, _ := l[postgis.ConfigKeyLayerName].(string)
		m.Layers = append(m.Layers, atlas.Layer{
			Name:              name,
			ProviderLayerName: name,
			MinZoom:           0,
			MaxZoom:           22,
		})
	}

	a := &atlas.Atlas{}
	a.AddMap(m)

	return a
}

func newServer(t *testing.T, a *atlas.Atlas) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(server.NewRouter(a))
	t.Cleanup(srv.Close)

	return srv
}

// tileURI builds an OGC tile path. The segments are tileMatrix, tileRow,
// tileCol -- z/y/x, transposed from the z/x/y the rest of the codebase uses,
// which is why they are named rather than positional here.
func tileURI(collection, scheme string, tileMatrix, tileRow, tileCol int) string {
	return fmt.Sprintf("/collections/%s/tiles/%s/%d/%d/%d", collection, scheme, tileMatrix, tileRow, tileCol)
}

// noCompressionClient is a client that negotiates nothing of its own, so what
// arrives is what the server sent.
func noCompressionClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}

// response is one raw answer, before anything is decoded.
type response struct {
	Status   int
	Header   http.Header
	Body     []byte
	Encoding string
}

// get asks for one tile and returns the raw answer.
//
// acceptEncoding is sent verbatim when non-empty; the empty string sends no
// header at all. The three cases are different code in the gzip middleware, and
// what a client receives differs between them.
func get(t *testing.T, srv *httptest.Server, uri, acceptEncoding string) response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, srv.URL+uri, nil)
	if err != nil {
		t.Fatalf("building the request for %v: %v", uri, err)
	}
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}

	// DisableCompression, and it is load-bearing. Left on, net/http adds
	// Accept-Encoding: gzip to any request that carries no such header, then
	// decompresses the answer and strips Content-Encoding before the caller
	// sees it. A check on what the server sent would then be reading what the
	// transport did -- and the "advertising nothing" case in particular would
	// pass whatever the server chose.
	resp, err := noCompressionClient().Do(req)
	if err != nil {
		t.Fatalf("GET %v: %v", uri, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %v: %v", uri, err)
	}

	return response{
		Status:   resp.StatusCode,
		Header:   resp.Header,
		Body:     body,
		Encoding: resp.Header.Get("Content-Encoding"),
	}
}

// fetch asks for one tile, insists it arrives as a gzipped vector tile, and
// returns it decoded.
func fetch(t *testing.T, srv *httptest.Server, collection, scheme string, tileMatrix, tileRow, tileCol int) mvttest.Tile {
	t.Helper()

	uri := tileURI(collection, scheme, tileMatrix, tileRow, tileCol)
	resp := get(t, srv, uri, "gzip")

	if resp.Status != http.StatusOK {
		t.Fatalf("GET %v status = %d, want 200", uri, resp.Status)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/vnd.mapbox-vector-tile" {
		t.Errorf("GET %v Content-Type = %q, want the vector tile media type", uri, got)
	}
	if resp.Encoding != "gzip" {
		t.Errorf("GET %v Content-Encoding = %q, want gzip", uri, resp.Encoding)
	}

	tile := mvttest.Decode(t, resp.Body)

	// What came back, before anything is asserted about it. A check that
	// reports only its own verdict cannot tell "the tile held what I expected"
	// apart from "the tile held nothing and I expected nothing", and the second
	// is how a fixture that quietly stopped loading looks.
	t.Logf("GET %v -> 200 %s, %d bytes gzipped, holding %s",
		uri, resp.Header.Get("Content-Type"), len(resp.Body), tile.Summary())

	return tile
}

// named returns the single feature in the layer whose name tag is name.
func named(t *testing.T, tile mvttest.Tile, layer, name string) mvttest.Feature {
	t.Helper()

	l, ok := tile.Layer(layer)
	if !ok {
		t.Fatalf("layer %q is missing; the tile holds %v", layer, tile.LayerNames())
	}

	f, ok := l.FeatureByTag("name", mvttest.String(name))
	if !ok {
		t.Fatalf("want exactly one feature named %q in %q; the tile holds %v", name, layer, tile.Summary())
	}

	if !f.HasID {
		t.Errorf("feature %q carries no id; the layer's id_fieldname did not reach ST_AsMVT", name)
	}

	return f
}

// at asserts a named feature's whole geometry, part by part.
func at(t *testing.T, tile mvttest.Tile, layer, name string, want ...mvttest.Part) {
	t.Helper()

	f := named(t, tile, layer, name)

	if len(f.Geom) != len(want) {
		t.Errorf("feature %q has %d geometry parts, want %d: %v", name, len(f.Geom), len(want), f.Geom)
		return
	}
	for i := range want {
		if f.Geom[i].String() != want[i].String() {
			t.Errorf("feature %q part %d = %v, want %v", name, i, f.Geom[i], want[i])
		}
	}

	// Inclusive of the buffer: geometry legitimately runs past the extent by up
	// to ST_AsMVTGeom's default 256, which is where the clipped road sits.
	for _, part := range f.Geom {
		for _, p := range part.Points {
			if p.X < -mvtBuffer || p.X > extent+mvtBuffer || p.Y < -mvtBuffer || p.Y > extent+mvtBuffer {
				t.Errorf("feature %q has %v outside the tile grid plus its buffer", name, p)
			}
		}
	}

	if !t.Failed() {
		t.Logf("  ok  %-13s id=%d geometry %v", name, f.ID, f.Geom)
	}
}

// absent asserts no feature of that name is in this tile.
//
// Only this tile: whether the scheme can reach the feature at all is a
// different claim, and the caller makes it by asking for every tile that could
// hold it.
func absent(t *testing.T, tile mvttest.Tile, layer, name string) {
	t.Helper()

	l, ok := tile.Layer(layer)
	if !ok {
		t.Logf("  ok  %-13s absent from this tile, which holds no %v layer at all", name, layer)
		return
	}
	// Any match, not exactly one. FeatureByTag reports ok only when a single
	// feature matches, so asking it would let two features sharing a name pass
	// as absent -- which is the case an absence assertion most needs to catch.
	for _, f := range l.Features {
		if v, ok := f.Tags["name"]; ok && v == mvttest.String(name) {
			t.Errorf("feature %q is present in %q, want it absent; the tile holds %v", name, layer, tile.Summary())
			return
		}
	}

	t.Logf("  ok  %-13s absent from this tile", name)
}

// providerLayer is the config for one MVT layer of a fixture.
func providerLayer(name, geomType, sql string) map[string]any {
	return map[string]any{
		postgis.ConfigKeyLayerName:   name,
		postgis.ConfigKeyGeomIDField: "fid",
		postgis.ConfigKeyGeomField:   "geom",
		// Declared rather than inferred: startup inference reads the layer's SQL
		// back, and a query ending in ST_AsMVTGeom returns tile-space geometry
		// it cannot type.
		postgis.ConfigKeyGeomType: geomType,
		postgis.ConfigKeySRID:     4326,
		postgis.ConfigKeySQL:      sql,
	}
}
