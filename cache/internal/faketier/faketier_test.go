package faketier_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/cache/internal/faketier"
)

var testKey = &cache.Key{MapName: "osm", Z: 1, X: 2, Y: 3}

// TestRoundTrip covers the record-and-replay half.
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	tier := faketier.New("hot")

	if _, hit, err := tier.Get(ctx, testKey); err != nil || hit {
		t.Fatalf("get on empty: got (hit %v, err %v), expected (false, nil)", hit, err)
	}

	if err := tier.Set(ctx, testKey, []byte("tile")); err != nil {
		t.Fatalf("set: unexpected error: %v", err)
	}

	val, hit, err := tier.Get(ctx, testKey)
	if err != nil || !hit {
		t.Fatalf("get after set: got (hit %v, err %v), expected (true, nil)", hit, err)
	}
	if string(val) != "tile" {
		t.Errorf("get after set: got %q, expected %q", val, "tile")
	}

	if err := tier.Purge(ctx, testKey); err != nil {
		t.Fatalf("purge: unexpected error: %v", err)
	}
	if _, hit, _ := tier.Get(ctx, testKey); hit {
		t.Error("get after purge: got a hit, expected a miss")
	}

	expected := []string{"hot:get", "hot:set", "hot:get", "hot:purge", "hot:get"}
	if trace := tier.Recorder().Trace(); !reflect.DeepEqual(trace, expected) {
		t.Errorf("trace: got %v, expected %v", trace, expected)
	}
}

// TestSharedRecorderOrdersAcrossTiers is the capability the Purge-ordering and
// seed-ordering assertions rest on.
func TestSharedRecorderOrdersAcrossTiers(t *testing.T) {
	ctx := context.Background()
	rec := faketier.NewRecorder()
	hot := faketier.NewWithRecorder("hot", rec)
	durable := faketier.NewWithRecorder("durable", rec)

	if err := durable.Purge(ctx, testKey); err != nil {
		t.Fatalf("durable purge: unexpected error: %v", err)
	}
	if err := hot.Purge(ctx, testKey); err != nil {
		t.Fatalf("hot purge: unexpected error: %v", err)
	}

	expected := []string{"durable:purge", "hot:purge"}
	if trace := rec.Trace(); !reflect.DeepEqual(trace, expected) {
		t.Errorf("trace: got %v, expected %v", trace, expected)
	}
}

func TestInjectedError(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("faketier: boom")

	tier := faketier.New("hot")
	tier.FailOn(faketier.OpGet, boom)

	if _, _, err := tier.Get(ctx, testKey); !errors.Is(err, boom) {
		t.Errorf("get: got %v, expected %v", err, boom)
	}
	// only the programmed op fails
	if err := tier.Set(ctx, testKey, []byte("tile")); err != nil {
		t.Errorf("set: unexpected error: %v", err)
	}
}

// TestGateObservesCancellation is the load-bearing one: a caller that walks
// away must be distinguishable from a call that was genuinely cancelled.
func TestGateObservesCancellation(t *testing.T) {
	gate := faketier.NewGate()
	tier := faketier.New("hot")
	tier.GateOn(faketier.OpGet, gate)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, _, err := tier.Get(ctx, testKey)
		done <- err
	}()

	gate.WaitEntered(1)
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("get: got %v, expected context.Canceled", err)
	}

	calls := tier.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls: got %d, expected 1", len(calls))
	}
	if !calls[0].Aborted {
		t.Error("call: not marked aborted, but the context was cancelled under it")
	}
	if !errors.Is(calls[0].CtxErr, context.Canceled) {
		t.Errorf("call ctx err: got %v, expected context.Canceled", calls[0].CtxErr)
	}
}

// TestGateReleaseRunsToCompletion is the other half: a released gate must leave
// a call that observed a live context, which is how "the detached write
// survived its parent's cancellation" is asserted.
func TestGateReleaseRunsToCompletion(t *testing.T) {
	gate := faketier.NewGate()
	tier := faketier.New("durable")
	tier.GateOn(faketier.OpSet, gate)

	done := make(chan error, 1)
	go func() { done <- tier.Set(context.Background(), testKey, []byte("tile")) }()

	gate.WaitEntered(1)
	gate.Release()

	if err := <-done; err != nil {
		t.Fatalf("set: unexpected error: %v", err)
	}

	calls := tier.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls: got %d, expected 1", len(calls))
	}
	if calls[0].Aborted {
		t.Error("call: marked aborted, but the gate was released with a live context")
	}
	if calls[0].CtxErr != nil {
		t.Errorf("call ctx err: got %v, expected nil", calls[0].CtxErr)
	}
	if _, ok := tier.Value(testKey); !ok {
		t.Error("value: the write did not land")
	}
}

func TestLatencyIsInterruptible(t *testing.T) {
	tier := faketier.New("hot")
	tier.DelayOn(faketier.OpGet, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, _, err := tier.Get(ctx, testKey); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("get: got %v, expected context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Minute {
		t.Errorf("get: took %v, expected it to return at the deadline", elapsed)
	}
}

func TestSeedDoesNotRecord(t *testing.T) {
	tier := faketier.New("hot")
	tier.Seed(testKey, []byte("tile"))

	if trace := tier.Recorder().Trace(); len(trace) != 0 {
		t.Errorf("trace: got %v, expected setup not to be recorded", trace)
	}
	if val, ok := tier.Value(testKey); !ok || string(val) != "tile" {
		t.Errorf("value: got (%q, %v), expected (%q, true)", val, ok, "tile")
	}
}
