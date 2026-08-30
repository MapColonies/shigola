package cache

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom/slippy"
)

type sTiles []slippy.Tile

func (st sTiles) Len() int           { return len(st) }
func (st sTiles) Swap(i, j int)      { st[i], st[j] = st[j], st[i] }
func (st sTiles) Less(i, j int) bool { return st[i].Less(st[j]) }

// IsEqual report true only if both the size and the elements are the same. Where a tile is equal only if the z,x,y elements match.
func (st sTiles) IsEqual(ost sTiles) bool {
	if len(st) != len(ost) {
		return false
	}
	for i := range st {
		zi, xi, yi := st[i].ZXY()
		zj, xj, yj := ost[i].ZXY()
		if zi != zj || xi != xj || yi != yj {
			return false
		}
	}
	return true
}

func (st sTiles) GoString() string {
	var b = bytes.NewBufferString("[")
	addComma := false
	for _, v := range st {
		if addComma {
			b.WriteString(",")
		} else {
			addComma = true
		}
		fmt.Fprintf(b, "%#v", v)
	}
	b.WriteString("]")
	return b.String()
}
func (st sTiles) String() string {
	var b = bytes.NewBufferString("[")
	b.WriteString("[")
	addComma := false
	for _, v := range st {
		if addComma {
			b.WriteString(",")
		} else {
			addComma = true
		}
		z, x, y := v.ZXY()
		fmt.Fprintf(b, "%v/%v/%v", z, x, y)
	}
	b.WriteString("]")
	return b.String()
}

