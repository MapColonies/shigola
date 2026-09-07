package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/config"
)

// TestRemovedMapLayerKeysAreRejected covers the three per-layer switches that
// configured the Go-side encode path (MAPCO-11491).
//
// The decoder ignores a key it has no field for, so deleting the fields alone
// would leave a config setting one loading happily and meaning nothing. That is
// the case this test exists to keep failing loudly.
func TestRemovedMapLayerKeysAreRejected(t *testing.T) {
	type tcase struct {
		toml string
		key  string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			_, err := config.Parse(strings.NewReader(tc.toml), "test")

			var removed config.ErrRemovedMapLayerKey
			if !errors.As(err, &removed) {
				t.Fatalf("Parse() = %v, want ErrRemovedMapLayerKey", err)
			}
			if !strings.HasSuffix(removed.Key, tc.key) {
				t.Errorf("Key = %q, want it to end in %q", removed.Key, tc.key)
			}
			if !strings.Contains(removed.Error(), "delete it") {
				t.Errorf("Error() = %q, want it to say what to do", removed.Error())
			}
		}
	}

	layer := func(key string) string {
		return `
[[maps]]
name = "osm"
  [[maps.layers]]
  provider_layer = "provider1.water"
  ` + key + ` = true
`
	}

	tests := map[string]tcase{
		"dont_simplify": {toml: layer("dont_simplify"), key: "dont_simplify"},
		"dont_clip":     {toml: layer("dont_clip"), key: "dont_clip"},
		"dont_clean":    {toml: layer("dont_clean"), key: "dont_clean"},
		// default_tags is a table rather than a scalar, so it is reported at a
		// different depth than the three above -- which is why the check looks
		// at every segment of the key path and not only the last.
		"default_tags": {
			toml: `
[[maps]]
name = "osm"
  [[maps.layers]]
  provider_layer = "provider1.water"
    [maps.layers.default_tags]
    kind = "water"
`,
			key: "default_tags",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestUnrelatedUnknownKeysStillLoad is the other half: only keys removed on
// purpose are rejected, so a typo or a key from a newer shigola does not fail
// a config that is otherwise fine.
func TestUnrelatedUnknownKeysStillLoad(t *testing.T) {
	const cfg = `
[[maps]]
name = "osm"
some_key_from_the_future = true
  [[maps.layers]]
  provider_layer = "provider1.water"
`

	if _, err := config.Parse(strings.NewReader(cfg), "test"); err != nil {
		t.Errorf("Parse() = %v, want nil", err)
	}
}
