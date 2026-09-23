package config

import (
	"reflect"
	"testing"
	"time"

	"github.com/MapColonies/shigola/internal/env"
)

// duration is kind int64 but written as a string, like "5s": the case
// kindIsTruthful exists for, which no Config field exercises today.
type duration time.Duration

func (d *duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	*d = duration(v)
	return err
}

// TestTypeHintTrustsOnlyEnvDecoders pins that a type decoding itself is
// described only when it is one of env's scalars, whose kind is the TOML value
// they take.
func TestTypeHintTrustsOnlyEnvDecoders(t *testing.T) {
	type section struct {
		Timeout   duration   `toml:"timeout"`
		Timeouts  []duration `toml:"timeouts"`
		TimeoutMS env.Int    `toml:"timeout_ms"`
		Untagged  env.Bool
	}

	type tcase struct {
		key    string
		want   string
		wantOK bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got, ok := typeHint(reflect.TypeOf(section{}), tc.key)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("typeHint(%q) = %q, %v, want %q, %v", tc.key, got, ok, tc.want, tc.wantOK)
			}
		}
	}

	tests := map[string]tcase{
		"a text unmarshaler":             {key: "timeout"},
		"an array of text unmarshalers":  {key: "timeouts"},
		"an env scalar":                  {key: "timeout_ms", want: "an integer", wantOK: true},
		"an untagged field, by its name": {key: "untagged", want: "a boolean, true or false", wantOK: true},
		"past a scalar":                  {key: "timeout_ms.x"},
		"no key":                         {key: ""},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
