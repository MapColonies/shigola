package cache_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"
)

// The deadline tests use a real clock with a wide margin — a short deadline
// against a tier that blocks effectively forever — rather than a fake one.
// context.WithTimeout reads the runtime's monotonic clock, so a clock
// abstraction in this package would not intercept it; faking it would deliver
// the illusion of determinism while the real timer still ran.
const (
	testDeadline = 50 * time.Millisecond
	forever      = time.Hour
)

var deadlineKey = &cache.Key{MapName: "osm", Z: 1, X: 2, Y: 3}

// registerFake makes the double reachable through cache.For/ForTier, which is
// the only way to exercise timeout_ms parsing and decorator application. Each
// test registers under its own type name because cache.Register is a
// process-global map with no removal.
func registerFake(t *testing.T, cacheType string, tier *faketier.Tier) {
	t.Helper()

	err := cache.Register(cacheType, func(dict.Dicter) (cache.Interface, error) {
		return tier, nil
	})
	if err != nil {
		t.Fatalf("register %v: unexpected error: %v", cacheType, err)
	}
}

func TestTimeoutConfig(t *testing.T) {
	type tcase struct {
		config      dict.Dict
		expectedErr error
		// deadlineApplied says whether a decorator should have been installed.
		deadlineApplied bool
	}

	fn := func(cacheType string, tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			tier := faketier.New("fake")
			tier.DelayOn(faketier.OpGet, forever)
			registerFake(t, cacheType, tier)

			c, err := cache.For(cacheType, tc.config)
			if tc.expectedErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.expectedErr)
				}
				var invalid cache.ErrInvalidTimeout
				if !errors.As(err, &invalid) {
					t.Fatalf("expected ErrInvalidTimeout, got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// A deadline, if applied, is the only thing that can end this Get.
			ctx, cancel := context.WithTimeout(context.Background(), 6*testDeadline)
			defer cancel()

			start := time.Now()
			_, _, err = c.Get(ctx, deadlineKey)
			elapsed := time.Since(start)

			if !tc.deadlineApplied {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("no timeout_ms: expected the caller's own deadline to end the read, got %v", err)
				}
				var tierTimeout cache.ErrTierTimeout
				if errors.As(err, &tierTimeout) {
					t.Error("no timeout_ms: got ErrTierTimeout, expected the parent's error")
				}
				return
			}

			var tierTimeout cache.ErrTierTimeout
			if !errors.As(err, &tierTimeout) {
				t.Fatalf("expected ErrTierTimeout, got %T: %v", err, err)
			}
			if elapsed > 4*testDeadline {
				t.Errorf("returned after %v, expected it at ~the %v deadline", elapsed, testDeadline)
			}
		}
	}

	tests := map[string]tcase{
		"absent":            {config: dict.Dict{}, deadlineApplied: false},
		"present":           {config: dict.Dict{"timeout_ms": 50}, deadlineApplied: true},
		"zero":              {config: dict.Dict{"timeout_ms": 0}, expectedErr: cache.ErrInvalidTimeout{}},
		"negative":          {config: dict.Dict{"timeout_ms": -1}, expectedErr: cache.ErrInvalidTimeout{}},
		"explicitly absent": {config: dict.Dict{"timeout_ms": nil}, deadlineApplied: false},
	}

	i := 0
	for name, tc := range tests {
		i++
		// cache.Register is a process-global map with no removal, so each case
		// needs its own type name.
		t.Run(name, fn("faketimeoutcfg"+strconv.Itoa(i), tc))
	}
}

func TestTimeoutNonInteger(t *testing.T) {
	tier := faketier.New("fake")
	registerFake(t, "faketimeoutnoninteger", tier)

	_, err := cache.For("faketimeoutnoninteger", dict.Dict{"timeout_ms": "35ms"})
	if err == nil {
		t.Fatal("expected a construction error for a non-integer timeout_ms, got nil")
	}
	// dict reports the type error; the point is that it does not reach the tier.
	if tier.Count(faketier.OpGet) != 0 {
		t.Error("the tier was reached despite a malformed timeout_ms")
	}
}

