// Package faketier provides a programmable cache.Interface double.
//
// It exists because no fake implementing cache.Interface exists anywhere in the
// repo — every backend tests against a real service behind a RUN_*_TESTS gate —
// and the layered cache needs one that can inject errors, inject latency, block
// until a test releases it, and report the order in which it was called.
//
// The last capability is the load-bearing one: a Tier records whether the
// context it was handed was actually cancelled. Without that a deadline test
// cannot tell a real abort from the caller merely walking away, which is exactly
// the distinction that ruled out select-over-time.After semantics.
//
// It lives under internal/ so the cache, cache/multi, atlas and server tests
// can all import it without it becoming public API. Not cache/internal, which
// only packages under cache/ may import — that would put it out of reach of
// exactly the atlas and server tests it was written for.
package faketier

import (
	"context"
	"sync"
	"time"

	"github.com/MapColonies/shigola/cache"
)

// Op names a cache.Interface method.
type Op string

const (
	OpGet   Op = "get"
	OpSet   Op = "set"
	OpPurge Op = "purge"
)

// Call is one observed invocation. Recorded when the call finishes, so the
// recorder's order is completion order, not entry order.
type Call struct {
	Tier string
	Op   Op
	Key  string

	// Aborted reports that the call returned early because the context it was
	// given became done while it was blocked on a gate or on injected latency.
	Aborted bool
	// CtxErr is the context's error as observed by the tier at the moment the
	// call finished. nil means the tier ran to completion with a live context —
	// which is what a detached write must show after its parent was cancelled.
	CtxErr error
	// CtxCause is context.Cause for the same moment.
	CtxCause error
}

// Recorder collects calls in completion order. Share one between tiers to
// assert cross-tier ordering — that Purge runs durable-first, or that seed's
// writes land before its purges.
type Recorder struct {
	mu    sync.Mutex
	calls []Call
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

func (r *Recorder) record(c Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

// Calls returns a copy of everything recorded so far.
func (r *Recorder) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Call, len(r.calls))
	copy(out, r.calls)
	return out
}

// Trace renders the recorded calls as "tier:op" strings, which is usually the
// whole assertion.
func (r *Recorder) Trace() []string {
	calls := r.Calls()

	out := make([]string, len(calls))
	for i := range calls {
		out[i] = calls[i].Tier + ":" + string(calls[i].Op)
	}
	return out
}

// Reset discards everything recorded so far.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}

// Gate blocks every call that carries it until the test releases it.
type Gate struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

// NewGate returns a Gate that is closed — calls carrying it block.
func NewGate() *Gate {
	return &Gate{
		release: make(chan struct{}),
		// buffered deep enough that a test can exhaust a write pool without the
		// entry notification itself becoming a rendezvous.
		entered: make(chan struct{}, 4096),
	}
}

// Release unblocks every call waiting on the gate, and every later one. Safe to
// call more than once.
func (g *Gate) Release() { g.once.Do(func() { close(g.release) }) }

// WaitEntered blocks until n calls have reached the gate. Use it instead of a
// sleep when a test needs to know a write is genuinely in flight.
func (g *Gate) WaitEntered(n int) {
	for i := 0; i < n; i++ {
		<-g.entered
	}
}

// behaviour is what a Tier does for one Op.
type behaviour struct {
	err     error
	latency time.Duration
	gate    *Gate
}

// Tier is a programmable cache.Interface. The zero value is not usable; call New.
type Tier struct {
	name     string
	recorder *Recorder

	mu    sync.Mutex
	store map[string][]byte
	ops   map[Op]behaviour
}

var _ cache.Interface = (*Tier)(nil)

// New returns a Tier with its own Recorder.
func New(name string) *Tier { return NewWithRecorder(name, NewRecorder()) }

// NewWithRecorder returns a Tier that records into rec, so several tiers can
// share one ordering.
func NewWithRecorder(name string, rec *Recorder) *Tier {
	return &Tier{
		name:     name,
		recorder: rec,
		store:    map[string][]byte{},
		ops:      map[Op]behaviour{},
	}
}

// Name returns the tier's name, which is what appears in recorded calls.
func (t *Tier) Name() string { return t.name }

// Recorder returns the recorder this tier writes to.
func (t *Tier) Recorder() *Recorder { return t.recorder }

// FailOn makes op return err. A nil err clears the injected failure.
func (t *Tier) FailOn(op Op, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	b := t.ops[op]
	b.err = err
	t.ops[op] = b
}

// DelayOn makes op take d before returning. The delay is interruptible: a
// context that becomes done during it aborts the call.
func (t *Tier) DelayOn(op Op, d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	b := t.ops[op]
	b.latency = d
	t.ops[op] = b
}

// GateOn makes op block on g until the test releases it. A nil g clears the gate.
func (t *Tier) GateOn(op Op, g *Gate) {
	t.mu.Lock()
	defer t.mu.Unlock()

	b := t.ops[op]
	b.gate = g
	t.ops[op] = b
}

// Seed puts a value in the tier without recording a call, so a test can set up
// a hit without polluting the trace.
func (t *Tier) Seed(key *cache.Key, val []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.store[key.String()] = val
}

// Value reads a value without recording a call.
func (t *Tier) Value(key *cache.Key) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	v, ok := t.store[key.String()]
	return v, ok
}

// Count returns how many times op was called on this tier.
func (t *Tier) Count(op Op) int {
	var n int
	for _, c := range t.recorder.Calls() {
		if c.Tier == t.name && c.Op == op {
			n++
		}
	}
	return n
}

// Calls returns the recorded calls belonging to this tier.
func (t *Tier) Calls() []Call {
	var out []Call
	for _, c := range t.recorder.Calls() {
		if c.Tier == t.name {
			out = append(out, c)
		}
	}
	return out
}

func (t *Tier) behaviourFor(op Op) behaviour {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ops[op]
}

// enter runs the blocking part of an operation and records the call. It returns
// the injected error, or the context's error if the context finished first.
func (t *Tier) enter(ctx context.Context, op Op, key *cache.Key) error {
	b := t.behaviourFor(op)

	call := Call{Tier: t.name, Op: op, Key: key.String()}

	abort := func() error {
		call.Aborted = true
		call.CtxErr = ctx.Err()
		call.CtxCause = context.Cause(ctx)
		t.recorder.record(call)
		return call.CtxErr
	}

	if b.gate != nil {
		select {
		case b.gate.entered <- struct{}{}:
		default: // the notification buffer is full; the test is not counting entries
		}

		select {
		case <-b.gate.release:
		case <-ctx.Done():
			return abort()
		}
	}

	if b.latency > 0 {
		timer := time.NewTimer(b.latency)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			return abort()
		}
	}

	call.CtxErr = ctx.Err()
	call.CtxCause = context.Cause(ctx)
	t.recorder.record(call)

	return b.err
}

func (t *Tier) Get(ctx context.Context, key *cache.Key) ([]byte, bool, error) {
	if err := t.enter(ctx, OpGet, key); err != nil {
		return nil, false, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	val, ok := t.store[key.String()]
	if !ok {
		return nil, false, nil
	}
	return val, true, nil
}

func (t *Tier) Set(ctx context.Context, key *cache.Key, val []byte) error {
	if err := t.enter(ctx, OpSet, key); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// copy: callers hand us a buffer they may reuse
	stored := make([]byte, len(val))
	copy(stored, val)
	t.store[key.String()] = stored

	return nil
}

func (t *Tier) Purge(ctx context.Context, key *cache.Key) error {
	if err := t.enter(ctx, OpPurge, key); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.store, key.String())

	return nil
}
