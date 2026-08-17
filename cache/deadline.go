package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MapColonies/shigola/dict"
)

// ConfigKeyTimeoutMs is the optional per-cache read deadline, in integer
// milliseconds. It is read by For and ForTier rather than by any backend, so it
// works on a plain single [cache] table, on every tier of a chain, and at every
// nesting depth.
//
// It carries its unit where the adjacent ttl does not. That is deliberate: ttl
// is bare seconds, and a bare timeout sitting next to it would be read as
// seconds by anyone skimming. The whole plausible range for a read deadline is
// milliseconds.
const ConfigKeyTimeoutMs = "timeout_ms"

// ErrTierTimeout reports that a read was abandoned because the deadline the
// cache derived from timeout_ms expired.
//
// It is returned *only* for a deadline this package derived. A parent context
// that was already done yields the parent's error unwrapped, because that is a
// client disconnect rather than a tier fault, and the two must not be counted
// together.
type ErrTierTimeout struct {
	Timeout time.Duration
}

func (e ErrTierTimeout) Error() string {
	return fmt.Sprintf("cache: read exceeded its %v timeout_ms deadline", e.Timeout)
}

// Unwrap reports context.DeadlineExceeded, so a caller that only wants to know
// "did this time out" need not know about this type. Callers that need to
// distinguish a tier fault from a client disconnect use errors.As instead —
// a client disconnect never produces this type in the first place.
func (e ErrTierTimeout) Unwrap() error { return context.DeadlineExceeded }

// ErrInvalidTimeout is returned at construction for a timeout_ms that is
// present but not a positive integer. Absent means "no deadline"; zero would be
// an ambiguous way to spell the same thing, so it is rejected rather than
// silently accepted as one or the other.
type ErrInvalidTimeout struct {
	Value int
}

func (e ErrInvalidTimeout) Error() string {
	return fmt.Sprintf("cache: %v must be a positive integer, got (%v)", ConfigKeyTimeoutMs, e.Value)
}

// timeoutFor reads ConfigKeyTimeoutMs out of a cache's config.
func timeoutFor(config dict.Dicter) (time.Duration, error) {
	if config == nil {
		return 0, nil
	}

	// Absent is the common case and means no deadline, so look before asking
	// for an int — Int on a missing key with no default is an error.
	v, ok := config.Interface(ConfigKeyTimeoutMs)
	if !ok || v == nil {
		return 0, nil
	}

	ms, err := config.Int(ConfigKeyTimeoutMs, nil)
	if err != nil {
		return 0, err
	}
	if ms <= 0 {
		return 0, ErrInvalidTimeout{Value: ms}
	}

	return time.Duration(ms) * time.Millisecond, nil
}

// deadlineCache bounds Get with a read deadline.
//
// Get only. Writes are off the response path, and bounding them is the write
// pool's job — on s3 a Set deadline would in any case be inert, since PutObject
// attaches no context.
//
// The mechanism is context.WithTimeout rather than a select over time.After.
// The latter stops the *wait* but not the *operation*: the losing branch keeps
// running, leaking a goroutine, a connection and a buffer. A context deadline
// returns at the deadline *and* tears the request down.
type deadlineCache struct {
	cache   Interface
	timeout time.Duration
}

// tieredDeadlineCache is deadlineCache over a composite cache.
//
// Two types rather than one because interface satisfaction is static: a single
// type carrying Tiers() would make *every* decorated cache satisfy Tiered and
// lie about it, which is a trap for the next thing that asserts the interface.
// newDeadlineCache picks the right one.
type tieredDeadlineCache struct {
	deadlineCache
}

var (
	_ Interface = (*deadlineCache)(nil)
	_ Interface = (*tieredDeadlineCache)(nil)
	_ Tiered    = (*tieredDeadlineCache)(nil)
)

// newDeadlineCache wraps c so its Get is bounded by timeout. If c is composite,
// so is the result — atlas has to be able to descend past this decorator to
// reach the tiers, and a decorator that swallowed cache.Tiered would disable
// every per-tier metric with no error and no log line.
//
// Neither form implements cache.Wrapped, so SetObservability cannot strip the
// deadline off on a second call.
func newDeadlineCache(c Interface, timeout time.Duration) Interface {
	d := deadlineCache{cache: c, timeout: timeout}

	if _, ok := c.(Tiered); ok {
		return &tieredDeadlineCache{deadlineCache: d}
	}

	return &d
}

func (d *deadlineCache) Get(ctx context.Context, key *Key) ([]byte, bool, error) {
	// A parent that is already done is a client disconnect. Report it as such
	// rather than deriving a deadline that is dead on arrival.
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	dctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	val, hit, err := d.cache.Get(dctx, key)
	if err == nil {
		return val, hit, nil
	}

	// Attribute the failure. Ours only if the derived deadline is what fired
	// and the parent is still live.
	if ctx.Err() == nil && errors.Is(dctx.Err(), context.DeadlineExceeded) {
		return nil, false, ErrTierTimeout{Timeout: d.timeout}
	}

	return val, hit, err
}

func (d *deadlineCache) Set(ctx context.Context, key *Key, val []byte) error {
	return d.cache.Set(ctx, key, val)
}

func (d *deadlineCache) Purge(ctx context.Context, key *Key) error {
	return d.cache.Purge(ctx, key)
}

func (d *tieredDeadlineCache) Tiers() []NamedTier {
	return d.cache.(Tiered).Tiers()
}

func (d *tieredDeadlineCache) WithTiers(tiers []Interface) Interface {
	// Re-wrap, so a second SetObservability cannot strip the deadline off by
	// rebuilding the chain through here.
	return newDeadlineCache(d.cache.(Tiered).WithTiers(tiers), d.timeout)
}
