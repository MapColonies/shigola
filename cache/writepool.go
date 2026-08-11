package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-spatial/tegola/internal/log"
)

// dropWarnInterval rate-limits the saturation warning. A saturated pool drops
// on every write, and a log line per dropped tile is itself an outage.
const dropWarnInterval = 30 * time.Second

// WritePoolStats is a snapshot of a pool, taken without locking so it is safe
// from a metrics scrape.
//
// It exists as a plain struct because observability imports cache, so cache can
// never import observability and nothing here can emit a metric. atlas builds a
// collector over this and registers it through the observer, exactly as it
// already does for per-map collectors.
type WritePoolStats struct {
	// Capacity is the configured number of slots.
	Capacity int
	// InFlight is the number of slots currently held.
	//
	// This is the leading indicator, and the one to alert on. Drops begin only
	// *after* the pool is already exhausted, so the drop counter confirms an
	// incident that in-flight-approaching-capacity would have predicted.
	InFlight int

	// The three loss counters are deliberately separate. They look identical
	// from a distance — all three are "writes we lost" — and they have opposite
	// remedies.

	// Dropped: the pool was full at admission, so nothing was attempted.
	// Raise DetachedWriteSlots, or find why writes are slow.
	Dropped uint64
	// Abandoned: the process was shutting down and the write had not finished
	// when the drain deadline expired. Raise DetachedWriteDrainMs, or fix a
	// slow durable tier.
	Abandoned uint64
	// TimedOut: the write ran past DetachedWriteTimeoutMs and was killed. This
	// is the pre-exhaustion signal — investigate the durable tier. Also counted
	// in Failed, mirroring how a read timeout counts in both _errors_total and
	// _read_timeouts_total.
	TimedOut uint64

	// Failed is attempted-and-errored, including TimedOut.
	Failed uint64
	// Completed is attempted-and-succeeded.
	Completed uint64
	// WriteNanos is the cumulative duration of completed writes. With
	// Completed it gives a truthful mean for every cache, chain or not — which
	// matters because the outermost whole-cache wrapper sits above the
	// detachment decorator, so whole-cache Set latency measures slot
	// acquisition rather than the write.
	//
	// A mean, not a distribution. Read the *tail* from the per-tier histogram
	// tegola_cache_tier_duration_seconds{sub_command="set"} instead: the tail
	// is what holds slots long enough to exhaust the pool, and this hides it.
	WriteNanos uint64
}

// WritePool runs detached cache writes on a bounded set of goroutines.
//
// Acquisition is non-blocking: a write that cannot immediately claim a slot is
// discarded and counted, never queued and never blocked on. Blocking would
// re-couple the write to whichever path called it, which is the exact thing
// detachment exists to prevent. Dropping is safe because a discarded write only
// means the tile is regenerated or re-read from the durable tier later.
//
// It is never a package-level global. Production has exactly one pool because
// the atlas holds exactly one cache — "one shared pool" is satisfied by
// ownership, not by globality — and a global would make pool exhaustion, the
// design's sharpest failure mode, untestable: it could not be reset between
// tests and tests could not run in parallel.
type WritePool struct {
	slots    chan struct{}
	capacity int

	// timeout bounds slot occupancy. Nothing else bounds an s3 write: the SDK
	// falls back to http.DefaultClient, which has Timeout: 0 and a transport
	// that bounds dialling and the TLS handshake but sets no overall timeout.
	// A wedged write holds its slot forever, and enough of them over a process
	// lifetime empty the pool permanently — recovery is a restart.
	timeout time.Duration

	// mu guards draining and the wg.Add below it. Held only across a
	// non-blocking channel send, so contention is negligible; it exists because
	// WaitGroup.Add must not race with Wait.
	mu       sync.Mutex
	draining bool
	wg       sync.WaitGroup

	inFlight atomic.Int64

	dropped    atomic.Uint64
	abandoned  atomic.Uint64
	timedOut   atomic.Uint64
	failed     atomic.Uint64
	completed  atomic.Uint64
	writeNanos atomic.Uint64

	warnMu   sync.Mutex
	warnLast time.Time
	warnSkip uint64
}

// NewWritePool returns a pool with the given capacity and per-write bound. A
// non-positive capacity falls back to the default; a non-positive timeout means
// no bound.
//
// The size is a parameter with exactly one production value on purpose: tests
// build a pool of one or two slots so exhaustion is a two-line setup rather
// than a 256-goroutine stress test.
func NewWritePool(capacity int, timeout time.Duration) *WritePool {
	if capacity <= 0 {
		capacity = defaultDetachedWriteSlots
	}

	return &WritePool{
		slots:    make(chan struct{}, capacity),
		capacity: capacity,
		timeout:  timeout,
	}
}