func TestGenerateTilesForBounds(t *testing.T) {

	worldBounds := [4]float64{-180.0, -85.0511, 180, 85.0511}

	type tcase struct {
		zooms  []uint
		bounds [4]float64
		tiles  sTiles
		grid   *tms.TileMatrixSet
		err    error
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {

			// Setup up the generator.
			tilechannel := generateTilesForBounds(context.Background(), tc.bounds, tc.zooms, tc.grid)
			tiles := make(sTiles, 0, len(tc.tiles))
			for tile := range tilechannel.Channel() {
				tiles = append(tiles, tile)
			}
			if tc.err != nil {
				err := tilechannel.Err()
				if err == nil || err.Error() != tc.err.Error() {
					t.Errorf("error, expected %v got %v", err, tc.err)
				}
				// We expected an error so, don't trust the tiles.
				return
			}

			if err := tilechannel.Err(); err != nil {
				t.Errorf("error, expected nil got %v", err)
				return
			}

			sort.Sort(tiles)
			if !tc.tiles.IsEqual(tiles) {
				t.Errorf("unexpected tile list generated, expected %v got %v", tc.tiles, tiles)
			}
		}
	}

	tests := map[string]tcase{
		"max_zoom=0": {
			zooms:  []uint{0},
			bounds: worldBounds,
			grid:   mustTestGrid(t, tms.WebMercatorQuad),
			tiles:  sTiles{slippy.Tile{}},
		},
		"min_zoom=1 max_zoom=1": {
			zooms:  []uint{1},
			bounds: worldBounds,
			grid:   mustTestGrid(t, tms.WebMercatorQuad),
			tiles: sTiles{
				slippy.Tile{Z: 1},
				slippy.Tile{Z: 1, Y: 1},
				slippy.Tile{Z: 1, X: 1},
				slippy.Tile{Z: 1, X: 1, Y: 1},
			},
		},
		// Corners given in reverse order, and landing exactly on the equator and
		// the prime meridian: the region is lon 0..180, lat 0..90.
		//
		// Only 1/1/0 lies inside it. The enumeration used to add 1/1/1 as well —
		// the tile south of the equator, which the region touches but does not
		// cover — while not adding 0/... for the identical touch at lon 0. Tiles
		// now come from the TileMatrixSet, which treats both edges alike, so a
		// boundary-touching tile is no longer seeded.
		"min_zoom=1 max_zoom=1 bounds=180,90,0,0": {
			zooms:  []uint{1},
			bounds: [4]float64{180.0, 90.0, 0.0, 0.0},
			grid:   mustTestGrid(t, tms.WebMercatorQuad),
			tiles: sTiles{
				slippy.Tile{Z: 1, X: 1},
			},
		},
		"min_zoom=1 max_zoom=1 bounds=5.9,45.8,10.5,47.8 WSG84": {
			// see: https://github.com/go-spatial/tegola/issues/880#issuecomment-2556563251
			zooms:  []uint{10},
			bounds: [4]float64{5.9, 45.8, 10.5, 47.8},
			grid:   mustTestGrid(t, tms.WebMercatorQuad),
			tiles: sTiles{
				slippy.Tile{Z: 10, X: 528, Y: 356}, slippy.Tile{Z: 10, X: 528, Y: 357}, slippy.Tile{Z: 10, X: 528, Y: 358}, slippy.Tile{Z: 10, X: 528, Y: 359}, slippy.Tile{Z: 10, X: 528, Y: 360}, slippy.Tile{Z: 10, X: 528, Y: 361}, slippy.Tile{Z: 10, X: 528, Y: 362}, slippy.Tile{Z: 10, X: 528, Y: 363}, slippy.Tile{Z: 10, X: 528, Y: 364}, slippy.Tile{Z: 10, X: 528, Y: 365},
				slippy.Tile{Z: 10, X: 529, Y: 356}, slippy.Tile{Z: 10, X: 529, Y: 357}, slippy.Tile{Z: 10, X: 529, Y: 358}, slippy.Tile{Z: 10, X: 529, Y: 359}, slippy.Tile{Z: 10, X: 529, Y: 360}, slippy.Tile{Z: 10, X: 529, Y: 361}, slippy.Tile{Z: 10, X: 529, Y: 362}, slippy.Tile{Z: 10, X: 529, Y: 363}, slippy.Tile{Z: 10, X: 529, Y: 364}, slippy.Tile{Z: 10, X: 529, Y: 365},
				slippy.Tile{Z: 10, X: 530, Y: 356}, slippy.Tile{Z: 10, X: 530, Y: 357}, slippy.Tile{Z: 10, X: 530, Y: 358}, slippy.Tile{Z: 10, X: 530, Y: 359}, slippy.Tile{Z: 10, X: 530, Y: 360}, slippy.Tile{Z: 10, X: 530, Y: 361}, slippy.Tile{Z: 10, X: 530, Y: 362}, slippy.Tile{Z: 10, X: 530, Y: 363}, slippy.Tile{Z: 10, X: 530, Y: 364}, slippy.Tile{Z: 10, X: 530, Y: 365},
				slippy.Tile{Z: 10, X: 531, Y: 356}, slippy.Tile{Z: 10, X: 531, Y: 357}, slippy.Tile{Z: 10, X: 531, Y: 358}, slippy.Tile{Z: 10, X: 531, Y: 359}, slippy.Tile{Z: 10, X: 531, Y: 360}, slippy.Tile{Z: 10, X: 531, Y: 361}, slippy.Tile{Z: 10, X: 531, Y: 362}, slippy.Tile{Z: 10, X: 531, Y: 363}, slippy.Tile{Z: 10, X: 531, Y: 364}, slippy.Tile{Z: 10, X: 531, Y: 365},
				slippy.Tile{Z: 10, X: 532, Y: 356}, slippy.Tile{Z: 10, X: 532, Y: 357}, slippy.Tile{Z: 10, X: 532, Y: 358}, slippy.Tile{Z: 10, X: 532, Y: 359}, slippy.Tile{Z: 10, X: 532, Y: 360}, slippy.Tile{Z: 10, X: 532, Y: 361}, slippy.Tile{Z: 10, X: 532, Y: 362}, slippy.Tile{Z: 10, X: 532, Y: 363}, slippy.Tile{Z: 10, X: 532, Y: 364}, slippy.Tile{Z: 10, X: 532, Y: 365},
				slippy.Tile{Z: 10, X: 533, Y: 356}, slippy.Tile{Z: 10, X: 533, Y: 357}, slippy.Tile{Z: 10, X: 533, Y: 358}, slippy.Tile{Z: 10, X: 533, Y: 359}, slippy.Tile{Z: 10, X: 533, Y: 360}, slippy.Tile{Z: 10, X: 533, Y: 361}, slippy.Tile{Z: 10, X: 533, Y: 362}, slippy.Tile{Z: 10, X: 533, Y: 363}, slippy.Tile{Z: 10, X: 533, Y: 364}, slippy.Tile{Z: 10, X: 533, Y: 365},
				slippy.Tile{Z: 10, X: 534, Y: 356}, slippy.Tile{Z: 10, X: 534, Y: 357}, slippy.Tile{Z: 10, X: 534, Y: 358}, slippy.Tile{Z: 10, X: 534, Y: 359}, slippy.Tile{Z: 10, X: 534, Y: 360}, slippy.Tile{Z: 10, X: 534, Y: 361}, slippy.Tile{Z: 10, X: 534, Y: 362}, slippy.Tile{Z: 10, X: 534, Y: 363}, slippy.Tile{Z: 10, X: 534, Y: 364}, slippy.Tile{Z: 10, X: 534, Y: 365},
				slippy.Tile{Z: 10, X: 535, Y: 356}, slippy.Tile{Z: 10, X: 535, Y: 357}, slippy.Tile{Z: 10, X: 535, Y: 358}, slippy.Tile{Z: 10, X: 535, Y: 359}, slippy.Tile{Z: 10, X: 535, Y: 360}, slippy.Tile{Z: 10, X: 535, Y: 361}, slippy.Tile{Z: 10, X: 535, Y: 362}, slippy.Tile{Z: 10, X: 535, Y: 363}, slippy.Tile{Z: 10, X: 535, Y: 364}, slippy.Tile{Z: 10, X: 535, Y: 365},
				slippy.Tile{Z: 10, X: 536, Y: 356}, slippy.Tile{Z: 10, X: 536, Y: 357}, slippy.Tile{Z: 10, X: 536, Y: 358}, slippy.Tile{Z: 10, X: 536, Y: 359}, slippy.Tile{Z: 10, X: 536, Y: 360}, slippy.Tile{Z: 10, X: 536, Y: 361}, slippy.Tile{Z: 10, X: 536, Y: 362}, slippy.Tile{Z: 10, X: 536, Y: 363}, slippy.Tile{Z: 10, X: 536, Y: 364}, slippy.Tile{Z: 10, X: 536, Y: 365},
				slippy.Tile{Z: 10, X: 537, Y: 356}, slippy.Tile{Z: 10, X: 537, Y: 357}, slippy.Tile{Z: 10, X: 537, Y: 358}, slippy.Tile{Z: 10, X: 537, Y: 359}, slippy.Tile{Z: 10, X: 537, Y: 360}, slippy.Tile{Z: 10, X: 537, Y: 361}, slippy.Tile{Z: 10, X: 537, Y: 362}, slippy.Tile{Z: 10, X: 537, Y: 363}, slippy.Tile{Z: 10, X: 537, Y: 364}, slippy.Tile{Z: 10, X: 537, Y: 365},
				slippy.Tile{Z: 10, X: 538, Y: 356}, slippy.Tile{Z: 10, X: 538, Y: 357}, slippy.Tile{Z: 10, X: 538, Y: 358}, slippy.Tile{Z: 10, X: 538, Y: 359}, slippy.Tile{Z: 10, X: 538, Y: 360}, slippy.Tile{Z: 10, X: 538, Y: 361}, slippy.Tile{Z: 10, X: 538, Y: 362}, slippy.Tile{Z: 10, X: 538, Y: 363}, slippy.Tile{Z: 10, X: 538, Y: 364}, slippy.Tile{Z: 10, X: 538, Y: 365},
				slippy.Tile{Z: 10, X: 539, Y: 356}, slippy.Tile{Z: 10, X: 539, Y: 357}, slippy.Tile{Z: 10, X: 539, Y: 358}, slippy.Tile{Z: 10, X: 539, Y: 359}, slippy.Tile{Z: 10, X: 539, Y: 360}, slippy.Tile{Z: 10, X: 539, Y: 361}, slippy.Tile{Z: 10, X: 539, Y: 362}, slippy.Tile{Z: 10, X: 539, Y: 363}, slippy.Tile{Z: 10, X: 539, Y: 364}, slippy.Tile{Z: 10, X: 539, Y: 365},
				slippy.Tile{Z: 10, X: 540, Y: 356}, slippy.Tile{Z: 10, X: 540, Y: 357}, slippy.Tile{Z: 10, X: 540, Y: 358}, slippy.Tile{Z: 10, X: 540, Y: 359}, slippy.Tile{Z: 10, X: 540, Y: 360}, slippy.Tile{Z: 10, X: 540, Y: 361}, slippy.Tile{Z: 10, X: 540, Y: 362}, slippy.Tile{Z: 10, X: 540, Y: 363}, slippy.Tile{Z: 10, X: 540, Y: 364}, slippy.Tile{Z: 10, X: 540, Y: 365},
				slippy.Tile{Z: 10, X: 541, Y: 356}, slippy.Tile{Z: 10, X: 541, Y: 357}, slippy.Tile{Z: 10, X: 541, Y: 358}, slippy.Tile{Z: 10, X: 541, Y: 359}, slippy.Tile{Z: 10, X: 541, Y: 360}, slippy.Tile{Z: 10, X: 541, Y: 361}, slippy.Tile{Z: 10, X: 541, Y: 362}, slippy.Tile{Z: 10, X: 541, Y: 363}, slippy.Tile{Z: 10, X: 541, Y: 364}, slippy.Tile{Z: 10, X: 541, Y: 365},
			},
		},
		// The generator used to fall back to WebMercatorQuad here. It cannot:
		// the worker writes under the grid the run resolved, so enumerating a
		// different one produces tiles filed where nothing looks for them, and
		// the run reports success.
		"no grid resolved": {
			zooms:  []uint{0},
			bounds: worldBounds,
			err:    ErrNoGridResolved,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}

}

// TestResolveSeedPurgeGrid covers the rule that one run means one tiling
// scheme, and that a targeted map which does not support it fails the run
// rather than being skipped.
func TestResolveSeedPurgeGrid(t *testing.T) {
	newMap := func(name string, gridIDs ...string) atlas.Map {
		return newTestMap(t, name, gridIDs...)
	}

	tests := map[string]struct {
		flag     string
		mapFlag  string
		maps     []atlas.Map
		expected string
		wantErr  bool
	}{
		"every map, no flag, defaults to WebMercatorQuad": {
			maps:     []atlas.Map{newMap("a"), newMap("b")},
			expected: tms.WebMercatorQuad,
		},
		"one map takes its own default": {
			mapFlag:  "crs84",
			maps:     []atlas.Map{newMap("crs84", tms.WorldCRS84Quad)},
			expected: tms.WorldCRS84Quad,
		},
		"the flag wins": {
			flag:     tms.WorldCRS84Quad,
			mapFlag:  "both",
			maps:     []atlas.Map{newMap("both", tms.WebMercatorQuad, tms.WorldCRS84Quad)},
			expected: tms.WorldCRS84Quad,
		},
		// The reason this is an error: one run enumerates one grid, so seeding
		// the CRS84-only map on a WebMercator pyramid would write tiles nothing
		// ever asks for, and report success.
		"a map that does not support the run's grid": {
			maps:    []atlas.Map{newMap("wm"), newMap("crs84", tms.WorldCRS84Quad)},
			wantErr: true,
		},
		"an unknown scheme": {
			flag:    "NoSuchQuad",
			maps:    []atlas.Map{newMap("a")},
			wantErr: true,
		},
		"a scheme this build cannot serve": {
			flag:    "NZTM2000Quad",
			maps:    []atlas.Map{newMap("a")},
			wantErr: true,
		},
		// A map listing no scheme supports none, so it fails the run's support
		// check rather than being seeded on WebMercatorQuad.
		//
		// This is a change: the list used to read as "the default one" when
		// empty, so such a map both chose WebMercatorQuad and passed the check.
		// Registration cannot produce it — register fills the list with every
		// available grid when the config omits the key — and failing beats
		// seeding a pyramid the map never declared, which is the same reason
		// the check rejects a mismatched map. It is pinned here so that if a
		// map ever can reach the CLI with no schemes, this says what happens.
		"a map listing no scheme at all": {
			maps:    []atlas.Map{{Name: "gridless"}},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cacheTileMatrixSet, cacheMap, seedPurgeMaps = tc.flag, tc.mapFlag, tc.maps
			defer func() { cacheTileMatrixSet, cacheMap, seedPurgeMaps = "", "", nil }()

			grid, err := resolveSeedPurgeGrid()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveSeedPurgeGrid() = %v, want an error", grid.ID())
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveSeedPurgeGrid() error = %v", err)
			}

			if grid.ID() != tc.expected {
				t.Errorf("resolveSeedPurgeGrid() = %v, want %v", grid.ID(), tc.expected)
			}
		})
	}
}

