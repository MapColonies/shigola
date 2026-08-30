package config_test

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/provider/postgis"
	"github.com/MapColonies/shigola/tms"
)

// The OGC CITE conformance suite is the only check in this repository that can
// catch a conformance rule the code has no way to check about itself, and its
// data source used to be a GeoPackage. That made the project's conformance
// evidence an invisible dependency of the GeoPackage provider: removing the
// provider would have deleted the evidence with it, and nothing would have said
// so. These tests are what made the dependency visible, and they are what let
// the provider be deleted (MAPCO-11488) with the evidence left standing.
//
// They still earn their place with the provider gone: the suite's data source
// is a config file, and a config file can be pointed anywhere. They need
// neither TeamEngine nor a database.
const (
	citeConfigPath   = "../.github/cite/config.toml"
	citeWorkflowPath = "../.github/workflows/ogc_cite.yml"
)

// citeRunInvocation matches the workflow's calls to run.sh, whose arguments are
// <tileMatrixSetId> <tileMatrix> <tileRow> <tileCol> -- row before column, which
// is the OGC order and the reverse of the x,y the rest of this codebase uses.
var citeRunInvocation = regexp.MustCompile(`\.github/cite/run\.sh\s+(\S+)\s+(\d+)\s+(\d+)\s+(\d+)`)

// loadCiteConfig returns the conformance config, or fails the test.
//
// It goes through Validate rather than Parse so that the config-level rules
// apply too -- in particular the one forbidding a map from mixing an MVT
// provider with any other, which is what would break first if someone added a
// second data source back.
func loadCiteConfig(t *testing.T) config.Config {
	t.Helper()

	cfg, err := config.LoadAndValidate(citeConfigPath)
	if err != nil {
		t.Fatalf("LoadAndValidate(%v) = %v, want nil", citeConfigPath, err)
	}
	if len(cfg.Maps) != 1 {
		t.Fatalf("map count = %d, want 1: the suite is pointed at one collection", len(cfg.Maps))
	}

	return cfg
}

// TestCiteConformanceConfig pins what the conformance fixture is allowed to be.
//
// The provider-type assertion is the guard that matters, and it is a stricter
// one than "the config loads": LoadAndValidate rejects a provider type no
// binary serves, but it has nothing to say about a config quietly re-pointed at
// some other type that is served. Naming mvt_postgis exactly is what keeps the
// conformance evidence about the path shigola actually ships.
func TestCiteConformanceConfig(t *testing.T) {
	cfg := loadCiteConfig(t)

	if len(cfg.Providers) == 0 {
		t.Fatal("the conformance config declares no providers")
	}

	for i, prvd := range cfg.Providers {
		typ, err := prvd.String("type", nil)
		if err != nil {
			t.Errorf("provider %d: type = %v, want a type", i, err)
			continue
		}
		if typ != postgis.MVTProviderType {
			t.Errorf("provider %d: type = %q, want %q", i, typ, postgis.MVTProviderType)
		}
	}

	// Both distinct servable grid shapes, in the order the config names them.
	// WGS1984Quad is servable too but indexes the same ground as
	// WorldCRS84Quad with only its declared axis order differing -- see
	// TestWGS1984QuadInvertedAxes -- so running the suite against it as well
	// would add a third of the run time for none of the coverage.
	want := []string{tms.WebMercatorQuad, tms.WorldCRS84Quad}

	got := make([]string, 0, len(cfg.Maps[0].TileMatrixSets))
	for _, id := range cfg.Maps[0].TileMatrixSets {
		got = append(got, string(id))
	}

	if !slices.Equal(got, want) {
		t.Errorf("tile_matrix_sets = %v, want %v", got, want)
	}
}

