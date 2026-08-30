package tilecontent_test

import (
	"net/http"

	"slices"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/internal/mvttest"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/tms"
)

// The tile-content fixture, as testdata/postgis/postgis-tile-content.sql places
// it: three layers, eight features, one column of each MVT value type.
const contentCollection = "content"

// The tile under test: WorldCRS84Quad zoom 3, row 2, column 10, spanning
// longitude 45..67.5 and latitude 22.5..45.
//
// Row and column differ, and the column is deliberately larger than the row
// count. WorldCRS84Quad is twice as wide as it is tall, so at zoom 3 there are
// 16 columns and 8 rows: column 10 exists and row 10 does not. A handler that
// read the two path segments the wrong way round would ask for row 10 and be
// rejected, so it fails every assertion here rather than serving a plausible
// tile quietly. TestTilePathOrder makes that guard explicit.
const (
	contentZoom = 3
	contentRow  = 2
	contentCol  = 10
)

func newContentAtlas(t *testing.T) *atlas.Atlas {
	t.Helper()

	return newAtlas(t, contentCollection, []map[string]any{
		providerLayer("places", "point",
			"SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid, name, rank, score, active, note FROM tile_content_places WHERE geom && !BBOX!"),
		providerLayer("roads", "linestring",
			"SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid, name, lanes FROM tile_content_roads WHERE geom && !BBOX!"),
		providerLayer("far", "point",
			"SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid, name FROM tile_content_far WHERE geom && !BBOX!"),
	})
}

// centreTags is the centre feature's row, as a client should read it. Stated
// once because it is the same row in both tiling schemes -- which is itself
// asserted, since a grid decides where a feature sits and not what it holds.
var centreTags = map[string]mvttest.Value{
	"name":   mvttest.String("centre"),
	"rank":   mvttest.Uint(10),
	"score":  mvttest.Double(1.5),
	"active": mvttest.Bool(true),
	"note":   mvttest.String("centre note"),
}

func point(x, y int32) mvttest.Part {
	return mvttest.Part{Points: []mvttest.Point{{X: x, Y: y}}}
}

func line(pts ...mvttest.Point) mvttest.Part { return mvttest.Part{Points: pts} }

// assertLayers pins the layer set exactly, rather than checking the ones the
// test happened to think of.
func assertLayers(t *testing.T, tile mvttest.Tile, want ...string) {
	t.Helper()

	got := tile.LayerNames()
	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("layers = %v, want exactly %v", got, want)
		return
	}

	t.Logf("  ok  layers are exactly %v", want)
}

// assertFeatureIDs pins a layer's feature ids exactly.
//
// Cardinality belongs here rather than in the golden alone. The golden catches
// a feature appearing or disappearing, but the golden is the thing that moves
// when someone blesses a regeneration; an assertion that every feature carries
// an id holds just as well for five features as for fifty.
func assertFeatureIDs(t *testing.T, tile mvttest.Tile, layer string, want ...uint64) {
	t.Helper()

	l, ok := tile.Layer(layer)
	if !ok {
		t.Errorf("layer %q is missing; the tile holds %v", layer, tile.LayerNames())
		return
	}

	got := l.FeatureIDs()
	if !slices.Equal(got, want) {
		t.Errorf("layer %q feature ids = %v, want exactly %v", layer, got, want)
		return
	}

	t.Logf("  ok  %-13s feature ids are exactly %v", layer, want)
}

// assertTags compares a feature's whole tag set against a literal.
//
// Values, not only keys. Feature ids and geometry prove the right rows were
// selected and placed where they belong; the values are what a client actually
// reads off them, and a key check holds just as well when every value is wrong
// or when two features have swapped rows.
func assertTags(t *testing.T, tile mvttest.Tile, layer, name string, want map[string]mvttest.Value) {
	t.Helper()

	f := named(t, tile, layer, name)

	if len(f.Tags) != len(want) {
		t.Errorf("feature %q has tags %v, want exactly %v", name, f.Tags, want)
		return
	}
	for k, w := range want {
		got, ok := f.Tags[k]
		if !ok {
			t.Errorf("feature %q has no tag %q; it has %v", name, k, f.Tags)
			continue
		}
		if got != w {
			t.Errorf("feature %q tag %q = %v, want %v", name, k, got, w)
		}
	}

	if !t.Failed() {
		t.Logf("  ok  %-13s tags %v", name, want)
	}
}