// Go runs write on a pooled goroutine and returns immediately. It reports
// whether a slot was claimed; false means the write was dropped and counted,
// and nothing was attempted.
//
// The context handed to write is derived with context.WithoutCancel, so the
// write survives the request that produced it, plus the pool's own bound if one
// is configured. The pool owns that deadline, which is what lets it tell its
// own timeout from a backend error — without which a bound-kill would be
// indistinguishable from an S3 500, inverting the priority, since a write
// hitting the bound is the specific signal that the durable tier is degrading.
func (p *WritePool) Go(ctx context.Context, write func(context.Context) error) bool {
	if !p.acquire() {
		p.warnSaturated()
		return false
	}

	wctx := context.WithoutCancel(ctx)

	var cancel context.CancelFunc
	if p.timeout > 0 {
		wctx, cancel = context.WithTimeout(wctx, p.timeout)
	}

	go func() {
		defer p.release()
		if cancel != nil {
			defer cancel()
		}

		start := time.Now()
		err := write(wctx)
		elapsed := time.Since(start)

		if err == nil {
			p.completed.Add(1)
			p.writeNanos.Add(uint64(elapsed))
			return
		}

		p.failed.Add(1)
		if p.timeout > 0 && errors.Is(wctx.Err(), context.DeadlineExceeded) {
			p.timedOut.Add(1)
		}
	}()

	return true
}

func (p *WritePool) acquire() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Admission stops first on drain, which is what makes the drain deadline
	// meaningful: writes created during it would otherwise keep extending it.
	if p.draining {
		p.dropped.Add(1)
		return false
	}

	select {
	case p.slots <- struct{}{}:
		p.wg.Add(1)
		p.inFlight.Add(1)
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

func (p *WritePool) release() {
	<-p.slots
	p.inFlight.Add(-1)
	p.wg.Done()
}

// warnSaturated logs at most once per dropWarnInterval, reporting how many
// drops were suppressed since the last line.
//
// Dropping is safe; silence is not. A saturated pool is otherwise
// indistinguishable from a healthy chain — same latencies, same status codes,
// just an unexplained rise in miss rate — and with no observer configured this
// log line is the only signal there is.
func (p *WritePool) warnSaturated() {
	p.warnMu.Lock()
	defer p.warnMu.Unlock()

	now := time.Now()
	if !p.warnLast.IsZero() && now.Sub(p.warnLast) < dropWarnInterval {
		p.warnSkip++
		return
	}

	log.Warnf(
		"cache: detached write pool saturated at %v slots, dropping writes (%v further drops suppressed since the last warning). "+
			"raise TEGOLA_OPTIONS=DetachedWriteSlots, or find why writes are slow",
		p.capacity, p.warnSkip,
	)

	p.warnLast = now
	p.warnSkip = 0
}

// Stats returns a snapshot. Lock-free, so it is safe to call from a scrape.
func (p *WritePool) Stats() WritePoolStats {
	return WritePoolStats{
		Capacity:   p.capacity,
		InFlight:   int(p.inFlight.Load()),
		Dropped:    p.dropped.Load(),
		Abandoned:  p.abandoned.Load(),
		TimedOut:   p.timedOut.Load(),
		Failed:     p.failed.Load(),
		Completed:  p.completed.Load(),
		WriteNanos: p.writeNanos.Load(),
	}
}

// Drain stops admitting writes and waits up to deadline for the in-flight ones.
// Whatever has not finished is abandoned, counted, and the process carries on
// exiting.
//
// Without it every process exit discards up to a poolful of writes. On
// Kubernetes that is every release, every config change, every node drain and
// every scale-down — a recurring, invisible cost whose signature is identical
// to pool exhaustion.
//
// A deadline of zero disables the drain entirely, including the admission stop,
// which is the behaviour before this existed.
//
// This is belt-and-braces for the serve path only. The CLI and Lambda paths set
// WithSynchronousWrites instead, because seed correctness must not depend on a
// clean shutdown.
func (p *WritePool) Drain(deadline time.Duration) {
	if deadline <= 0 {
		return
	}

	p.mu.Lock()
	p.draining = true
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(deadline)
	defer timer.Stop()

	select {
	case <-done:
		log.Infof("cache: detached write pool drained, %v writes completed", p.completed.Load())
	case <-timer.C:
		// Approximate by construction: a write may finish between the deadline
		// firing and this read. The number is a signal, not an accounting.
		remaining := p.inFlight.Load()
		if remaining > 0 {
			p.abandoned.Add(uint64(remaining))
		}
		log.Warnf(
			"cache: detached write pool drain expired after %v with %v writes still in flight, abandoning them. "+
				"raise TEGOLA_OPTIONS=DetachedWriteDrainMs, or fix a slow durable tier",
			deadline, remaining,
		)
	}
}