// newTestMap builds a map listing the named schemes, in the order given -- the
// order is what resolveSeedPurgeGrid reads, so it is never sorted here.
//
// Naming none leaves the constructor's WebMercatorQuad in place rather than
// emptying the list. Registration never produces a map listing nothing, so an
// empty list would make this helper build something no config can express.
func newTestMap(t *testing.T, name string, gridIDs ...string) atlas.Map {
	t.Helper()

	m := atlas.NewWebMercatorMap(name)
	if len(gridIDs) == 0 {
		return m
	}

	grids := make([]*tms.TileMatrixSet, 0, len(gridIDs))
	for _, id := range gridIDs {
		grid, err := tms.Get(id)
		if err != nil {
			t.Fatalf("tms.Get(%q): %v", id, err)
		}
		grids = append(grids, grid)
	}
	m.TileMatrixSets = grids

	return m
}

// TestResolveSeedPurgeGridWarnsWhenDefaulted covers the warning a run gets when
// it never named a scheme.
//
// A run that names none still writes a whole pyramid under one, and the wrong
// one is not visibly wrong: the run reports success either way. The warning is
// the only thing between an operator and a seeded cache nothing reads, so it has
// to name the scheme actually used and say where it came from -- "a default was
// applied" would leave them to work out which.
func TestResolveSeedPurgeGridWarnsWhenDefaulted(t *testing.T) {
	type tcase struct {
		flag    string
		mapFlag string
		maps    []atlas.Map
		want    []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			cacheTileMatrixSet, cacheMap, seedPurgeMaps = tc.flag, tc.mapFlag, tc.maps
			defer func() { cacheTileMatrixSet, cacheMap, seedPurgeMaps = "", "", nil }()

			var buf bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			defer slog.SetDefault(previous)

			if _, err := resolveSeedPurgeGrid(); err != nil {
				t.Fatalf("resolveSeedPurgeGrid() error = %v", err)
			}

			logged := buf.String()

			if len(tc.want) == 0 {
				if strings.Contains(logged, "--tile-matrix-set") {
					t.Errorf("warned about a scheme the run named itself: %v", logged)
				}

				return
			}

			for _, want := range tc.want {
				if !strings.Contains(logged, want) {
					t.Errorf("warning = %q, want it to mention %q", logged, want)
				}
			}
		}
	}

	tests := map[string]tcase{
		"the flag names one, so nothing to warn about": {
			flag:    tms.WorldCRS84Quad,
			mapFlag: "crs84",
			maps:    []atlas.Map{newTestMap(t, "crs84", tms.WorldCRS84Quad)},
		},
		"one map, no flag: names the scheme and the map it came from": {
			mapFlag: "crs84",
			maps:    []atlas.Map{newTestMap(t, "crs84", tms.WorldCRS84Quad, tms.WebMercatorQuad)},
			// The handler escapes the quotes around the map name, so match the
			// name itself rather than the form the message wrote it in.
			want: []string{tms.WorldCRS84Quad, "first scheme map", "crs84", "--tile-matrix-set"},
		},
		"every map, no flag: names WebMercatorQuad and says it is the default": {
			maps: []atlas.Map{newTestMap(t, "a"), newTestMap(t, "b")},
			want: []string{tms.WebMercatorQuad, "not scoped to one map", "--tile-matrix-set"},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestValidateBoundsSRID covers what --bounds-srid may say now that it no longer
// selects the tiling scheme.
//
// --bounds describes the bounds and nothing else, and the bounds are validated
// as lng/lat, so a projected srid never described them. Saying so beats
// accepting the flag and ignoring it: a run whose bounds meant metres would
// silently seed the wrong area.
func TestValidateBoundsSRID(t *testing.T) {
	tests := map[string]struct {
		srid    int
		wantErr bool
	}{
		"the geographic default":       {srid: 4326},
		"a projected srid is rejected": {srid: 3857, wantErr: true},
		"world mercator is rejected":   {srid: 3395, wantErr: true},
		"an unknown srid is rejected":  {srid: 1234, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateBoundsSRID(tc.srid)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateBoundsSRID(%d) = nil, want an error", tc.srid)
				}
				return
			}

			if err != nil {
				t.Fatalf("validateBoundsSRID(%d) = %v, want nil", tc.srid, err)
			}
		})
	}
}