// TestDeadlineAbortsTheCall is the assertion that would pass vacuously if
// written naively. An implementation that merely stops *waiting* also returns
// at the deadline; only the fake's cancellation observation distinguishes a
// real abort from abandonment.
func TestDeadlineAbortsTheCall(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	tier := faketier.New("hot")
	tier.GateOn(faketier.OpGet, gate)
	registerFake(t, "fakedeadlineabort", tier)

	c, err := cache.For("fakedeadlineabort", dict.Dict{"timeout_ms": 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, _, err = c.Get(context.Background(), deadlineKey); err == nil {
		t.Fatal("expected an error, got nil")
	}

	calls := tier.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls: got %d, expected 1", len(calls))
	}
	if !calls[0].Aborted {
		t.Error("the tier was abandoned, not cancelled — the deadline stopped the wait but not the operation")
	}
	if !errors.Is(calls[0].CtxErr, context.DeadlineExceeded) {
		t.Errorf("tier ctx err: got %v, expected context.DeadlineExceeded", calls[0].CtxErr)
	}
}

// TestParentCancellationIsNotATierFault pins the attribution rule: only a
// deadline this package derived counts as a tier fault. A client disconnect
// must not be counted against the tier.
func TestParentCancellationIsNotATierFault(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	tier := faketier.New("hot")
	tier.GateOn(faketier.OpGet, gate)
	registerFake(t, "fakeparentcancel", tier)

	// A deadline generous enough that it cannot be what fires.
	c, err := cache.For("fakeparentcancel", dict.Dict{"timeout_ms": 60000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, _, err := c.Get(ctx, deadlineKey)
		done <- err
	}()

	gate.WaitEntered(1)
	cancel()

	err = <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, expected context.Canceled", err)
	}

	var tierTimeout cache.ErrTierTimeout
	if errors.As(err, &tierTimeout) {
		t.Error("a client disconnect was reported as ErrTierTimeout")
	}
}

// TestAlreadyDoneParentIsNotATierFault covers the same rule for a parent that
// was done before the call, which never reaches the backend at all.
func TestAlreadyDoneParentIsNotATierFault(t *testing.T) {
	tier := faketier.New("hot")
	registerFake(t, "fakedoneparent", tier)

	c, err := cache.For("fakedoneparent", dict.Dict{"timeout_ms": 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = c.Get(ctx, deadlineKey)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, expected context.Canceled", err)
	}

	var tierTimeout cache.ErrTierTimeout
	if errors.As(err, &tierTimeout) {
		t.Error("an already-cancelled parent was reported as ErrTierTimeout")
	}
	if tier.Count(faketier.OpGet) != 0 {
		t.Error("the tier was called with a context that was already done")
	}
}

// TestDeadlineIsGetOnly — writes are off the response path, and on s3 a write
// deadline would be inert anyway.
func TestDeadlineIsGetOnly(t *testing.T) {
	tier := faketier.New("hot")
	tier.DelayOn(faketier.OpSet, 150*time.Millisecond)
	tier.DelayOn(faketier.OpPurge, 150*time.Millisecond)
	registerFake(t, "fakegetonly", tier)

	c, err := cache.For("fakegetonly", dict.Dict{"timeout_ms": 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := c.Set(context.Background(), deadlineKey, []byte("tile")); err != nil {
		t.Errorf("set: got %v, expected the deadline not to apply", err)
	}
	if err := c.Purge(context.Background(), deadlineKey); err != nil {
		t.Errorf("purge: got %v, expected the deadline not to apply", err)
	}
}

// TestNonTieredCacheDoesNotClaimToBeTiered pins the reason the decorator is two
// types instead of one. A single type carrying Tiers() would make every
// decorated cache satisfy cache.Tiered and report no tiers, which is a trap for
// the next thing that asserts the interface.
func TestNonTieredCacheDoesNotClaimToBeTiered(t *testing.T) {
	tier := faketier.New("hot")
	registerFake(t, "fakenottiered", tier)

	c, err := cache.For("fakenottiered", dict.Dict{"timeout_ms": 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := c.(cache.Tiered); ok {
		t.Error("a decorated non-composite cache satisfies cache.Tiered")
	}
	if _, ok := c.(cache.Wrapped); ok {
		t.Error("the deadline decorator satisfies cache.Wrapped, so SetObservability can strip it")
	}
}
