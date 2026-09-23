package postgis

import "testing"

// TestSQLDebugFlags pins SQL debugging to the environment-variable shim: the
// documented SHIGOLA_SQL_DEBUG name has to work, and the TEGOLA_SQL_DEBUG name
// a deployment may still set has to keep working rather than go quiet.
func TestSQLDebugFlags(t *testing.T) {
	type tcase struct {
		env         map[string]string
		wantLayer   bool
		wantExecute bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			t.Setenv("SHIGOLA_SQL_DEBUG", "")
			t.Setenv("TEGOLA_SQL_DEBUG", "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			layer, execute := sqlDebugFlags()
			if layer != tc.wantLayer || execute != tc.wantExecute {
				t.Errorf("got (layer %v, execute %v), expected (layer %v, execute %v)",
					layer, execute, tc.wantLayer, tc.wantExecute)
			}
		}
	}

	tests := map[string]tcase{
		"unset": {},
		"current name": {
			env:       map[string]string{"SHIGOLA_SQL_DEBUG": "LAYER_SQL"},
			wantLayer: true,
		},
		"legacy name": {
			env:         map[string]string{"TEGOLA_SQL_DEBUG": "EXECUTE_SQL"},
			wantExecute: true,
		},
		"both values": {
			env:         map[string]string{"SHIGOLA_SQL_DEBUG": "LAYER_SQL:EXECUTE_SQL"},
			wantLayer:   true,
			wantExecute: true,
		},
		"current name wins": {
			env: map[string]string{
				"SHIGOLA_SQL_DEBUG": "LAYER_SQL",
				"TEGOLA_SQL_DEBUG":  "EXECUTE_SQL",
			},
			wantLayer: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