// TestTileContent asserts what one served tile holds, down to the coordinates
// and the attribute values (MAPCO-11547).
func TestTileContent(t *testing.T) {
	ttools.ShouldSkip(t, dataTestEnv)

	srv := newServer(t, newContentAtlas(t))
	tile := fetch(t, srv, contentCollection, tms.WorldCRS84Quad, contentZoom, contentRow, contentCol)

	t.Run("the tile carries exactly these layers and features", func(t *testing.T) {
		// far is absent entirely: every one of its rows is outside this tile,
		// so ST_AsMVT emits no layer for it at all.
		assertLayers(t, tile, "places", "roads")
		assertFeatureIDs(t, tile, "places", 1, 2, 3, 5)
		assertFeatureIDs(t, tile, "roads", 1, 2)

		// The named exclusions, stated literally rather than left to the counts.
		absent(t, tile, "places", "outside")
		absent(t, tile, "far", "far a")
	})

	t.Run("every feature sits where the arithmetic says", func(t *testing.T) {
		// x = (lon - 45) / 22.5 * 4096, y = (45 - lat) / 22.5 * 4096.
		at(t, tile, "places", "centre", point(2048, 2048))
		at(t, tile, "places", "probe", point(2248, 1416))
		at(t, tile, "places", "nulltag", point(3072, 1024))
		at(t, tile, "places", "midband", point(2048, 512))

		at(t, tile, "roads", "inside", line(mvttest.Point{X: 1024, Y: 1024}, mvttest.Point{X: 2048, Y: 1024}))

		// Clipped where it leaves the tile -- at the extent plus ST_AsMVTGeom's
		// default 256 buffer, not at the extent. The epic assumed no buffer;
		// this is the assertion that says otherwise.
		at(t, tile, "roads", "crossing",
			line(mvttest.Point{X: 3072, Y: 2048}, mvttest.Point{X: extent + mvtBuffer, Y: 2048}))
	})

	t.Run("attribute values are what the rows hold", func(t *testing.T) {
		assertTags(t, tile, "places", "centre", centreTags)
		assertTags(t, tile, "places", "probe", map[string]mvttest.Value{
			"name": mvttest.String("probe"), "rank": mvttest.Uint(20),
			"score": mvttest.Double(2.25), "active": mvttest.Bool(false),
			"note": mvttest.String("probe note"),
		})
		assertTags(t, tile, "roads", "crossing", map[string]mvttest.Value{
			"name": mvttest.String("crossing"), "lanes": mvttest.Uint(2),
		})
	})

	t.Run("a null attribute is omitted from the feature, not from the layer", func(t *testing.T) {
		// ST_AsMVT drops a null-valued tag from the feature that holds the null,
		// while the key stays in the layer's dictionary because other features
		// still carry it. Pinned rather than discovered.
		assertTags(t, tile, "places", "nulltag", map[string]mvttest.Value{
			"name": mvttest.String("nulltag"), "rank": mvttest.Uint(30),
			"score": mvttest.Double(3.75), "active": mvttest.Bool(true),
		})

		places, ok := tile.Layer("places")
		if !ok {
			t.Fatal("layer places is missing")
		}
		if !slices.Contains(places.Keys, "note") {
			t.Errorf("layer keys = %v, want note among them even though one feature has no such tag", places.Keys)
		}
	})

	t.Run("the id field is the feature id, not a property", func(t *testing.T) {
		for _, l := range tile.Layers {
			if slices.Contains(l.Keys, "fid") {
				t.Errorf("layer %q lists fid among its keys %v; it is the feature id", l.Name, l.Keys)
			}
			for _, f := range l.Features {
				if !f.HasID {
					t.Errorf("layer %q has a feature with no id", l.Name)
				}
			}
		}
	})

	t.Run("each layer declares its geometry type", func(t *testing.T) {
		// A client reads the declared type to decide how to interpret the
		// geometry, so a layer that starts declaring the wrong one is a real
		// change even when every coordinate is unchanged.
		type tcase struct {
			layer string
			want  string
		}

		fn := func(tc tcase) func(*testing.T) {
			return func(t *testing.T) {
				l, ok := tile.Layer(tc.layer)
				if !ok {
					t.Fatalf("layer %q is missing; the tile holds %v", tc.layer, tile.LayerNames())
				}
				for _, f := range l.Features {
					if f.GeomType != tc.want {
						t.Errorf("feature %d declares %v, want %v", f.ID, f.GeomType, tc.want)
					}
				}
			}
		}

		tests := map[string]tcase{
			"places are points":     {layer: "places", want: "POINT"},
			"roads are linestrings": {layer: "roads", want: "LINESTRING"},
		}

		for name, tc := range tests {
			t.Run(name, fn(tc))
		}
	})

	t.Run("the encoding is negotiated", func(t *testing.T) {
		uri := tileURI(contentCollection, tms.WorldCRS84Quad, contentZoom, contentRow, contentCol)

		type tcase struct {
			accept       string
			wantEncoding string
		}

		fn := func(tc tcase) func(*testing.T) {
			return func(t *testing.T) {
				resp := get(t, srv, uri, tc.accept)

				if resp.Status != http.StatusOK {
					t.Fatalf("status = %d, want 200", resp.Status)
				}
				if resp.Encoding != tc.wantEncoding {
					t.Errorf("Content-Encoding = %q, want %q", resp.Encoding, tc.wantEncoding)
				}
				if got := resp.Header.Get("Content-Type"); got != "application/vnd.mapbox-vector-tile" {
					t.Errorf("Content-Type = %q, want the vector tile media type", got)
				}

				// Whatever the encoding, the same tile has to come back --
				// read the way the response said it was encoded.
				var decoded mvttest.Tile
				if resp.Encoding == "gzip" {
					decoded = mvttest.Decode(t, resp.Body)
				} else {
					decoded = mvttest.DecodeRaw(t, resp.Body)
				}
				assertLayers(t, decoded, "places", "roads")

				t.Logf("  ok  Accept-Encoding %-9q -> Content-Encoding %q, %d bytes", tc.accept, resp.Encoding, len(resp.Body))
			}
		}

		tests := map[string]tcase{
			"advertising gzip":    {accept: "gzip", wantEncoding: "gzip"},
			"advertising nothing": {accept: "", wantEncoding: ""},
			"refusing gzip":       {accept: "gzip;q=0", wantEncoding: ""},
		}

		for name, tc := range tests {
			t.Run(name, fn(tc))
		}
	})

	mvttest.AssertGolden(t, "testdata/golden/content-WorldCRS84Quad-3-2-10.txt", tile.Render())
}

