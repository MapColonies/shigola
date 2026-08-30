package postgis

import (
	"bytes"
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/mvttest"
	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/provider"
	"github.com/go-spatial/geom"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TESTENV is the environment variable that must be set to "yes" to run postgis tests.
const TESTENV = "RUN_POSTGIS_TESTS"

var DefaultEnvConfig map[string]any

var DefaultConfig map[string]any = map[string]any{
	ConfigKeyURI:         "postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable",
	ConfigKeySSLMode:     "disable",
	ConfigKeySSLKey:      "",
	ConfigKeySSLCert:     "",
	ConfigKeySSLRootCert: "",
}

func getConfigFromEnv() map[string]any {
	return map[string]any{
		ConfigKeyURI: ttools.GetEnvDefault(
			"PGURI",
			"postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable",
		),
		ConfigKeySSLMode:     ttools.GetEnvDefault("PGSSLMODE", "disable"),
		ConfigKeySSLKey:      ttools.GetEnvDefault("PGSSLKEY", ""),
		ConfigKeySSLCert:     ttools.GetEnvDefault("PGSSLCERT", ""),
		ConfigKeySSLRootCert: ttools.GetEnvDefault("PGSSLROOTCERT", ""),
	}
}

func init() {
	DefaultEnvConfig = getConfigFromEnv()
}

type TCConfig struct {
	BaseConfig     map[string]any
	ConfigOverride map[string]any
	LayerConfig    []map[string]any
}

func (cfg TCConfig) Config(mConfig map[string]any) dict.Dict {
	var config map[string]any
	if cfg.BaseConfig != nil {
		mConfig = cfg.BaseConfig
	}
	config = make(map[string]any, len(mConfig))
	maps.Copy(config, mConfig)

	// set the config overrides
	maps.Copy(config, cfg.ConfigOverride)

	if len(cfg.LayerConfig) > 0 {
		layerConfig, _ := config[ConfigKeyLayers].([]map[string]any)
		layerConfig = append(layerConfig, cfg.LayerConfig...)
		config[ConfigKeyLayers] = layerConfig
	}

	return dict.Dict(config)
}

// athensCiteLayers returns the three Athens layer configs .github/cite/config.toml
// serves, as the mvt_postgis provider config equivalent. Both conformance-tile
// cases in TestMVTProviders use it: they differ only in the tile, and pinning the
// layers in one place is what makes that the visible difference.
func athensCiteLayers() []map[string]any {
	return []map[string]any{
		{
			ConfigKeyGeomIDField: "fid",
			ConfigKeyGeomType:    "multipolygon",
			ConfigKeyGeomField:   "geom",
			ConfigKeyLayerName:   "land",
			ConfigKeySQL:         "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid FROM land_polygons WHERE geom && !BBOX!",
			ConfigKeySRID:        4326,
		},
		{
			ConfigKeyGeomIDField: "fid",
			ConfigKeyGeomType:    "multilinestring",
			ConfigKeyGeomField:   "geom",
			ConfigKeyLayerName:   "roads",
			ConfigKeySQL:         "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid, highway FROM roads_lines WHERE geom && !BBOX!",
			ConfigKeySRID:        4326,
		},
		{
			ConfigKeyGeomIDField: "fid",
			ConfigKeyGeomType:    "point",
			ConfigKeyGeomField:   "geom",
			ConfigKeyLayerName:   "places",
			ConfigKeySQL:         "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, fid, place, is_in FROM places_points WHERE geom && !BBOX!",
			ConfigKeySRID:        4326,
		},
	}
}

func TestMVTProviders(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	type tcase struct {
		TCConfig
		layerNames []string
		err        string
		tile       provider.Tile
	}
	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			config := tc.Config(DefaultEnvConfig)
			config[ConfigKeyName] = "provider_name"
			prvd, err := NewMVTTileProvider(config, nil)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Logf("error %#v", err)
					t.Errorf("expected error with %v in NewMVTTileProvider, got: %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Errorf("NewMVTTileProvider unexpected error: %v", err)
				return
			}
			layers := make([]provider.Layer, len(tc.layerNames))

			for i := range tc.layerNames {
				layers[i] = provider.Layer{
					Name:    tc.layerNames[i],
					MVTName: tc.layerNames[i],
				}
			}
			mvtTile, err := prvd.MVTForLayers(context.Background(), tc.tile, nil, layers)
			if err != nil {
				t.Errorf("NewProvider unexpected error: %v", err)
				return
			}
			assertMVTForLayers(t, mvtTile, tc.layerNames)
		}
	}
	tests := map[string]tcase{
		"1": {
			TCConfig: TCConfig{
				LayerConfig: []map[string]any{
					{
						ConfigKeyGeomIDField: "gid",
						ConfigKeyGeomType:    "multipolygon",
						ConfigKeyGeomField:   "geom",
						ConfigKeyLayerName:   "land",
						ConfigKeySQL:         "SELECT ST_AsMVTGeom(geom,!BBOX!) as geom, gid, scalerank FROM ne_10m_land_scale_rank WHERE geom && !BBOX!",
						ConfigKeySRID:        4326,
					},
				},
			},
			layerNames: []string{"land"},
			tile:       provider.NewTile(0, 0, 0, 16, 4326),
		},
		// The Athens OSM extract, reached through ST_AsMVT. It used to be
		// served out of a GeoPackage, which was its only home until this
		// fixture existed; that provider is gone now, and this is the path
		// that replaced it. The three layers and their id_fieldname are the
		// ones .github/cite/config.toml serves, so this covers the same ground
		// the OGC conformance suite does without needing TeamEngine to run.
		//
		// The tile is the one .github/workflows/ogc_cite.yml passes to run.sh for
		// WebMercatorQuad, deliberately: a tile chosen independently could drift
		// into empty ground and turn assertMVTForLayers' "has no features" check
		// into a false alarm about the fixture. This one holds 2 land polygons,
		// 629 roads and 2 places.
		//
		// The layers are SRID 4326 while the tile is WebMercatorQuad, which is
		// the interesting case: !BBOX! has to arrive already transformed into the
		// layer's SRID, not the tile's.
		"athens, on the tile the conformance suite runs": {
			TCConfig:   TCConfig{LayerConfig: athensCiteLayers()},
			layerNames: []string{"land", "roads", "places"},
			tile:       provider.NewTile(14, 9271, 6324, 16, 3857),
		},
		// The other half of the pair above. The conformance config serves one map
		// on both schemes, so a provider that could only answer for one of them
		// would pass CI on WebMercatorQuad and fail the suite on WorldCRS84Quad.
		// Here tile SRID and layer SRID are both 4326, so !BBOX! is the tile's own
		// extent untransformed -- the case .github/cite/config.toml's SQL comment
		// explains the choice of unwrapped ST_AsMVTGeom for.
		//
		// Athens again, but not the same tile: a WorldCRS84Quad z14 tile is half
		// the width and a bit over half the height of the WebMercatorQuad one, so
		// the ground the other case covers spills across four tiles here. This is
		// the nearest tile in the same column that holds all three layers -- 2 land
		// polygons, 168 roads and 1 place -- and holding all three is the point:
		// assertMVTForLayers only sees a layer ST_AsMVT emitted, and ST_AsMVT
		// emits nothing at all for a layer with no rows.
		"athens, on the WorldCRS84Quad tile the conformance suite runs": {
			TCConfig:   TCConfig{LayerConfig: athensCiteLayers()},
			layerNames: []string{"land", "roads", "places"},
			tile:       provider.NewTile(14, 18542, 4740, 16, 4326),
		},
	}
	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// assertMVTForLayers checks a tile against the layers its config asked for.