// TestValidateTileInGrid covers the check that a named tile exists in the
// scheme the run operates in.
//
// The two active schemes differ in width — WorldCRS84Quad has 2*2^z columns
// where WebMercatorQuad has 2^z — so a tile name valid in one can name nothing
// in the other. Seeding it would generate and store a tile no request can ask
// for, and the run would report success.
func TestValidateTileInGrid(t *testing.T) {
	tests := map[string]struct {
		gridID  string
		tile    slippy.Tile
		wantErr bool
	}{
		"z0 x0 in WebMercatorQuad":            {gridID: tms.WebMercatorQuad, tile: slippy.Tile{Z: 0, X: 0, Y: 0}},
		"z0 x1 is off WebMercatorQuad":        {gridID: tms.WebMercatorQuad, tile: slippy.Tile{Z: 0, X: 1, Y: 0}, wantErr: true},
		"z0 x1 is on WorldCRS84Quad":          {gridID: tms.WorldCRS84Quad, tile: slippy.Tile{Z: 0, X: 1, Y: 0}},
		"z0 y1 is off WorldCRS84Quad":         {gridID: tms.WorldCRS84Quad, tile: slippy.Tile{Z: 0, X: 0, Y: 1}, wantErr: true},
		"a zoom beyond the scheme's matrices": {gridID: tms.WebMercatorQuad, tile: slippy.Tile{Z: 99, X: 0, Y: 0}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			grid, err := tms.Get(tc.gridID)
			if err != nil {
				t.Fatalf("tms.Get(%q): %v", tc.gridID, err)
			}

			err = validateTileInGrid(tc.tile, grid)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateTileInGrid(%v, %v) = nil, want an error", tc.tile, tc.gridID)
				}
				return
			}

			if err != nil {
				t.Fatalf("validateTileInGrid(%v, %v) = %v, want nil", tc.tile, tc.gridID, err)
			}
		})
	}
}

