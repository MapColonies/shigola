package multi_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/cache/multi"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/internal/faketier"
)

var key = &cache.Key{MapName: "osm", Z: 3, X: 2, Y: 1}

var errBackend = errors.New("backend exploded")

// Tiers reachable through config, for the tests that need cache.For to build
// the chain rather than NewChain — cache.For is the only path that creates a
// write pool and applies the detachment decorator.
var (
	slowTier         = faketier.New("slow")
	gatedHotTier     = faketier.New("gatedhot")
	gatedDurableTier = faketier.New("gateddurable")
)

// Registered once, because cache.Register is a process-global map with no
// removal. Construction tests only care about names and errors, so sharing one
// instance per type is fine.
func init() {
	register := func(cacheType string, c cache.Interface) {
		if err := cache.Register(cacheType, func(dict.Dicter) (cache.Interface, error) { return c, nil }); err != nil {
			panic(err)
		}
	}

	register("fakehot", faketier.New("fakehot"))
	register("fakedurable", faketier.New("fakedurable"))
	register("fakeslow", slowTier)
	register("fakegatedhot", gatedHotTier)
	register("fakegateddurable", gatedDurableTier)
}

// chain builds a two-tier chain over fakes sharing one recorder, which is what
// makes cross-tier ordering assertable.
func chain(t *testing.T, promote bool) (*multi.Cache, *faketier.Tier, *faketier.Tier, *faketier.Recorder) {
	t.Helper()

	rec := faketier.NewRecorder()
	hot := faketier.NewWithRecorder("hot", rec)
	durable := faketier.NewWithRecorder("durable", rec)

	// nil pool: promotion runs inline, so these tests can assert on it without
	// a rendezvous. Detached promotion has its own tests in cache/.
	c, err := multi.NewChain([]cache.NamedTier{
		{Name: "hot", Cache: hot},
		{Name: "durable", Cache: durable},
	}, promote, nil)
	if err != nil {
		t.Fatalf("NewChain: unexpected error: %v", err)
	}

	return c, hot, durable, rec
}

func TestGet(t *testing.T) {
	ctx := context.Background()

	t.Run("hit in tier 0 does not query tier 1", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		hot.Seed(key, []byte("tile"))

		val, hit, err := c.Get(ctx, key)
		if err != nil || !hit || string(val) != "tile" {
			t.Fatalf("got (%q, %v, %v), expected (tile, true, nil)", val, hit, err)
		}
		if durable.Count(faketier.OpGet) != 0 {
			t.Error("the durable tier was queried after a hot-tier hit")
		}
	})

	t.Run("hit in tier 1 promotes into tier 0", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		durable.Seed(key, []byte("tile"))

		val, hit, err := c.Get(ctx, key)
		if err != nil || !hit || string(val) != "tile" {
			t.Fatalf("got (%q, %v, %v), expected (tile, true, nil)", val, hit, err)
		}
		if _, ok := hot.Value(key); !ok {
			t.Error("the tile was not promoted into the hot tier")
		}
		if stats := c.ChainStats(); stats.Promotions != 1 {
			t.Errorf("promotions: got %d, expected 1", stats.Promotions)
		}
	})

	t.Run("promotion suppressed by context", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		durable.Seed(key, []byte("tile"))

		if _, hit, err := c.Get(cache.WithoutPromotion(ctx), key); !hit || err != nil {
			t.Fatalf("got (%v, %v), expected (true, nil)", hit, err)
		}
		if _, ok := hot.Value(key); ok {
			t.Error("the tile was promoted despite WithoutPromotion")
		}
	})

	t.Run("promote_on_hit false", func(t *testing.T) {
		c, hot, durable, _ := chain(t, false)
		durable.Seed(key, []byte("tile"))

		if _, hit, err := c.Get(ctx, key); !hit || err != nil {
			t.Fatalf("got (%v, %v), expected (true, nil)", hit, err)
		}
		if _, ok := hot.Value(key); ok {
			t.Error("the tile was promoted with promote_on_hit off")
		}
	})

	t.Run("a failed promotion does not turn a hit into an error", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		durable.Seed(key, []byte("tile"))
		hot.FailOn(faketier.OpSet, errBackend)

		val, hit, err := c.Get(ctx, key)
		if err != nil {
			t.Fatalf("got %v, expected nil — the caller has a valid tile", err)
		}
		if !hit || string(val) != "tile" {
			t.Fatalf("got (%q, %v), expected (tile, true)", val, hit)
		}
	})

	t.Run("all tiers miss", func(t *testing.T) {
		c, _, _, _ := chain(t, true)

		val, hit, err := c.Get(ctx, key)
		if val != nil || hit || err != nil {
			t.Fatalf("got (%v, %v, %v), expected (nil, false, nil)", val, hit, err)
		}
	})

	t.Run("tier 0 errors, tier 1 hits", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		hot.FailOn(faketier.OpGet, errBackend)
		durable.Seed(key, []byte("tile"))

		val, hit, err := c.Get(ctx, key)
		if err != nil {
			t.Fatalf("got %v, expected nil", err)
		}
		if !hit || string(val) != "tile" {
			t.Fatalf("got (%q, %v), expected (tile, true)", val, hit)
		}
	})

	// The one that matters: an erroring chain would stop the middleware writing
	// tiles back to a healthy tier, turning a partial cache failure into a
	// total one.
	t.Run("every tier errors is a miss, not an error", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		hot.FailOn(faketier.OpGet, errBackend)
		durable.FailOn(faketier.OpGet, errBackend)

		val, hit, err := c.Get(ctx, key)
		if err != nil {
			t.Fatalf("got %v, expected nil — a dead chain is a miss", err)
		}
		if val != nil || hit {
			t.Fatalf("got (%v, %v), expected (nil, false)", val, hit)
		}
	})
}

