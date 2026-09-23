package env_test

import (
	"testing"

	"github.com/MapColonies/shigola/internal/env"
)

// TestParseErrorMessages pins what an operator reads when a config value has
// the wrong type (MAPCO-11617). The value and what it should have been are the
// whole message: these parsers run inside UnmarshalTOML, which is never told
// the key it is decoding, so the value is all they can name.
func TestParseErrorMessages(t *testing.T) {
	type tcase struct {
		parse func(t *testing.T) error
		want  string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			err := tc.parse(t)
			if err == nil {
				t.Fatalf("err = nil, want %q", tc.want)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("err = %q, want %q", got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"bool from a number": {
			parse: func(t *testing.T) error { _, err := env.ParseBool(int64(1)); return err },
			want:  "1 (int64) is not a boolean (true or false)",
		},
		// strconv's own wording ("strconv.ParseBool: parsing ...") names a Go
		// function, not the config.
		"bool from a non-boolean string": {
			parse: func(t *testing.T) error { _, err := env.ParseBool("xx"); return err },
			want:  `"xx" is not a boolean (true or false)`,
		},
		"bool from an env var holding a non-boolean": {
			parse: func(t *testing.T) error {
				t.Setenv("MAPCO_11617_BOOL", "xx")
				_, err := env.ParseBool("${MAPCO_11617_BOOL}")
				return err
			},
			want: `"xx" is not a boolean (true or false)`,
		},
		"string from a number": {
			parse: func(t *testing.T) error { _, err := env.ParseString(int64(1)); return err },
			want:  "1 (int64) is not a string",
		},
		"int from a float": {
			parse: func(t *testing.T) error { _, err := env.ParseInt(1.5); return err },
			want:  "1.5 (float64) is not an integer",
		},
		"int from a non-integer string": {
			parse: func(t *testing.T) error { _, err := env.ParseInt("xx"); return err },
			want:  `"xx" is not an integer`,
		},
		"uint from a negative number": {
			parse: func(t *testing.T) error { _, err := env.ParseUint(int64(-1)); return err },
			want:  "-1 (int64) is not a non-negative integer",
		},
		"uint from a negative string": {
			parse: func(t *testing.T) error { _, err := env.ParseUint("-1"); return err },
			want:  `"-1" is not a non-negative integer`,
		},
		// TOML reads 1 as an integer, and ParseFloat takes only a float64, so
		// the message has to show what a float looks like.
		"float from an integer": {
			parse: func(t *testing.T) error { _, err := env.ParseFloat(int64(1)); return err },
			want:  "1 (int64) is not a floating-point number (e.g. 1.0)",
		},
		"float from a non-numeric string": {
			parse: func(t *testing.T) error { _, err := env.ParseFloat("xx"); return err },
			want:  `"xx" is not a floating-point number (e.g. 1.0)`,
		},
		"dict from a string": {
			parse: func(t *testing.T) error { _, err := env.ParseDict("xx"); return err },
			want:  `"xx" is not a table`,
		},
		"url from a number": {
			parse: func(t *testing.T) error { _, err := env.ParseURL(int64(1)); return err },
			want:  "1 (int64) is not a URL string",
		},
		"a missing env var still names the variable": {
			parse: func(t *testing.T) error { _, err := env.ParseBool("${MAPCO_11617_UNSET}"); return err },
			want:  `environment variable "MAPCO_11617_UNSET" not found`,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
