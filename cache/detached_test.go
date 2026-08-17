package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"
)

var detachedKey = &cache.Key{MapName: "osm", Z: 5, X: 4, Y: 3}

// TestSetReturnsBeforeTheWriteCompletes is the guarantee the whole write model
// exists for. It would pass vacuously against a fast in-memory tier, so it uses
// the gate: the write is *observably* still running when Set returns.
func TestSetReturnsBeforeTheWriteCompletes(t *testing.T) {
	gate := faketier.NewGate()
	tier := faketier.New("durable")
	tier.GateOn(faketier.OpSet, gate)

	pool := cache.NewWritePool(4, 0)
	c := cache.NewDetachedCache(tier, pool)

	if err := c.Set(context.Background(), detachedKey, []byte("tile")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Still gated, so the write cannot have finished — yet Set is back.
	gate.WaitEntered(1)
	if _, ok := tier.Value(detachedKey); ok {
		t.Fatal("the write completed before Set returned, so it was not detached")
	}

	gate.Release()
	if !waitFor(t, time.Second, func() bool { _, ok := tier.Value(detachedKey); return ok }) {
		t.Error("the detached write never landed")
	}
}

// TestDetachedWriteSurvivesParentCancellation — the write is derived with
// context.WithoutCancel, so the request finishing does not kill it. Without
// that, every detached write on the serve path would die the moment its handler
// returned.
func TestDetachedWriteSurvivesParentCancellation(t *testing.T) {
	gate := faketier.NewGate()
	tier := faketier.New("durable")
	tier.GateOn(faketier.OpSet, gate)

	pool := cache.NewWritePool(4, 0)
	c := cache.NewDetachedCache(tier, pool)

	ctx, cancel := context.WithCancel(context.Background())

	if err := c.Set(ctx, detachedKey, []byte("tile")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gate.WaitEntered(1)

	// The request goes away while the write is in flight.
	cancel()
	gate.Release()

	if !waitFor(t, time.Second, func() bool { return pool.Stats().Completed == 1 }) {
		t.Fatalf("got %+v, expected the write to complete", pool.Stats())
	}

	calls := tier.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls: got %d, expected 1", len(calls))
	}
	// Observed, not inferred: the tier saw a live context throughout.
	if calls[0].Aborted || calls[0].CtxErr != nil {
		t.Errorf("the write saw its parent's cancellation: aborted %v, ctx err %v", calls[0].Aborted, calls[0].CtxErr)
	}
}

func TestSynchronousWrites(t *testing.T) {
	boom := errors.New("durable tier rejected the write")

	t.Run("the write completes before Set returns", func(t *testing.T) {
		tier := faketier.New("durable")
		c := cache.NewDetachedCache(tier, cache.NewWritePool(4, 0))

		ctx := cache.WithSynchronousWrites(context.Background())
		if err := c.Set(ctx, detachedKey, []byte("tile")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := tier.Value(detachedKey); !ok {
			t.Error("Set returned before the write landed")
		}
	})

	// The error path strict Set exists for. Detached, a failure is logged and
	// counted long after Set returned; inline, it reaches the seed worker.
	t.Run("errors reach the caller", func(t *testing.T) {
		tier := faketier.New("durable")
		tier.FailOn(faketier.OpSet, boom)

		c := cache.NewDetachedCache(tier, cache.NewWritePool(4, 0))

		ctx := cache.WithSynchronousWrites(context.Background())
		if err := c.Set(ctx, detachedKey, []byte("tile")); !errors.Is(err, boom) {
			t.Errorf("got %v, expected %v", err, boom)
		}
	})

	t.Run("a detached failure does not reach the caller", func(t *testing.T) {
		tier := faketier.New("durable")
		tier.FailOn(faketier.OpSet, boom)

		pool := cache.NewWritePool(4, 0)
		c := cache.NewDetachedCache(tier, pool)

		if err := c.Set(context.Background(), detachedKey, []byte("tile")); err != nil {
			t.Errorf("got %v, expected nil — the write fails long after Set returned", err)
		}
		if !waitFor(t, time.Second, func() bool { return pool.Stats().Failed == 1 }) {
			t.Errorf("got %+v, expected the failure to be counted", pool.Stats())
		}
	})
}

// TestDroppedWriteIsSilentToTheCaller — the third outcome. Nothing was
// attempted, and nil is still returned; the drop counter is the only evidence.
func TestDroppedWriteIsSilentToTheCaller(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	tier := faketier.New("durable")
	tier.GateOn(faketier.OpSet, gate)

	pool := cache.NewWritePool(1, 0)
	c := cache.NewDetachedCache(tier, pool)

	if err := c.Set(context.Background(), detachedKey, []byte("tile")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gate.WaitEntered(1)

	start := time.Now()
	if err := c.Set(context.Background(), detachedKey, []byte("tile")); err != nil {
		t.Fatalf("got %v, expected nil for a dropped write", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Set took %v — a saturated pool blocked the caller", elapsed)
	}

	if stats := pool.Stats(); stats.Dropped != 1 {
		t.Errorf("dropped: got %d, expected 1", stats.Dropped)
	}
}

// TestReadsAndPurgesAreNotDetached — the client is waiting on a read, and purge
// is an operation whose entire purpose is a guarantee of removal.
func TestReadsAndPurgesAreNotDetached(t *testing.T) {
	tier := faketier.New("durable")
	tier.Seed(detachedKey, []byte("tile"))

	c := cache.NewDetachedCache(tier, cache.NewWritePool(4, 0))

	val, hit, err := c.Get(context.Background(), detachedKey)
	if err != nil || !hit || string(val) != "tile" {
		t.Fatalf("get: got (%q, %v, %v), expected (tile, true, nil)", val, hit, err)
	}

	if err := c.Purge(context.Background(), detachedKey); err != nil {
		t.Fatalf("purge: unexpected error: %v", err)
	}
	if _, ok := tier.Value(detachedKey); ok {
		t.Error("purge returned before the tier was purged")
	}

	// A purge failure must reach the caller.
	boom := errors.New("purge failed")
	tier.FailOn(faketier.OpPurge, boom)
	if err := c.Purge(context.Background(), detachedKey); !errors.Is(err, boom) {
		t.Errorf("purge: got %v, expected %v", err, boom)
	}
}

// TestSingleBackendCacheDetaches — the guarantee is not chain-only. A plain
// [cache] type = "redis" gets the same non-blocking writes, which is why the
// decorator lives in cache.For rather than in the chain.
func TestSingleBackendCacheDetaches(t *testing.T) {
	gate := faketier.NewGate()
	tier := faketier.New("solo")
	tier.GateOn(faketier.OpSet, gate)

	registerFake(t, "fakesolodetach", tier)

	c, err := cache.For("fakesolodetach", dict.Dict{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := c.(cache.Tiered); ok {
		t.Error("a single-backend cache satisfies cache.Tiered")
	}
	if _, ok := c.(cache.WritePoolHolder); !ok {
		t.Fatal("a single-backend cache holds no write pool, so atlas cannot publish its stats")
	}

	if err := c.Set(context.Background(), detachedKey, []byte("tile")); err != nil {
		t.Fatalf("set: unexpected error: %v", err)
	}
	gate.WaitEntered(1)
	if _, ok := tier.Value(detachedKey); ok {
		t.Fatal("the write was inline")
	}

	gate.Release()
	if !waitFor(t, time.Second, func() bool { _, ok := tier.Value(detachedKey); return ok }) {
		t.Error("the detached write never landed")
	}
}

// TestForAppliesBothDecorators pins the stack cache.For builds, and that
// neither decorator can be stripped by SetObservability.
func TestForAppliesBothDecorators(t *testing.T) {
	tier := faketier.New("solo")
	registerFake(t, "fakebothdecorators", tier)

	c, err := cache.For("fakebothdecorators", dict.Dict{"timeout_ms": 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := c.(cache.Wrapped); ok {
		t.Error("the decorated cache satisfies cache.Wrapped, so SetObservability can unwrap past the decorators")
	}
	if _, ok := c.(cache.WritePoolHolder); !ok {
		t.Error("the detachment decorator is missing")
	}
}

// TestForTierDoesNotDetach is the asymmetry the whole construction split exists
// for. Detaching per tier would make a composite Set return before any tier
// write, leaving its joined error nothing to join.
func TestForTierDoesNotDetach(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	tier := faketier.New("tier")
	tier.GateOn(faketier.OpGet, gate)
	registerFake(t, "fakefortier", tier)

	c, err := cache.ForTier("fakefortier", dict.Dict{"timeout_ms": 50}, cache.NewWritePool(4, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := c.(cache.WritePoolHolder); ok {
		t.Fatal("ForTier applied the detachment decorator")
	}

	// The deadline is applied, though: it is per-tier by design.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err = c.Get(ctx, detachedKey)
	var tierTimeout cache.ErrTierTimeout
	if !errors.As(err, &tierTimeout) {
		t.Errorf("get: got %T (%v), expected cache.ErrTierTimeout", err, err)
	}
}
