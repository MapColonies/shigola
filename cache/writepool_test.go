package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/internal/faketier"
)

var poolKey = &cache.Key{MapName: "osm", Z: 4, X: 3, Y: 2}

// waitFor polls until cond holds or the budget expires. Used instead of a sleep
// for the handful of assertions about a goroutine that has already been
// released and only needs to be scheduled.
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

func TestWritePoolStats(t *testing.T) {
	pool := cache.NewWritePool(2, 0)

	if stats := pool.Stats(); stats.Capacity != 2 || stats.InFlight != 0 {
		t.Fatalf("initial stats: got %+v", stats)
	}

	gate := faketier.NewGate()
	tier := faketier.New("durable")
	tier.GateOn(faketier.OpSet, gate)

	if !pool.Go(context.Background(), func(ctx context.Context) error {
		return tier.Set(ctx, poolKey, []byte("tile"))
	}) {
		t.Fatal("the write was dropped by an empty pool")
	}

	gate.WaitEntered(1)
	if stats := pool.Stats(); stats.InFlight != 1 {
		t.Errorf("in flight: got %d, expected 1", stats.InFlight)
	}

	gate.Release()

	if !waitFor(t, time.Second, func() bool { return pool.Stats().Completed == 1 }) {
		t.Fatalf("after release: got %+v, expected Completed 1", pool.Stats())
	}

	stats := pool.Stats()
	if stats.InFlight != 0 {
		t.Errorf("in flight: got %d, expected the slot back", stats.InFlight)
	}
	if stats.Failed != 0 || stats.Dropped != 0 || stats.TimedOut != 0 || stats.Abandoned != 0 {
		t.Errorf("loss counters moved on a clean write: %+v", stats)
	}
	if stats.WriteNanos == 0 {
		t.Error("write duration was not recorded")
	}
}

// TestWritePoolExhaustion is the failure mode nothing else in the suite covers.
func TestWritePoolExhaustion(t *testing.T) {
	t.Run("a full pool drops rather than blocking", func(t *testing.T) {
		gate := faketier.NewGate()
		defer gate.Release()

		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(2, 0)
		write := func(ctx context.Context) error { return tier.Set(ctx, poolKey, []byte("tile")) }

		for i := 0; i < 2; i++ {
			if !pool.Go(context.Background(), write) {
				t.Fatalf("write %d was dropped by a pool with a free slot", i)
			}
		}
		gate.WaitEntered(2)

		// The third has nowhere to go. It must return immediately rather than
		// wait for a slot: blocking would re-couple the write to its caller.
		start := time.Now()
		if pool.Go(context.Background(), write) {
			t.Fatal("a full pool admitted a third write")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("admission took %v — the pool blocked instead of dropping", elapsed)
		}

		if stats := pool.Stats(); stats.Dropped != 1 {
			t.Errorf("dropped: got %d, expected 1", stats.Dropped)
		}
	})

	t.Run("slots are returned when writes complete", func(t *testing.T) {
		gate := faketier.NewGate()
		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(1, 0)
		write := func(ctx context.Context) error { return tier.Set(ctx, poolKey, []byte("tile")) }

		if !pool.Go(context.Background(), write) {
			t.Fatal("the first write was dropped")
		}
		gate.WaitEntered(1)
		if pool.Go(context.Background(), write) {
			t.Fatal("a full pool admitted a second write")
		}

		gate.Release()
		if !waitFor(t, time.Second, func() bool { return pool.Stats().InFlight == 0 }) {
			t.Fatal("the slot was never returned")
		}

		if !pool.Go(context.Background(), write) {
			t.Error("the pool did not accept a write after recovering")
		}
	})

	// The claim the 10s default rests on: with the bound on, an exhausted pool
	// recovers without intervention.
	t.Run("with the write bound on, an exhausted pool recovers unaided", func(t *testing.T) {
		gate := faketier.NewGate()
		defer gate.Release()

		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(1, 100*time.Millisecond)
		write := func(ctx context.Context) error { return tier.Set(ctx, poolKey, []byte("tile")) }

		if !pool.Go(context.Background(), write) {
			t.Fatal("the first write was dropped")
		}
		gate.WaitEntered(1)
		if pool.Go(context.Background(), write) {
			t.Fatal("a full pool admitted a second write")
		}

		// Nothing releases the gate. The bound is the only thing that can free
		// the slot, and it must.
		if !waitFor(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 0 }) {
			t.Fatal("the wedged write held its slot past the bound")
		}

		stats := pool.Stats()
		if stats.TimedOut != 1 {
			t.Errorf("timed out: got %d, expected 1", stats.TimedOut)
		}
		// A bound-kill counts in both, mirroring how a read timeout counts in
		// both _errors_total and _read_timeouts_total.
		if stats.Failed != 1 {
			t.Errorf("failed: got %d, expected 1 — a bound-kill is also a failure", stats.Failed)
		}
		if !pool.Go(context.Background(), write) {
			t.Error("the pool did not recover after the bound fired")
		}
	})

	// The same setup with the bound disabled, pinned as deliberate and
	// reachable rather than as a latent bug.
	t.Run("with the bound disabled, a wedged write holds its slot forever", func(t *testing.T) {
		gate := faketier.NewGate()
		defer gate.Release()

		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(1, 0)
		write := func(ctx context.Context) error { return tier.Set(ctx, poolKey, []byte("tile")) }

		if !pool.Go(context.Background(), write) {
			t.Fatal("the first write was dropped")
		}
		gate.WaitEntered(1)

		// Generous relative to the bound the previous case used.
		time.Sleep(300 * time.Millisecond)

		if stats := pool.Stats(); stats.InFlight != 1 {
			t.Errorf("in flight: got %d, expected the slot to still be held", stats.InFlight)
		}
		if pool.Go(context.Background(), write) {
			t.Error("the pool recovered with DetachedWriteTimeoutMs=0, which it cannot do")
		}
	})
}