// TestTileContentBothSchemes runs the same fixture through the other tiling
// scheme, at the tile that pairs with the one above (MAPCO-11549).
//
// The pairing is WorldCRS84Quad zoom 3 against WebMercatorQuad zoom 4: that
// scheme has half as many columns at a given zoom, so at one zoom deeper it
// divides longitude identically. Both tiles are column 10, spanning longitude
// 45..67.5.
//
// The consequence is worth stating because it looks wrong: x is the same in
// both schemes, and only y differs. Both grids are dyadic on the same longitude
// origin, so the same ground lands at the same x. That is what the pairing buys
// -- it isolates the latitude axis, which is the one the two schemes actually
// disagree about, so a broken transform has nowhere to hide.
func TestTileContentBothSchemes(t *testing.T) {
	ttools.ShouldSkip(t, dataTestEnv)

	srv := newServer(t, newContentAtlas(t))

	// WebMercatorQuad zoom 4 row 6 spans latitude 21.9430455334..40.9798980696.
	// !BBOX! reaches a 4326 layer already transformed, and ST_AsMVTGeom maps it
	// affinely -- linearly in latitude, not in mercator y -- so
	// y = (top - lat) / (top - bottom) * 4096 and the results are not whole
	// numbers. They are still derived rather than recorded.
	mercator := fetch(t, srv, contentCollection, tms.WebMercatorQuad, 4, 6, 10)

	t.Run("the same layers and features, in the other scheme", func(t *testing.T) {
		assertLayers(t, mercator, "places", "roads")
		// midband is missing here and present in the geographic tile: that is
		// the exclusion this pairing exists to produce, asserted below.
		assertFeatureIDs(t, mercator, "places", 1, 2, 3)
		assertFeatureIDs(t, mercator, "roads", 1, 2)
	})

	t.Run("every feature sits where this scheme's arithmetic says", func(t *testing.T) {
		// Derived, not recorded. x = (lon - 45) / 22.5 * 4096 as before, since
		// the pairing divides longitude identically. y is linear in latitude
		// over this tile's own span, 21.9430455334..40.9798980696, because
		// !BBOX! reaches a 4326 layer already transformed and ST_AsMVTGeom maps
		// it affinely -- so y = (top - lat) / (top - bottom) * 4096, which is
		// 1555.60, 808.62 and 345.31 before ST_AsMVTGeom rounds to the grid.
		//
		// Stated literally rather than left to the golden. A golden regenerated
		// in error would otherwise take this scheme's coordinates with it,
		// which is the whole reason the assertions live in two tiers.
		at(t, mercator, "places", "centre", point(2048, 1556))
		at(t, mercator, "places", "probe", point(2248, 809))
		at(t, mercator, "places", "nulltag", point(3072, 345))
	})

	t.Run("x is shared and y is not", func(t *testing.T) {
		crs84 := fetch(t, srv, contentCollection, tms.WorldCRS84Quad, contentZoom, contentRow, contentCol)

		for _, name := range []string{"centre", "probe", "nulltag"} {
			a := named(t, crs84, "places", name).Points()[0]
			b := named(t, mercator, "places", name).Points()[0]

			if a.X != b.X {
				t.Errorf("feature %q x = %d in WorldCRS84Quad and %d in WebMercatorQuad; the aligned pairing divides longitude identically", name, a.X, b.X)
			}
			if a.Y == b.Y {
				t.Errorf("feature %q y = %d in both schemes; the pairing exists because latitude differs", name, a.Y)
			}

			t.Logf("  ok  %-13s x=%d in both, y=%d then %d", name, a.X, a.Y, b.Y)
		}
	})

	t.Run("tags do not change with the scheme", func(t *testing.T) {
		// The same row, read through a different grid, is the same row.
		assertTags(t, mercator, "places", "centre", centreTags)
	})

	t.Run("each scheme excludes ground the other serves", func(t *testing.T) {
		// The mercator tile stops at latitude 40.9798980696; the geographic one
		// reaches 45, because a mercator row covers less ground the further it
		// is from the equator. midband sits at 42.1875, between the two.
		//
		// Both directions, because an absence that is never paired with a
		// presence can pass by the row simply being missing from the database.
		crs84 := fetch(t, srv, contentCollection, tms.WorldCRS84Quad, contentZoom, contentRow, contentCol)

		absent(t, mercator, "places", "midband")
		at(t, crs84, "places", "midband", point(2048, 512))

		// And the other way: the mercator tile reaches south past the
		// geographic tile's lower edge of 22.5, down to 21.9430455334. Nothing
		// in the fixture sits in that band, so the pairing's asymmetry is shown
		// by the band above rather than duplicated below -- stated so the gap
		// is deliberate rather than overlooked.
	})

	mvttest.AssertGolden(t, "testdata/golden/content-WebMercatorQuad-4-6-10.txt", mercator.Render())
}