func TestSet(t *testing.T) {
	ctx := context.Background()

	t.Run("writes every tier", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)

		if err := c.Set(ctx, key, []byte("tile")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := hot.Value(key); !ok {
			t.Error("the hot tier was not written")
		}
		if _, ok := durable.Value(key); !ok {
			t.Error("the durable tier was not written")
		}
	})

	// Strict Set is what stops a planet seed with a broken durable config
	// exiting 0 having accomplished nothing.
	t.Run("one target fails: error names it, the other is still written", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		durable.FailOn(faketier.OpSet, errBackend)

		err := c.Set(ctx, key, []byte("tile"))
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		var tierErr multi.ErrTier
		if !errors.As(err, &tierErr) {
			t.Fatalf("expected multi.ErrTier, got %T: %v", err, err)
		}
		if tierErr.Name != "durable" {
			t.Errorf("error names tier %q, expected %q", tierErr.Name, "durable")
		}
		if !errors.Is(err, errBackend) {
			t.Errorf("the backend error was not preserved: %v", err)
		}
		if _, ok := hot.Value(key); !ok {
			t.Error("a failing tier aborted the others")
		}
	})

	t.Run("write-tier subset", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)

		wctx := cache.WithWriteTiers(ctx, []string{"durable"})
		if err := c.Set(wctx, key, []byte("tile")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := hot.Value(key); ok {
			t.Error("the hot tier was written despite not being named")
		}
		if _, ok := durable.Value(key); !ok {
			t.Error("the named tier was not written")
		}
	})
}

// TestWriteTiersBoundPromotion — --cache-tiers means "the tiers I may write",
// by either route. Promotion is a write.
func TestWriteTiersBoundPromotion(t *testing.T) {
	ctx := context.Background()

	t.Run("write tiers excluding tier 0 suppresses promotion", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		durable.Seed(key, []byte("tile"))

		wctx := cache.WithWriteTiers(ctx, []string{"durable"})
		if _, hit, err := c.Get(wctx, key); !hit || err != nil {
			t.Fatalf("got (%v, %v), expected (true, nil)", hit, err)
		}
		if _, ok := hot.Value(key); ok {
			t.Error("promoted into a tier the caller said it may not write")
		}
	})

	t.Run("no write tiers set: promote_on_hit alone governs", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		durable.Seed(key, []byte("tile"))

		if _, hit, err := c.Get(ctx, key); !hit || err != nil {
			t.Fatalf("got (%v, %v), expected (true, nil)", hit, err)
		}
		if _, ok := hot.Value(key); !ok {
			t.Error("did not promote with no restriction in force")
		}
	})
}