//
// Decoding goes through internal/mvttest (MAPCO-11546), which is also what the
// server-side tile-content checks use. One decoder in the tree, so a tile is
// described the same way wherever it is inspected -- and the specification's
// delta-encoded geometry is resolved in one place rather than in each caller.
//
// Layers are matched by name rather than by position. The specification does
// not order layers within a tile, so reading them positionally pins something
// ST_AsMVT is free to change.
func assertMVTForLayers(t *testing.T, data []byte, expectedLayerNames []string) {
	t.Helper()

	tile := mvttest.DecodeRaw(t, data)

	if len(tile.Layers) != len(expectedLayerNames) {
		t.Fatalf("layer count = %d, want %d", len(tile.Layers), len(expectedLayerNames))
	}

	for _, name := range expectedLayerNames {
		layer, ok := tile.Layer(name)
		if !ok {
			t.Errorf("layer %q is missing; the tile holds %v", name, tile.LayerNames())
			continue
		}
		if len(layer.Features) == 0 {
			t.Errorf("layer %q has no features", name)
		}
	}
}

func TestLayerGeomType(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	type tcase struct {
		TCConfig
		layerName string
		geom      geom.Geometry
		err       string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			config := tc.Config(DefaultEnvConfig)
			config[ConfigKeyName] = "provider_name"
			provider, err := NewMVTTileProvider(config, nil)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Logf("error %#v", err)
					t.Errorf("expected error with %v in NewProvider, got: %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Errorf("NewProvider unexpected error: %v", err)
				return
			}

			p := provider.(*Provider)
			layer := p.layers[tc.layerName]

			if !reflect.DeepEqual(tc.geom, layer.geomType) {
				t.Errorf("geom type, expected %v got %v", tc.geom, layer.geomType)
				return
			}
		}
	}

	tests := map[string]tcase{
		"1": {
			TCConfig: TCConfig{
				LayerConfig: []map[string]any{
					{
						ConfigKeyLayerName: "land",
						ConfigKeySQL:       "SELECT gid, ST_AsBinary(geom) FROM ne_10m_land_scale_rank WHERE geom && !BBOX!",
					},
				},
			},
			layerName: "land",
			geom:      geom.MultiPolygon{},
		},
		"zoom token replacement": {
			TCConfig: TCConfig{
				LayerConfig: []map[string]any{
					{
						ConfigKeyLayerName: "land",
						ConfigKeySQL:       "SELECT gid, ST_AsBinary(geom) FROM ne_10m_land_scale_rank WHERE gid = !ZOOM! AND geom && !BBOX!",
					},
				},
			},
			layerName: "land",
			geom:      geom.MultiPolygon{},
		},
		"configured geometry_type": {
			TCConfig: TCConfig{
				LayerConfig: []map[string]any{
					{
						ConfigKeyLayerName: "land",
						ConfigKeyGeomType:  "multipolygon",
						ConfigKeySQL:       "SELECT gid, ST_AsBinary(geom) FROM invalid_table_to_check_query_table_was_not_inspected WHERE geom && !BBOX!",
					},
				},
			},
			layerName: "land",
			geom:      geom.MultiPolygon{},
		},
		"configured geometry_type (case insensitive)": {
			TCConfig: TCConfig{
				LayerConfig: []map[string]any{
					{
						ConfigKeyLayerName: "land",
						ConfigKeyGeomType:  "MultiPolyGOn",
						ConfigKeySQL:       "SELECT gid, ST_AsBinary(geom) FROM invalid_table_to_check_query_table_was_not_inspected WHERE geom && !BBOX!",
					},
				},
			},
			layerName: "land",
			geom:      geom.MultiPolygon{},
		},
		"invalid configured geometry_type": {
			TCConfig: TCConfig{
				LayerConfig: []map[string]any{
					{
						ConfigKeyLayerName: "land",
						ConfigKeyGeomType:  "invalid",
						ConfigKeySQL:       "SELECT gid, ST_AsBinary(geom) FROM invalid_table_to_check_query_table_was_not_inspected WHERE geom && !BBOX!",
					},
				},
			},
			layerName: "land",
			geom:      geom.MultiPolygon{},
			err:       "unsupported geometry_type",
		},
		"role no access to table": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: ttools.GetEnvDefault(
						"PGURI_NO_ACCESS",
						"postgres://shigola_no_access:postgres@localhost:5432/shigola",
					),
				},
				LayerConfig: []map[string]any{
					{
						ConfigKeyLayerName: "land",
						ConfigKeySQL:       "SELECT gid, ST_AsBinary(geom) FROM ne_10m_land_scale_rank WHERE geom && !BBOX!",
					},
				},
			},
			layerName: "land",
			geom:      geom.MultiPolygon{},
			err:       "error fetching geometry type for layer (land): ERROR: permission denied for table ne_10m_land_scale_rank (SQLSTATE 42501)",
		},
		"configure from postgreql URI": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: DefaultEnvConfig["uri"],
				},
				LayerConfig: []map[string]any{
					{
						ConfigKeyLayerName: "land",
						ConfigKeySQL:       "SELECT gid, ST_AsBinary(geom) FROM ne_10m_land_scale_rank WHERE geom && !BBOX!",
					},
				},
			},
			layerName: "land",
			geom:      geom.MultiPolygon{},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestBuildUri(t *testing.T) {
	type tcase struct {
		TCConfig
		expectedUri string
		err         string
	}

	tests := map[string]tcase{
		"add sslmode to uri if missing": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: "postgres://postgres:postgres@localhost:5432/shigola",
				},
			},
			expectedUri: "postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable",
		},
		"add sslmode of uri and dont overwrite with default": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: "postgres://postgres:postgres@localhost:5432/shigola?sslmode=prefer",
				},
			},
			expectedUri: "postgres://postgres:postgres@localhost:5432/shigola?sslmode=prefer",
		},
		"invalid uri": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: false,
				},
			},
			err: "config: value mapped to \"uri\" is bool not string",
		},
		"invalid uri scheme": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: "http://hi.de",
				},
			},
			err: "postgis: invalid uri (invalid connection scheme (http))",
		},
		"invalid uri missing user": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: "postgres://hi.de",
				},
			},
			err: "postgis: invalid uri (auth credentials missing)",
		},
		"invalid uri missing port": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: "postgres://postgres:postgres@localhost/bla",
				},
			},
			err: "postgis: splitting host port error: address localhost: missing port in address",
		},
		"invalid uri missing host": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: "postgres://postgres:postgres@:5432/bla",
				},
			},
			err: "postgis: invalid uri (address :5432: missing host in address)",
		},
		"invalid uri missing database": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeyURI: "postgres://postgres:postgres@localhost:5432",
				},
			},
			err: "postgis: invalid uri (missing database)",
		},
		"invalid sslmode": {
			TCConfig: TCConfig{
				ConfigOverride: map[string]any{
					ConfigKeySSLMode: false,
				},
			},
			err: "config: value mapped to \"ssl_mode\" is bool not string",
		},
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			config := tc.Config(DefaultConfig)
			uri, _, err := BuildURI(config)

			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Logf("error %#v", err)
					t.Errorf("expected error with %v in BuildURI, got: %v", tc.err, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if uri.String() != tc.expectedUri {
				t.Errorf("expected: %v, got: %v", tc.expectedUri, uri)
			}
		}
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestPGXOnNotice(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	tc := &TCConfig{}
	// DefaultEnvConfig, not DefaultConfig: this test opens a real connection, so
	// it has to honour PGURI like every other connecting test here. DefaultConfig
	// hardcodes localhost:5432, which is not where the database is when the tests
	// run inside the devcontainer -- and TestBuildUri above still uses it, because
	// asserting how a URI is built needs a fixed input, not the environment's.
	c := tc.Config(DefaultEnvConfig)
	uri, _, err := BuildURI(c)
	if err != nil {
		t.Fatal("building the uri should not fail:", err)
	}

	dbconfig, err := BuildDBConfig(&DBConfigOptions{Uri: uri.String()})
	if err != nil {
		t.Fatal("building db config should not fail:", err)
	}
	if dbconfig.ConnConfig.Tracer == nil {
		t.Fatal("tracer should not be nil on dbconfig")
	}

	var noticeBuffer bytes.Buffer

	// Set the OnNotice callback to write the notice messages into our buffer.
	dbconfig.ConnConfig.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		noticeBuffer.WriteString(n.Message)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), dbconfig)
	if err != nil {
		t.Fatal("creating a pool from config should not fail:", err)
	}
	defer pool.Close()

	r, err := pool.Query(context.Background(), "SELECT test_warning_log();")
	if err != nil {
		t.Fatal("querying a row should not fail:", err)
	}
	t.Cleanup(func() {
		r.Close()
	})

	for r.Next() {
		var result string
		if err := r.Scan(&result); err != nil {
			t.Fatalf("failed to scan row: %v", err)
		}
	}

	if err := r.Err(); err != nil {
		t.Fatalf("error during row iteration: %v", err)
	}

	expectedMsg := "This is a test warning message"
	if !strings.Contains(noticeBuffer.String(), expectedMsg) {
		t.Errorf(
			"expected notice message %q not found in buffer, got: %s",
			expectedMsg,
			noticeBuffer.String(),
		)
	}
}