// TestTilePathOrder pins the reading of the path's row and column
// (MAPCO-11550).
//
// The content checks guard the common case by choosing a tile whose row and
// column differ. This is the case that guard cannot reach, and it is sharper:
// the request below is rejected read correctly and names the content tile read
// transposed, so the two readings differ as a rejection against a full,
// correct-looking success rather than as two sets of coordinates. On a square
// matrix that is impossible, because an out-of-range row is also an
// out-of-range column; WorldCRS84Quad being twice as wide as it is tall is what
// makes the pair separable.
func TestTilePathOrder(t *testing.T) {
	ttools.ShouldSkip(t, dataTestEnv)

	srv := newServer(t, newContentAtlas(t))

	// Read correctly this is row 10, and zoom 3 has 8 rows.
	// Read transposed it is row 2 column 10 -- the content tile.
	uri := tileURI(contentCollection, tms.WorldCRS84Quad, contentZoom, contentCol, contentRow)
	resp := get(t, srv, uri, "gzip")

	if resp.Status == http.StatusOK {
		t.Errorf("GET %v returned 200; read correctly its row is out of range, so a success means the segments were read transposed", uri)
	}
	if resp.Status != http.StatusBadRequest {
		t.Errorf("GET %v status = %d, want 400", uri, resp.Status)
	}

	t.Logf("  ok  %v rejected with %d; transposed it would have named the content tile", uri, resp.Status)
}