func TestPurge(t *testing.T) {
	ctx := context.Background()

	// The ordering IS the correctness property: purging the hot tier first
	// opens a window for a concurrent read to promote the stale tile back.
	t.Run("runs durable first", func(t *testing.T) {
		c, _, _, rec := chain(t, true)

		if err := c.Purge(ctx, key); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := []string{"durable:purge", "hot:purge"}
		if trace := rec.Trace(); !reflect.DeepEqual(trace, expected) {
			t.Errorf("trace: got %v, expected %v", trace, expected)
		}
	})

	t.Run("one tier fails: error returned, others still purged", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		hot.Seed(key, []byte("tile"))
		durable.Seed(key, []byte("tile"))
		durable.FailOn(faketier.OpPurge, errBackend)

		err := c.Purge(ctx, key)
		if err == nil {
			t.Fatal("expected an error, got nil — a false success costs stale tiles served indefinitely")
		}
		if _, ok := hot.Value(key); ok {
			t.Error("a failing tier aborted the rest of the purge")
		}
	})

	t.Run("purge targets every tier regardless of write tiers", func(t *testing.T) {
		c, hot, durable, _ := chain(t, true)
		hot.Seed(key, []byte("tile"))
		durable.Seed(key, []byte("tile"))

		wctx := cache.WithWriteTiers(ctx, []string{"durable"})
		if err := c.Purge(wctx, key); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := hot.Value(key); ok {
			t.Error("the hot tier was not purged: there is no --cache-tiers equivalent for purge")
		}
	})
}