// TestTileFamilyStaysInsideGrid checks the assumption the tile-name and
// tile-list zoom expansion rests on: that walking a tile's family with
// slippy.RangeFamilyAt yields tiles that exist in the run's scheme.
//
// RangeFamilyAt halves and doubles x and y, which is quadtree arithmetic. That
// looks like it assumes a square 2^z pyramid, and WorldCRS84Quad is 2*2^z by
// 2^z — but its columns and rows both double per zoom, so it is a quadtree too
// and the arithmetic holds. This test is what says so, rather than the reading.
func TestTileFamilyStaysInsideGrid(t *testing.T) {
	for _, gridID := range []string{tms.WebMercatorQuad, tms.WorldCRS84Quad} {
		t.Run(gridID, func(t *testing.T) {
			grid, err := tms.Get(gridID)
			if err != nil {
				t.Fatalf("tms.Get(%q): %v", gridID, err)
			}

			// start from every tile of zoom 1, so a 2:1 scheme's extra columns
			// are covered
			cols, rows, err := grid.MatrixSize(1)
			if err != nil {
				t.Fatalf("MatrixSize: %v", err)
			}

			for x := int64(0); x < cols; x++ {
				for y := int64(0); y < rows; y++ {
					start := slippy.Tile{Z: 1, X: uint(x), Y: uint(y)}

					for _, zoom := range []uint{0, 1, 2, 5} {
						slippy.RangeFamilyAt(start, slippy.Zoom(zoom), func(tile slippy.Tile) bool {
							if err := validateTileInGrid(tile, grid); err != nil {
								t.Errorf("family of %v at zoom %d produced %v: %v", start, zoom, tile, err)
								return false
							}

							return true
						})
					}
				}
			}
		})
	}
}

// mustTestGrid resolves a tiling scheme, failing the test if this build cannot
// serve it.
func mustTestGrid(t *testing.T, id string) *tms.TileMatrixSet {
	t.Helper()

	grid, err := tms.Get(id)
	if err != nil {
		t.Fatalf("tms.Get(%q): %v", id, err)
	}

	return grid
}