// TestEmptyAnswers pins what the surface returns where there is nothing to
// serve (MAPCO-11550).
//
// An empty but valid tile rather than a not-found, deliberately: a not-found
// would tell a client the tileset itself is wrong, when the tileset is fine and
// the ground is simply empty. There are two ways to arrive at it and they run
// different code, so both are asked for.
func TestEmptyAnswers(t *testing.T) {
	ttools.ShouldSkip(t, dataTestEnv)

	srv := newServer(t, newContentAtlas(t))

	assertEmpty := func(t *testing.T, label string, tileMatrix, tileRow, tileCol int) {
		t.Helper()

		uri := tileURI(contentCollection, tms.WorldCRS84Quad, tileMatrix, tileRow, tileCol)
		resp := get(t, srv, uri, "gzip")

		if resp.Status != http.StatusOK {
			t.Errorf("%s: GET %v status = %d, want 200 -- a tileset that exists over empty ground is not a not-found", label, uri, resp.Status)
			return
		}
		if len(resp.Body) == 0 {
			t.Errorf("%s: GET %v returned an empty body, want a well-formed empty tile", label, uri)
			return
		}

		// A decoder has to accept it and read it as carrying no layers.
		if got := mvttest.Decode(t, resp.Body); len(got.Layers) != 0 {
			t.Errorf("%s: GET %v holds %v, want no layers", label, uri, got.LayerNames())
			return
		}

		t.Logf("  ok  %-38s %v -> 200, %d bytes, no layers", label, uri, len(resp.Body))
	}

	t.Run("an ordinary zoom over empty ground", func(t *testing.T) {
		// The opposite side of the world from the fixture, at the content
		// tile's own zoom. The query runs and returns nothing.
		assertEmpty(t, "ordinary zoom, empty ground", contentZoom, contentRow, 1)
	})

	t.Run("a high zoom the layers still cover", func(t *testing.T) {
		// The layers declare zooms up to 22, so this reaches the database too.
		// Ground far from the fixture at a fine zoom is the case a check at one
		// zoom would miss.
		assertEmpty(t, "high zoom, empty ground", 18, 100, 100)
	})

	t.Run("a zoom beyond what the layers declare", func(t *testing.T) {
		// Past the layers' max_zoom the map has no layers at that zoom, so the
		// surface answers before the provider is asked and no query runs.
		assertEmpty(t, "zoom beyond the layers' range", 23, 0, 0)
	})
}