// TestInvalidateUnwritten covers `seed --overwrite`: write the targets, then
// purge what was not written.
func TestInvalidateUnwritten(t *testing.T) {
	ctx := context.Background()

	t.Run("non-target tiers are purged, after the write", func(t *testing.T) {
		c, hot, durable, rec := chain(t, true)
		hot.Seed(key, []byte("stale"))

		wctx := cache.WithInvalidateUnwritten(cache.WithWriteTiers(ctx, []string{"durable"}))
		if err := c.Set(wctx, key, []byte("fresh")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := hot.Value(key); ok {
			t.Error("the stale hot-tier tile survived --overwrite")
		}
		if val, ok := durable.Value(key); !ok || string(val) != "fresh" {
			t.Errorf("durable tier: got (%q, %v), expected (fresh, true)", val, ok)
		}

		// Write before purge, never the reverse: purging first reopens the
		// re-promotion race.
		expected := []string{"durable:set", "hot:purge"}
		if trace := rec.Trace(); !reflect.DeepEqual(trace, expected) {
			t.Errorf("trace: got %v, expected %v", trace, expected)
		}
	})

	t.Run("without the flag nothing is purged", func(t *testing.T) {
		c, _, _, rec := chain(t, true)

		wctx := cache.WithWriteTiers(ctx, []string{"durable"})
		if err := c.Set(wctx, key, []byte("fresh")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := []string{"durable:set"}
		if trace := rec.Trace(); !reflect.DeepEqual(trace, expected) {
			t.Errorf("trace: got %v, expected %v", trace, expected)
		}
	})

	t.Run("every tier written leaves nothing to purge", func(t *testing.T) {
		c, _, _, rec := chain(t, true)

		if err := c.Set(cache.WithInvalidateUnwritten(ctx), key, []byte("fresh")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, call := range rec.Calls() {
			if call.Op == faketier.OpPurge {
				t.Errorf("tier %v was purged, but every tier was a write target", call.Tier)
			}
		}
	})

	// Purge failures follow purge strictness. A tile whose durable write
	// succeeded but whose hot-tier purge failed is reported failed, because a
	// silent success would say the data propagated while a tier serves the old
	// bytes.
	t.Run("a failing purge fails the tile", func(t *testing.T) {
		c, hot, _, _ := chain(t, true)
		hot.FailOn(faketier.OpPurge, errBackend)

		wctx := cache.WithInvalidateUnwritten(cache.WithWriteTiers(ctx, []string{"durable"}))
		err := c.Set(wctx, key, []byte("fresh"))
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		var tierErr multi.ErrTier
		if !errors.As(err, &tierErr) || tierErr.Op != "purge" {
			t.Fatalf("expected a purge ErrTier, got %T: %v", err, err)
		}
	})
}

func TestConstruction(t *testing.T) {
	type tcase struct {
		config      dict.Dict
		expectedErr error
		// tierNames, when non-nil, is the expected resolved name list.
		tierNames []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			c, err := multi.New(tc.config)

			if tc.expectedErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.expectedErr)
				}
				if !errors.Is(err, tc.expectedErr) && reflect.TypeOf(err) != reflect.TypeOf(tc.expectedErr) {
					t.Fatalf("expected %T (%v), got %T (%v)", tc.expectedErr, tc.expectedErr, err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.tierNames != nil {
				if names := multi.TierNames(c); !reflect.DeepEqual(names, tc.tierNames) {
					t.Errorf("tier names: got %v, expected %v", names, tc.tierNames)
				}
			}
		}
	}

	tests := map[string]tcase{
		"zero layers": {
			config:      dict.Dict{},
			expectedErr: multi.ErrNoLayers,
		},
		"empty layer list": {
			config:      dict.Dict{"layers": []map[string]interface{}{}},
			expectedErr: multi.ErrNoLayers,
		},
		"a layer without a type": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"ttl": 3600},
			}},
			expectedErr: dict.ErrKeyRequired("type"),
		},
		"names default to the type": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"type": "fakehot"},
				{"type": "fakedurable"},
			}},
			tierNames: []string{"fakehot", "fakedurable"},
		},
		"a repeated type gets a collision suffix": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"type": "fakehot"},
				{"type": "fakehot"},
			}},
			tierNames: []string{"fakehot", "fakehot#2"},
		},
		"an explicit name wins": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"type": "fakehot", "name": "hot"},
				{"type": "fakedurable", "name": "durable"},
			}},
			tierNames: []string{"hot", "durable"},
		},
		"duplicate explicit names": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"type": "fakehot", "name": "tiles"},
				{"type": "fakedurable", "name": "tiles"},
			}},
			expectedErr: multi.ErrDuplicateTierName{},
		},
		"an explicit name colliding with a derived one": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"type": "fakehot"},
				{"type": "fakedurable", "name": "fakehot"},
			}},
			expectedErr: multi.ErrDuplicateTierName{},
		},
		"a bad timeout_ms on a tier": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"type": "fakehot", "timeout_ms": 0},
			}},
			expectedErr: cache.ErrInvalidTimeout{},
		},
		"an unregistered layer type": {
			config: dict.Dict{"layers": []map[string]interface{}{
				{"type": "no-such-cache-backend"},
			}},
			expectedErr: errors.New(""),
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// nested builds hot → (inner1 → inner2), fresh, so subtests do not observe each
// other's writes.
func nested(t *testing.T) (*multi.Cache, *faketier.Tier, *faketier.Tier, *faketier.Tier) {
	t.Helper()

	rec := faketier.NewRecorder()
	hot := faketier.NewWithRecorder("hot", rec)
	inner1 := faketier.NewWithRecorder("inner1", rec)
	inner2 := faketier.NewWithRecorder("inner2", rec)

	inner, err := multi.NewChain([]cache.NamedTier{
		{Name: "inner1", Cache: inner1},
		{Name: "inner2", Cache: inner2},
	}, true, nil)
	if err != nil {
		t.Fatalf("inner chain: unexpected error: %v", err)
	}

	outer, err := multi.NewChain([]cache.NamedTier{
		{Name: "hot", Cache: hot},
		{Name: "nested", Cache: inner},
	}, true, nil)
	if err != nil {
		t.Fatalf("outer chain: unexpected error: %v", err)
	}

	return outer, hot, inner1, inner2
}

// TestNesting covers the axis that falls out of the parser rather than being a
// capability anyone asked for. It is tested because it is reachable from
// config, not because it is recommended.
func TestNesting(t *testing.T) {
	ctx := context.Background()

	t.Run("names are path-qualified", func(t *testing.T) {
		outer, _, _, _ := nested(t)

		expected := []string{"hot", "nested", "nested/inner1", "nested/inner2"}
		if names := multi.TierNames(outer); !reflect.DeepEqual(names, expected) {
			t.Errorf("got %v, expected %v", names, expected)
		}
	})

	t.Run("the last tier recurses", func(t *testing.T) {
		outer, _, _, _ := nested(t)

		name, ok := multi.LastTierName(outer)
		if !ok || name != "nested/inner2" {
			t.Errorf("got (%q, %v), expected (nested/inner2, true)", name, ok)
		}
	})

	t.Run("a path-qualified write tier reaches into the nested chain", func(t *testing.T) {
		outer, hot, inner1, inner2 := nested(t)

		wctx := cache.WithWriteTiers(ctx, []string{"nested/inner2"})
		if err := outer.Set(wctx, key, []byte("tile")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := hot.Value(key); ok {
			t.Error("the hot tier was written")
		}
		if _, ok := inner1.Value(key); ok {
			t.Error("the wrong nested tier was written")
		}
		if _, ok := inner2.Value(key); !ok {
			t.Error("the named nested tier was not written")
		}
	})

	t.Run("naming a chain outright writes all of it", func(t *testing.T) {
		outer, hot, inner1, inner2 := nested(t)

		wctx := cache.WithWriteTiers(ctx, []string{"nested"})
		if err := outer.Set(wctx, key, []byte("tile")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := hot.Value(key); ok {
			t.Error("the hot tier was written")
		}
		if _, ok := inner1.Value(key); !ok {
			t.Error("inner1 was not written")
		}
		if _, ok := inner2.Value(key); !ok {
			t.Error("inner2 was not written")
		}
	})

	t.Run("a hit in the nested chain promotes all the way up", func(t *testing.T) {
		outer, hot, _, inner2 := nested(t)
		inner2.Seed(key, []byte("tile"))

		if _, hit, err := outer.Get(ctx, key); !hit || err != nil {
			t.Fatalf("got (%v, %v), expected (true, nil)", hit, err)
		}
		if _, ok := hot.Value(key); !ok {
			t.Error("the outer chain did not promote a nested hit")
		}
	})
}

// TestWithTiers pins the idempotence instrumentation depends on.
func TestWithTiers(t *testing.T) {
	c, _, _, _ := chain(t, true)

	tiers := c.Tiers()
	if len(tiers) != 2 || tiers[0].Name != "hot" || tiers[1].Name != "durable" {
		t.Fatalf("Tiers: got %v", tiers)
	}

	// stand-ins for observability wrappers
	wrapped := []cache.Interface{faketier.New("w0"), faketier.New("w1")}
	rebuilt := c.WithTiers(wrapped)

	if rc, ok := rebuilt.(*multi.Cache); !ok || rc == c {
		t.Fatal("WithTiers mutated in place, so a second SetObservability would wrap what the first installed")
	}

	// The original tiers survive on the new chain, so re-deriving a second time
	// yields the same thing rather than a second layer.
	again := rebuilt.(cache.Tiered).Tiers()
	for i := range again {
		if again[i].Cache != tiers[i].Cache {
			t.Errorf("tier %d: WithTiers changed the original tiers", i)
		}
		if again[i].Name != tiers[i].Name {
			t.Errorf("tier %d: name changed from %q to %q", i, tiers[i].Name, again[i].Name)
		}
	}
}

// TestTopLevelTimeoutBoundsTheChain — timeout_ms is not a chain key. Read by
// cache.For, it applies to the chain itself as a whole-chain read budget.
//
// Note what the outcome is, because it is not what a per-tier deadline
// produces: a miss, with no error. The deadline decorator sits *outside* the
// chain, and the chain's contract is that Get never returns an error — so the
// budget expiring bounds the duration and degrades to a miss.
//
// That is the behaviour we want, not a gap. An error here would reach the tile
// middleware, which regenerates on both the error and the miss branch but only
// installs its caching tee on the miss branch — so reporting a chain-budget
// expiry as an error would stop the regenerated tile being written back to a
// perfectly healthy tier.
//
// The cost is that a chain-level budget expiry is visible only as an elevated
// miss rate plus the per-tier error counters underneath it. A whole-chain
// read_timeouts counter would need the decorator to distinguish "my deadline
// fired" from "the inner cache simply missed", which it deliberately does not.
func TestTopLevelTimeoutBoundsTheChain(t *testing.T) {
	slowTier.DelayOn(faketier.OpGet, time.Hour)

	c, err := cache.For(multi.CacheType, dict.Dict{
		"timeout_ms": 50,
		"layers": []map[string]interface{}{
			{"type": "fakeslow"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	start := time.Now()
	val, hit, err := c.Get(context.Background(), key)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("got %v, expected nil — the chain degrades a budget expiry to a miss", err)
	}
	if val != nil || hit {
		t.Errorf("got (%v, %v), expected (nil, false)", val, hit)
	}
	if elapsed > time.Second {
		t.Errorf("returned after %v, expected it at ~50ms rather than the tier's own latency", elapsed)
	}

	// The tier did observe the cancellation, so this is a real abort rather
	// than the caller walking away from a still-running read.
	calls := slowTier.Calls()
	if len(calls) == 0 {
		t.Fatal("the tier was never called")
	}
	last := calls[len(calls)-1]
	if !last.Aborted || !errors.Is(last.CtxErr, context.DeadlineExceeded) {
		t.Errorf("tier call: got (aborted %v, ctx err %v), expected (true, context.DeadlineExceeded)", last.Aborted, last.CtxErr)
	}
}

// waitFor polls until cond holds or the budget expires.
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

// TestPromotionIsDetached — promotion is the one write genuinely on the
// critical path, since it happens inside Get with the client still blocked.
// With a pool it must not be waited on.
func TestPromotionIsDetached(t *testing.T) {
	rec := faketier.NewRecorder()
	hot := faketier.NewWithRecorder("hot", rec)
	durable := faketier.NewWithRecorder("durable", rec)
	durable.Seed(key, []byte("tile"))

	gate := faketier.NewGate()
	hot.GateOn(faketier.OpSet, gate)

	pool := cache.NewWritePool(4, 0)
	c, err := multi.NewChain([]cache.NamedTier{
		{Name: "hot", Cache: hot},
		{Name: "durable", Cache: durable},
	}, true, pool)
	if err != nil {
		t.Fatalf("NewChain: unexpected error: %v", err)
	}

	// Gated, so a synchronous promotion would hang here.
	val, hit, err := c.Get(context.Background(), key)
	if err != nil || !hit || string(val) != "tile" {
		t.Fatalf("got (%q, %v, %v), expected (tile, true, nil)", val, hit, err)
	}

	gate.WaitEntered(1)
	if _, ok := hot.Value(key); ok {
		t.Fatal("Get waited for the promotion")
	}

	gate.Release()
	if !waitFor(t, time.Second, func() bool { _, ok := hot.Value(key); return ok }) {
		t.Error("the detached promotion never landed")
	}
	if stats := c.ChainStats(); stats.Promotions != 1 {
		t.Errorf("promotions: got %d, expected 1", stats.Promotions)
	}
}

// TestPromotionIsDroppable — re-promotion is best-effort by design, and the
// drop has to be countable or the hot tier silently stops filling.
func TestPromotionIsDroppable(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	blocker := faketier.New("blocker")
	blocker.GateOn(faketier.OpSet, gate)

	pool := cache.NewWritePool(1, 0)

	// Occupy the only slot with something unrelated.
	pool.Go(context.Background(), func(ctx context.Context) error {
		return blocker.Set(ctx, key, []byte("tile"))
	})
	gate.WaitEntered(1)

	hot := faketier.New("hot")
	durable := faketier.New("durable")
	durable.Seed(key, []byte("tile"))

	c, err := multi.NewChain([]cache.NamedTier{
		{Name: "hot", Cache: hot},
		{Name: "durable", Cache: durable},
	}, true, pool)
	if err != nil {
		t.Fatalf("NewChain: unexpected error: %v", err)
	}

	if _, hit, err := c.Get(context.Background(), key); !hit || err != nil {
		t.Fatalf("got (%v, %v), expected (true, nil) — a dropped promotion must not fail the read", hit, err)
	}

	if stats := c.ChainStats(); stats.PromotionsDropped != 1 {
		t.Errorf("promotions dropped: got %d, expected 1", stats.PromotionsDropped)
	}
	if _, ok := hot.Value(key); ok {
		t.Error("the promotion ran despite the pool being full")
	}
}

// TestChainSetConsumesOneSlot — the chain does not detach again. It is already
// off the request path, and detaching per tier would consume a slot per tier
// for one logical write and destroy the joined error the seed path depends on.
func TestChainSetConsumesOneSlot(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	gatedHotTier.GateOn(faketier.OpSet, gate)
	gatedDurableTier.GateOn(faketier.OpSet, gate)

	c, err := cache.For(multi.CacheType, dict.Dict{
		"layers": []map[string]interface{}{
			{"type": "fakegatedhot"},
			{"type": "fakegateddurable"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pool := c.(cache.WritePoolHolder).WritePool()

	if err := c.Set(context.Background(), key, []byte("tile")); err != nil {
		t.Fatalf("set: unexpected error: %v", err)
	}

	// Both tier writes are running…
	gate.WaitEntered(2)
	// …on one slot.
	if stats := pool.Stats(); stats.InFlight != 1 {
		t.Errorf("in flight: got %d, expected 1 slot for the whole fan-out", stats.InFlight)
	}
}

// TestNestedChainSharesTheParentPool — one pool per constructed tree, not per
// chain. Built through cache.For, because that is the only path that creates a
// pool at all.
func TestNestedChainSharesTheParentPool(t *testing.T) {
	c, err := cache.For(multi.CacheType, dict.Dict{
		"layers": []map[string]interface{}{
			{"type": "fakehot"},
			{"type": "multi", "name": "nested", "layers": []map[string]interface{}{
				{"type": "fakedurable"},
				{"type": "fakeslow"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	holder, ok := c.(cache.WritePoolHolder)
	if !ok {
		t.Fatal("the chain is not a WritePoolHolder, so atlas cannot publish pool stats")
	}
	pool := holder.WritePool()
	if pool == nil {
		t.Fatal("no pool was created")
	}

	outer, ok := c.(cache.Tiered)
	if !ok {
		t.Fatal("the decorated chain is not cache.Tiered")
	}

	tiers := outer.Tiers()
	if len(tiers) != 2 {
		t.Fatalf("tiers: got %d, expected 2", len(tiers))
	}

	inner, ok := tiers[1].Cache.(*multi.Cache)
	if !ok {
		t.Fatalf("tier 1: got %T, expected the nested chain", tiers[1].Cache)
	}
	if inner.WritePoolForTest() != pool {
		t.Error("the nested chain did not get the parent's pool, so its promotions run inline")
	}
}
