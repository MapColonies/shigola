package atlas

import (
	"context"
	"testing"
	"time"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/dict"
)

// waitFor polls until cond holds or the budget expires. Needed because
// cache.For detaches writes: a serve-path Set or promotion returns before the
// backend has been touched.
func waitFor(t *testing.T, budget time.Duration, cond func() bool) bool {
	t.Helper()

	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}

	return cond()
}

// TestMultiCacheEndToEnd drives a chain the way a deployment reaches it: built
// by cache.For from a config dict, over two real backends, with no fakes
// anywhere. It uses `file` rather than `memory` because importing cache/memory
// here would register a sixth backend and break TestCheckCacheTypes.
func TestMultiCacheEndToEnd(t *testing.T) {
	ctx := context.Background()
	key := &cache.Key{MapName: "osm", Z: 2, X: 1, Y: 1}

	hotPath := t.TempDir()
	durablePath := t.TempDir()

	c, err := cache.For("multi", dict.Dict{
		"layers": []map[string]interface{}{
			{"type": "file", "basepath": hotPath, "name": "hot"},
			{"type": "file", "basepath": durablePath, "name": "durable"},
		},
	})
	if err != nil {
		t.Fatalf("building the chain: unexpected error: %v", err)
	}

	tiered, ok := c.(cache.Tiered)
	if !ok {
		t.Fatal("the chain does not satisfy cache.Tiered, so atlas could never instrument its tiers")
	}
	if tiers := tiered.Tiers(); len(tiers) != 2 || tiers[0].Name != "hot" || tiers[1].Name != "durable" {
		t.Fatalf("tiers: got %v", tiers)
	}

	// a lens onto the hot tier alone, to see what promotion actually did
	hot, err := cache.For("file", dict.Dict{"basepath": hotPath})
	if err != nil {
		t.Fatalf("building the hot-tier lens: unexpected error: %v", err)
	}

	// Write the durable tier only — what `tegola cache seed` does by default,
	// including its synchronous writes. Without those the write is handed to
	// the detached pool and Set returns before anything has been written, which
	// is exactly why the CLI path sets the flag.
	seedCtx := cache.WithSynchronousWrites(cache.WithWriteTiers(ctx, []string{"durable"}))
	if err := c.Set(seedCtx, key, []byte("tile")); err != nil {
		t.Fatalf("set: unexpected error: %v", err)
	}
	if _, hit, err := hot.Get(ctx, key); err != nil || hit {
		t.Fatalf("hot tier after a durable-only write: got (hit %v, err %v), expected (false, nil)", hit, err)
	}

	// A serve-path read finds it in the durable tier and promotes it up.
	val, hit, err := c.Get(ctx, key)
	if err != nil || !hit || string(val) != "tile" {
		t.Fatalf("get: got (%q, %v, %v), expected (tile, true, nil)", val, hit, err)
	}
	// The promotion is detached, so it has not necessarily landed yet — Get
	// deliberately does not wait on it.
	if !waitFor(t, 5*time.Second, func() bool {
		_, hit, err := hot.Get(ctx, key)
		return err == nil && hit
	}) {
		t.Fatal("the tile was never promoted into the hot tier")
	}

	// Purge clears every tier.
	if err := c.Purge(ctx, key); err != nil {
		t.Fatalf("purge: unexpected error: %v", err)
	}
	if _, hit, err := c.Get(ctx, key); err != nil || hit {
		t.Fatalf("get after purge: got (hit %v, err %v), expected (false, nil)", hit, err)
	}
}