// TestTileContentAcrossZooms follows one feature up and down the zoom pyramid
// (MAPCO-11551).
//
// Every other check here asks for one tile at one zoom, which leaves the zoom
// term itself untested: an error that scales with zoom, or one that only shows
// where a tile is very large or very small, is invisible to them.
//
// The probe is the fixture's "probe" point, at the centre of a WorldCRS84Quad
// zoom-11 tile. That placement is what makes every assertion exact: such a
// point lands on whole tile-space coordinates at every shallower zoom and on a
// tile boundary at none of them, so each zoom has exactly one tile that should
// contain it. A probe derived from a shallower zoom's midpoint reads as tidier
// and is worse -- it sits exactly on a tile corner at the deeper zooms, where
// the provider's inclusive bounding-box intersection hands it to four adjacent
// tiles at once, which is correct behaviour and makes it useless as a
// single-tile probe. TestTileContentSchemeEdges pins that behaviour.
//
// The pyramid runs in WorldCRS84Quad because that grid is linear in longitude
// and latitude, so a 4326 layer is exact in it at every zoom without the
// placement constraint WebMercatorQuad imposes.
//
// What varies with zoom here is the database's, not shigola's: shigola's own
// geometry simplification belongs to the encode branch the standard provider
// used, and that provider was removed under MAPCO-11490. A map backed by the
// MVT provider goes straight to the provider's own encoding, so the only
// zoom-dependent shaping is ST_AsMVTGeom snapping geometry onto the tile's
// integer grid.
func TestTileContentAcrossZooms(t *testing.T) {
	ttools.ShouldSkip(t, dataTestEnv)

	srv := newServer(t, newContentAtlas(t))

	// Derived from the probe's own tile: at zoom z the tile is
	// (col >> (11-z), row >> (11-z)) and the position within it is
	// ((col mod 2^k) + 0.5) * 4096 / 2^k, k = 11 - z. Whole numbers while
	// k <= 11, which is why the pyramid stops at zoom 0 rather than going
	// deeper than the probe's own zoom.
	type tcase struct {
		zoom, row, col int
		x, y           int32
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			at(t, fetch(t, srv, contentCollection, tms.WorldCRS84Quad, tc.zoom, tc.row, tc.col),
				"places", "probe", point(tc.x, tc.y))

			// Exactly one tile per zoom holds it, and never on a boundary, so
			// every neighbour must not -- in both axes. Checking only the
			// columns would leave uniqueness in y untested, and y is the axis
			// the schemes disagree about.
			for _, n := range []struct{ row, col int }{
				{tc.row, tc.col - 1}, {tc.row, tc.col + 1},
				{tc.row - 1, tc.col}, {tc.row + 1, tc.col},
			} {
				if n.col < 0 || n.col >= 1<<(tc.zoom+1) || n.row < 0 || n.row >= 1<<tc.zoom {
					continue
				}
				absent(t, fetch(t, srv, contentCollection, tms.WorldCRS84Quad, tc.zoom, n.row, n.col),
					"places", "probe")
			}
		}
	}

	tests := map[string]tcase{
		"zoom 0":  {zoom: 0, row: 0, col: 1, x: 1305, y: 1201},
		"zoom 1":  {zoom: 1, row: 0, col: 2, x: 2610, y: 2402},
		"zoom 2":  {zoom: 2, row: 1, col: 5, x: 1124, y: 708},
		"zoom 3":  {zoom: 3, row: 2, col: 10, x: 2248, y: 1416},
		"zoom 4":  {zoom: 4, row: 4, col: 21, x: 400, y: 2832},
		"zoom 5":  {zoom: 5, row: 9, col: 42, x: 800, y: 1568},
		"zoom 6":  {zoom: 6, row: 18, col: 84, x: 1600, y: 3136},
		"zoom 7":  {zoom: 7, row: 37, col: 168, x: 3200, y: 2176},
		"zoom 8":  {zoom: 8, row: 75, col: 337, x: 2304, y: 256},
		"zoom 9":  {zoom: 9, row: 150, col: 675, x: 512, y: 512},
		"zoom 10": {zoom: 10, row: 300, col: 1350, x: 1024, y: 1024},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}

	// One golden from the shallow end, where a tile covers so much ground that
	// features start collapsing into each other. Where exactly that happens is
	// the database's rounding as ST_AsMVTGeom snaps geometry onto the tile's
	// integer grid -- not something shigola decides -- so it is recorded here
	// rather than asserted literally. Pinning it in the literal tier would turn
	// a PostGIS upgrade into a red build, which is the opposite of the point.
	mvttest.AssertGolden(t, "testdata/golden/content-WorldCRS84Quad-2-1-5.txt",
		fetch(t, srv, contentCollection, tms.WorldCRS84Quad, 2, 1, 5).Render())
}
