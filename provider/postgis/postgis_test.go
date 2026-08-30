package postgis_test

import (
	"strings"
	"testing"

	"github.com/MapColonies/shigola/internal/ttools"
	"github.com/MapColonies/shigola/provider/postgis"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDBConfig(t *testing.T) {
	uri := ttools.GetEnvDefault("PGURI", "postgres://postgres:postgres@localhost:5432/shigola")

	type tcase struct {
		opts                          *postgis.DBConfigOptions
		expApplicationName            string
		expDefaultTransactionReadOnly string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			dbconfig, err := postgis.BuildDBConfig(
				tc.opts)
			if err != nil {
				t.Errorf("unable to build config: %v", err)
			}

			applicationName := dbconfig.ConnConfig.RuntimeParams["application_name"]
			if applicationName != tc.expApplicationName {
				t.Errorf("expected application name: %s, got: %s", tc.expApplicationName, applicationName)
			}

			defaultTransactionReadOnly := dbconfig.ConnConfig.RuntimeParams["default_transaction_read_only"]
			if defaultTransactionReadOnly != tc.expDefaultTransactionReadOnly {
				t.Errorf("expected transaction read only: %s, got: %s", tc.expDefaultTransactionReadOnly, defaultTransactionReadOnly)
			}
		}
	}
	tests := map[string]tcase{
		"1": {
			opts: &postgis.DBConfigOptions{
				Uri:                        uri,
				ApplicationName:            "tegola",
				DefaultTransactionReadOnly: "TRUE",
			},
			expApplicationName:            "tegola",
			expDefaultTransactionReadOnly: "TRUE",
		},
		"2": {
			opts: &postgis.DBConfigOptions{
				Uri:                        uri,
				ApplicationName:            "aloget",
				DefaultTransactionReadOnly: "OFF",
			},
			expApplicationName:            "aloget",
			expDefaultTransactionReadOnly: "",
		},
		"3": {
			opts: &postgis.DBConfigOptions{
				Uri:                        uri,
				ApplicationName:            "tegola",
				DefaultTransactionReadOnly: "FALSE",
			},
			expApplicationName:            "tegola",
			expDefaultTransactionReadOnly: "FALSE",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestTLSConfig(t *testing.T) {
	uri := "postgres://testuser:testpassword@testhost:5432/testdb"

	testConnConfig, err := postgis.BuildDBConfig(
		&postgis.DBConfigOptions{
			Uri:                        uri,
			DefaultTransactionReadOnly: "TRUE",
			ApplicationName:            "tegola",
		})
	if err != nil {
		t.Fatalf("unable to build db config: %v", err)
	}

	type tcase struct {
		sslMode     string
		sslKey      string
		sslCert     string
		sslRootCert string
		testFunc    func(config *pgxpool.Config)
		shouldError bool
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			err := postgis.ConfigTLS(tc.sslMode, tc.sslKey, tc.sslCert, tc.sslRootCert, testConnConfig)
			if !tc.shouldError && err != nil {
				t.Errorf("unable to create a new provider: %v", err)
				return
			} else if tc.shouldError && err == nil {
				t.Errorf("Error expected but got no error")
				return
			}

			tc.testFunc(testConnConfig)
		}
	}

	tests := map[string]tcase{
		"1": {
			sslMode:     "",
			sslKey:      "",
			sslCert:     "",
			sslRootCert: "",
			shouldError: true,
			testFunc: func(config *pgxpool.Config) {
			},
		},
		"2": {
			sslMode:     "disable",
			sslKey:      "",
			sslCert:     "",
			sslRootCert: "",
			shouldError: false,
			testFunc: func(config *pgxpool.Config) {
				if config.ConnConfig.TLSConfig != nil {
					t.Errorf("When using disable ssl mode; UseFallbackTLS, expected nil got %v", testConnConfig.ConnConfig.TLSConfig)
				}
			},
		},
		"3": {
			sslMode:     "allow",
			sslKey:      "",
			sslCert:     "",
			sslRootCert: "",
			shouldError: false,
			testFunc: func(config *pgxpool.Config) {
				if config.ConnConfig.TLSConfig.InsecureSkipVerify == false {
					t.Error("When using allow ssl mode; UseFallbackTLS.InsecureSkipVerify, expected true got false")
				}
			},
		},
		"4": {
			sslMode:     "prefer",
			sslKey:      "",
			sslCert:     "",
			sslRootCert: "",
			shouldError: false,
			testFunc: func(config *pgxpool.Config) {
				if config.ConnConfig.TLSConfig == nil {
					t.Error("When using prefer ssl mode; TLSConfig, expected not nil got nil")
				}

				if config.ConnConfig.TLSConfig != nil && config.ConnConfig.TLSConfig.InsecureSkipVerify == false {
					t.Error("When using prefer ssl mode; TLSConfig.InsecureSkipVerify, expected true got false")
				}
			},
		},
		"5": {
			sslMode:     "require",
			sslKey:      "",
			sslCert:     "",
			sslRootCert: "",
			shouldError: false,
			testFunc: func(config *pgxpool.Config) {
				if config.ConnConfig.TLSConfig == nil {
					t.Error("When using prefer ssl mode; TLSConfig, expected not nil got nil")
				}

				if config.ConnConfig.TLSConfig != nil && config.ConnConfig.TLSConfig.InsecureSkipVerify == false {
					t.Error("When using prefer ssl mode; TLSConfig.InsecureSkipVerify, expected true got false")
				}
			},
		},
		"6": {
			sslMode:     "verify-ca",
			sslKey:      "",
			sslCert:     "",
			sslRootCert: "",
			shouldError: false,
			testFunc: func(config *pgxpool.Config) {
				if config.ConnConfig.TLSConfig == nil {
					t.Error("When using prefer ssl mode; TLSConfig, expected not nil got nil")
				}

				if config.ConnConfig.TLSConfig != nil && config.ConnConfig.TLSConfig.ServerName != testConnConfig.ConnConfig.Host {
					t.Errorf("When using prefer ssl mode; TLSConfig.ServerName, expected %s got %s", testConnConfig.ConnConfig.Host, config.ConnConfig.TLSConfig.ServerName)
				}
			},
		},
		"7": {
			sslMode:     "verify-full",
			sslKey:      "",
			sslCert:     "",
			sslRootCert: "",
			shouldError: false,
			testFunc: func(config *pgxpool.Config) {
				if config.ConnConfig.TLSConfig == nil {
					t.Error("When using prefer ssl mode; TLSConfig, expected not nil got nil")
				}

				if config.ConnConfig.TLSConfig != nil && config.ConnConfig.TLSConfig.ServerName != testConnConfig.ConnConfig.Host {
					t.Errorf("When using prefer ssl mode; TLSConfig.ServerName, expected %s got %s", testConnConfig.ConnConfig.Host, config.ConnConfig.TLSConfig.ServerName)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestNewMVTTileProvider(t *testing.T) {
	ttools.ShouldSkip(t, postgis.TESTENV)

	type tcase struct {
		postgis.TCConfig
		// wantErr is a fragment of the error the config should be rejected
		// with; empty means the provider must be constructed.
		wantErr string
	}

	fn := func(tc tcase) func(t *testing.T) {
		return func(t *testing.T) {
			config := tc.Config(postgis.DefaultEnvConfig)
			config[postgis.ConfigKeyName] = "provider_name"

			_, err := postgis.NewMVTTileProvider(config, nil)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("unable to create a new provider. err: %v", err)
			}
		}
	}

	tests := map[string]tcase{
		// geometry_type is declared rather than inferred, and has to be:
		// startup inference reads the layer's SQL back, and a query ending in
		// ST_AsMVTGeom returns tile-space geometry it cannot type. See the same
		// note in .github/cite/config.toml.
		"sql, with the geometry wrapped in ST_AsMVTGeom": {
			TCConfig: postgis.TCConfig{
				LayerConfig: []map[string]interface{}{
					{
						postgis.ConfigKeyLayerName: "land",
						postgis.ConfigKeyGeomType:  "multipolygon",
						postgis.ConfigKeySQL:       "SELECT ST_AsMVTGeom(geom,!BBOX!) AS geom, gid FROM ne_10m_land_scale_rank WHERE geom && !BBOX!",
					},
				},
			},
		},
		// tablename is a leftover of the removed standard type. It is still
		// accepted -- genSQL builds a query from it -- but the query it builds
		// selects the geometry unwrapped, and the geometry-type inspection this
		// provider does at startup cannot read raw PostGIS geometry back. So the
		// trap closes at startup rather than at request time, which is the good
		// direction, and this pins that.
		"tablename, which the MVT type cannot use": {
			TCConfig: postgis.TCConfig{
				LayerConfig: []map[string]interface{}{
					{
						postgis.ConfigKeyLayerName: "land",
						postgis.ConfigKeyTablename: "ne_10m_land_scale_rank",
					},
				},
			},
			wantErr: "error fetching geometry type for layer (land)",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
