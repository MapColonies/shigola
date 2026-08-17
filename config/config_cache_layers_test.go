package config_test

import (
	"strings"
	"testing"

	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/dict"
)

// TestCacheLayersParse pins the one claim the layered-cache config shape rests
// on that belongs to third-party decoding rather than to this repo: that
// `[[cache.layers]]` — and `[[cache.layers.layers]]` beneath it — survives the
// TOML decoder, env.Dict.UnmarshalTOML → env.ParseDict, and comes back out of
// env.Dict.MapSlice as a []dict.Dicter, with ${ENV} expansion still working at
// depth.
//
// ParseDict stores arrays raw in its `default:` branch and MapSlice asserts
// exactly []map[string]interface{}, so nothing in between re-wraps them. That
// is the behaviour under test; it had been traced by eye and never executed.
func TestCacheLayersParse(t *testing.T) {
	const conf = `
[cache]
type = "multi"

  [[cache.layers]]
  type       = "redis"
  ttl        = 3600
  timeout_ms = 35
  password   = "${TEST_REDIS_PASSWORD}"

  [[cache.layers]]
  type = "multi"

    [[cache.layers.layers]]
    type   = "s3"
    bucket = "${TEST_S3_BUCKET}"

    [[cache.layers.layers]]
    type     = "file"
    basepath = "/tmp/tegola"
`

	t.Setenv("TEST_REDIS_PASSWORD", "hunter2")
	t.Setenv("TEST_S3_BUCKET", "tiles-from-the-environment")

	c, err := config.Parse(strings.NewReader(conf), "")
	if err != nil {
		t.Fatalf("parse: unexpected error: %v", err)
	}

	cacheType, err := c.Cache.String("type", nil)
	if err != nil {
		t.Fatalf("cache type: unexpected error: %v", err)
	}
	if cacheType != "multi" {
		t.Errorf("cache type: got %q, expected %q", cacheType, "multi")
	}

	layers, err := c.Cache.MapSlice("layers")
	if err != nil {
		t.Fatalf("cache layers: unexpected error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("cache layers: got %d, expected 2", len(layers))
	}

	// tier 0: a leaf, with an ${ENV} value one level down
	assertString(t, layers[0], "type", "redis")
	assertInt(t, layers[0], "ttl", 3600)
	assertInt(t, layers[0], "timeout_ms", 35)
	assertString(t, layers[0], "password", "hunter2")

	// tier 1: a nested chain. Real nesting needs the key to nest —
	// [[cache.layers.layers]] — because TOML indentation is cosmetic and
	// repeated [[cache.layers]] headers would be siblings however indented.
	assertString(t, layers[1], "type", "multi")

	nested, err := layers[1].MapSlice("layers")
	if err != nil {
		t.Fatalf("nested layers: unexpected error: %v", err)
	}
	if len(nested) != 2 {
		t.Fatalf("nested layers: got %d, expected 2", len(nested))
	}

	assertString(t, nested[0], "type", "s3")
	// the load-bearing half: expansion runs at String()-read time, not at parse
	// time, so it still fires two levels of array-of-table down.
	assertString(t, nested[0], "bucket", "tiles-from-the-environment")
	assertString(t, nested[1], "type", "file")
	assertString(t, nested[1], "basepath", "/tmp/tegola")
}

// TestCacheLayersSiblingsAreNotNesting pins the trap next to the feature: an
// indented [[cache.layers]] is still a sibling. If this ever starts reporting a
// nested chain, the config above is testing something else than it reads as.
func TestCacheLayersSiblingsAreNotNesting(t *testing.T) {
	const conf = `
[cache]
type = "multi"

  [[cache.layers]]
  type = "redis"

    [[cache.layers]]
    type = "s3"
`

	c, err := config.Parse(strings.NewReader(conf), "")
	if err != nil {
		t.Fatalf("parse: unexpected error: %v", err)
	}

	layers, err := c.Cache.MapSlice("layers")
	if err != nil {
		t.Fatalf("cache layers: unexpected error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("cache layers: got %d, expected 2 siblings", len(layers))
	}

	assertString(t, layers[0], "type", "redis")
	assertString(t, layers[1], "type", "s3")

	for i := range layers {
		nested, err := layers[i].MapSlice("layers")
		if err != nil {
			t.Fatalf("layer %d: unexpected error: %v", i, err)
		}
		if len(nested) != 0 {
			t.Errorf("layer %d: got %d nested layers, expected none", i, len(nested))
		}
	}
}

func assertString(t *testing.T, d dict.Dicter, key, expected string) {
	t.Helper()

	got, err := d.String(key, nil)
	if err != nil {
		t.Errorf("%v: unexpected error: %v", key, err)
		return
	}
	if got != expected {
		t.Errorf("%v: got %q, expected %q", key, got, expected)
	}
}

func assertInt(t *testing.T, d dict.Dicter, key string, expected int) {
	t.Helper()

	got, err := d.Int(key, nil)
	if err != nil {
		t.Errorf("%v: unexpected error: %v", key, err)
		return
	}
	if got != expected {
		t.Errorf("%v: got %d, expected %d", key, got, expected)
	}
}