// TestWritePoolFailureAttribution — the three loss counters have opposite
// remedies, so they must be distinguishable.
func TestWritePoolFailureAttribution(t *testing.T) {
	t.Run("a backend error is Failed but not TimedOut", func(t *testing.T) {
		boom := errors.New("s3: 500")

		pool := cache.NewWritePool(1, time.Minute)
		pool.Go(context.Background(), func(context.Context) error { return boom })

		if !waitFor(t, time.Second, func() bool { return pool.Stats().Failed == 1 }) {
			t.Fatalf("got %+v, expected Failed 1", pool.Stats())
		}
		if stats := pool.Stats(); stats.TimedOut != 0 {
			t.Errorf("timed out: got %d, expected 0 — a backend error is not a bound-kill", stats.TimedOut)
		}
	})

	t.Run("the drain abandons rather than drops", func(t *testing.T) {
		gate := faketier.NewGate()
		defer gate.Release()

		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(4, 0)
		if !pool.Go(context.Background(), func(ctx context.Context) error {
			return tier.Set(ctx, poolKey, []byte("tile"))
		}) {
			t.Fatal("the write was dropped")
		}
		gate.WaitEntered(1)

		pool.Drain(100 * time.Millisecond)

		stats := pool.Stats()
		if stats.Abandoned != 1 {
			t.Errorf("abandoned: got %d, expected 1", stats.Abandoned)
		}
		// Distinguishable is the whole point: a pool that is too small wants
		// more slots, a drain that expires wants a longer budget.
		if stats.Dropped != 0 {
			t.Errorf("dropped: got %d, expected 0 — abandonment is not saturation", stats.Dropped)
		}
	})
}

func TestWritePoolDrain(t *testing.T) {
	t.Run("waits for in-flight writes, which then land", func(t *testing.T) {
		gate := faketier.NewGate()
		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(4, 0)
		pool.Go(context.Background(), func(ctx context.Context) error {
			return tier.Set(ctx, poolKey, []byte("tile"))
		})
		gate.WaitEntered(1)

		// Release shortly after the drain starts waiting.
		go func() {
			time.Sleep(20 * time.Millisecond)
			gate.Release()
		}()

		pool.Drain(5 * time.Second)

		if _, ok := tier.Value(poolKey); !ok {
			t.Error("the drain returned before the write landed")
		}
		if stats := pool.Stats(); stats.Abandoned != 0 || stats.Completed != 1 {
			t.Errorf("got %+v, expected Completed 1 and Abandoned 0", stats)
		}
	})

	t.Run("returns at the deadline on a wedged write", func(t *testing.T) {
		gate := faketier.NewGate()
		defer gate.Release()

		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(4, 0)
		pool.Go(context.Background(), func(ctx context.Context) error {
			return tier.Set(ctx, poolKey, []byte("tile"))
		})
		gate.WaitEntered(1)

		start := time.Now()
		pool.Drain(100 * time.Millisecond)
		elapsed := time.Since(start)

		if elapsed > 3*time.Second {
			t.Errorf("the drain took %v — a wedged write hung shutdown", elapsed)
		}
		if stats := pool.Stats(); stats.Abandoned != 1 {
			t.Errorf("abandoned: got %d, expected 1", stats.Abandoned)
		}
	})

	// Stopping admission first is what makes the deadline meaningful: writes
	// created during the drain would otherwise keep extending it.
	t.Run("admission stops before the wait", func(t *testing.T) {
		pool := cache.NewWritePool(4, 0)
		pool.Drain(50 * time.Millisecond)

		if pool.Go(context.Background(), func(context.Context) error { return nil }) {
			t.Error("the pool admitted a write after draining")
		}
		if stats := pool.Stats(); stats.Dropped != 1 {
			t.Errorf("dropped: got %d, expected 1", stats.Dropped)
		}
	})

	t.Run("a zero deadline disables the drain entirely", func(t *testing.T) {
		gate := faketier.NewGate()
		defer gate.Release()

		tier := faketier.New("durable")
		tier.GateOn(faketier.OpSet, gate)

		pool := cache.NewWritePool(4, 0)
		pool.Go(context.Background(), func(ctx context.Context) error {
			return tier.Set(ctx, poolKey, []byte("tile"))
		})
		gate.WaitEntered(1)

		start := time.Now()
		pool.Drain(0)

		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("Drain(0) waited %v, expected it to be a no-op", elapsed)
		}
		if stats := pool.Stats(); stats.Abandoned != 0 {
			t.Errorf("abandoned: got %d, expected 0", stats.Abandoned)
		}
		// Admission is untouched too — this is the behaviour from before the
		// drain existed.
		if !pool.Go(context.Background(), func(context.Context) error { return nil }) {
			t.Error("Drain(0) stopped admission")
		}
	})
}
