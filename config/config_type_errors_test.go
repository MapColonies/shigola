package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/MapColonies/shigola/config"
)

// TestParseTypeErrorMessages pins what a mistyped config value tells the
// operator (MAPCO-11617). Each case is a config fragment and the substrings the
// resulting error must and must not contain.
func TestParseTypeErrorMessages(t *testing.T) {
	type tcase struct {
		config  string
		want    []string
		notWant []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			_, err := config.Parse(strings.NewReader(tc.config), "")
			if err == nil {
				t.Fatal("Parse() = nil error, want the value rejected")
			}

			got := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Parse() error = %q, want it to contain %q", got, want)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("Parse() error = %q, want it not to contain %q", got, notWant)
				}
			}
		}
	}

	tests := map[string]tcase{
		// The lexer rejects this before any type reaches our code, so the
		// expected type has to be read off the field the key names.
		"unquoted garbage for a boolean": {
			config: "[[maps]]\nname = \"osm\"\nserve_layer_collections = xx\n",
			want: []string{
				`last key parsed 'maps.serve_layer_collections'`,
				`expected value but found "xx" instead`,
				"(maps.serve_layer_collections takes a boolean, true or false)",
			},
		},
		"a quoted non-boolean": {
			config:  "[[maps]]\nname = \"osm\"\nserve_layer_collections = \"xx\"\n",
			want:    []string{`"xx" is not a boolean (true or false)`},
			notWant: []string{"strconv"},
		},
		"a number for a boolean": {
			config:  "[[maps]]\nname = \"osm\"\nserve_layer_collections = 1\n",
			want:    []string{"1 (int64) is not a boolean (true or false)"},
			notWant: []string{"%!"},
		},
		"a missing env var": {
			config: "[[maps]]\nname = \"osm\"\nserve_layer_collections = \"${MAPCO_11617_UNSET}\"\n",
			want:   []string{`environment variable "MAPCO_11617_UNSET" not found`},
		},
		"unquoted garbage for a nested unsigned key": {
			config: "[[maps]]\nname = \"osm\"\n[[maps.layers]]\nmin_zoom = abc\n",
			want:   []string{"(maps.layers.min_zoom takes a non-negative integer)"},
		},
		"unquoted garbage for a string": {
			config: "[webserver]\nport = xx\n",
			want:   []string{"(webserver.port takes a string)"},
		},
		"unquoted garbage for a pointer to an int": {
			config: "tile_buffer = xx\n",
			want:   []string{"(tile_buffer takes an integer)"},
		},
		"unquoted garbage in an array of floats": {
			config: "[[maps]]\nname = \"osm\"\nbounds = [1.0, x]\n",
			want:   []string{"(maps.bounds takes an array of floating-point numbers)"},
		},
		"unquoted garbage in a fixed-length array": {
			config: "[[maps]]\nname = \"osm\"\ncenter = [1.0, x]\n",
			want:   []string{"(maps.center takes an array of floating-point numbers)"},
		},
		"unquoted garbage in a section struct": {
			config: "[tracing]\nenabled = yes\n",
			want:   []string{"(tracing.enabled takes a boolean, true or false)"},
		},

		// Everything below must stay silent: a hint is only worth giving when
		// the field's kind says unambiguously what TOML value it takes.

		// env.URL is a struct written as a string.
		"env.URL gets no hint": {
			config:  "[webserver]\nhostname = xx\n",
			notWant: []string{"takes"},
		},
		// env.Dict is a map written as a table, with keys of any type.
		"a key inside an env.Dict gets no hint": {
			config:  "[webserver.headers]\nX-Foo = xx\n",
			notWant: []string{"takes"},
		},
		"a key inside a provider gets no hint": {
			config:  "[[providers]]\nname = xx\n",
			notWant: []string{"takes"},
		},
		// A structural error leaves the table itself as the last key, and an
		// array of tables is not something to describe.
		"a structural error in an array of tables gets no hint": {
			config:  "[[maps]]\nname = \"osm\"\nfoo bar\n",
			notWant: []string{"takes"},
		},
		"an unknown key gets no hint": {
			config:  "[[maps]]\nnope = xx\n",
			notWant: []string{"takes"},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestParseErrorStaysAParseError guards the hint against hiding the structured
// error: a caller can still reach the line and key through errors.As.
func TestParseErrorStaysAParseError(t *testing.T) {
	_, err := config.Parse(strings.NewReader("[[maps]]\nserve_layer_collections = xx\n"), "")

	var pe toml.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Parse() error = %#v, want it to wrap a toml.ParseError", err)
	}
	if pe.LastKey != "maps.serve_layer_collections" {
		t.Errorf("LastKey = %q, want %q", pe.LastKey, "maps.serve_layer_collections")
	}
}