// TestCiteConformanceWorkflowTiles checks the workflow's tile arguments against
// the config that serves them. The two files are edited independently and a
// mismatch between them is quiet: run.sh reports no failures for a tile the
// service says does not exist, and its MIN_PASSED floor only catches a run that
// never reached the service at all.
//
// What this can check without a database is that each declared scheme is run
// exactly once, on a tile that exists in that grid, inside the map's bounds, at a
// zoom every layer covers. What it cannot check is that the tile holds rows in
// all three tables -- ST_AsMVT emits no layer at all for a layer with none, so an
// almost-empty tile still passes the suite. TestMVTProviders in provider/postgis
// covers that half, against the database, on these same two tiles.
func TestCiteConformanceWorkflowTiles(t *testing.T) {
	cfg := loadCiteConfig(t)

	workflow, err := os.ReadFile(citeWorkflowPath)
	if err != nil {
		t.Fatalf("ReadFile(%v) = %v, want nil", citeWorkflowPath, err)
	}

	type tcase struct {
		tile tms.Tile
	}

	fn := func(scheme string, tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			grid, err := tms.Get(scheme)
			if err != nil {
				t.Fatalf("tms.Get(%v) = %v, want nil", scheme, err)
			}

			if err := grid.ValidateTile(tc.tile.Z, tc.tile.X, tc.tile.Y); err != nil {
				t.Fatalf("ValidateTile(%v) = %v, want nil", tc.tile, err)
			}

			// Every layer has to be in range at this zoom, or the suite fetches a
			// tile that is missing one and still reports a clean pass. The window
			// is narrow on purpose -- see the layer SQL in the config -- so a
			// failure here is a question about accuracy, not a typo.
			for _, l := range cfg.Maps[0].Layers {
				if l.MinZoom != nil && tc.tile.Z < int(*l.MinZoom) {
					t.Errorf("layer %q starts at zoom %d, above the suite's %d", l.Name, *l.MinZoom, tc.tile.Z)
				}
				if l.MaxZoom != nil && tc.tile.Z > int(*l.MaxZoom) {
					t.Errorf("layer %q ends at zoom %d, below the suite's %d", l.Name, *l.MaxZoom, tc.tile.Z)
				}
			}

			// The map's bounds are what the tileset's tileMatrixSetLimits are
			// derived from, so a tile outside them is a tile the suite is told
			// does not exist.
			b, err := grid.Bounds(tc.tile)
			if err != nil {
				t.Fatalf("Bounds(%v) = %v, want nil", tc.tile, err)
			}

			bounds := cfg.Maps[0].Bounds
			if len(bounds) != 4 {
				t.Fatalf("map bounds = %v, want 4 values", bounds)
			}
			minLon, minLat := float64(bounds[0]), float64(bounds[1])
			maxLon, maxLat := float64(bounds[2]), float64(bounds[3])

			if b.Right <= minLon || b.Left >= maxLon || b.Top <= minLat || b.Bottom >= maxLat {
				t.Errorf("tile %v spans %v, outside the map's bounds %v", tc.tile, b, bounds)
			}
		}
	}

	tests := map[string]tcase{}

	for _, run := range citeRunInvocation.FindAllStringSubmatch(string(workflow), -1) {
		scheme := run[1]
		if _, seen := tests[scheme]; seen {
			t.Errorf("the workflow runs %q more than once", scheme)
		}

		zoom, err := strconv.Atoi(run[2])
		if err != nil {
			t.Fatalf("tileMatrix %q in the workflow is not a number", run[2])
		}
		row, err := strconv.ParseInt(run[3], 10, 64)
		if err != nil {
			t.Fatalf("tileRow %q in the workflow is not a number", run[3])
		}
		col, err := strconv.ParseInt(run[4], 10, 64)
		if err != nil {
			t.Fatalf("tileCol %q in the workflow is not a number", run[4])
		}

		tests[scheme] = tcase{tile: tms.Tile{Z: zoom, X: col, Y: row}}
	}

	declared := make([]string, 0, len(cfg.Maps[0].TileMatrixSets))
	for _, id := range cfg.Maps[0].TileMatrixSets {
		declared = append(declared, string(id))
	}

	// Per scheme, not by count: a workflow that ran WebMercatorQuad twice and
	// WorldCRS84Quad never would otherwise satisfy a count.
	for _, scheme := range declared {
		if _, ok := tests[scheme]; !ok {
			t.Errorf("the map declares %q, but the workflow never runs the suite against it", scheme)
		}
	}

	for scheme, tc := range tests {
		if !slices.Contains(declared, scheme) {
			t.Errorf("the workflow runs %q, which the map does not declare", scheme)
			continue
		}
		t.Run(scheme, fn(scheme, tc))
	}
}
