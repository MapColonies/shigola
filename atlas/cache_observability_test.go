package atlas

import (
	"context"
	"errors"
	"testing"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/internal/faketier"
	"github.com/go-spatial/tegola/observability"
	"github.com/go-spatial/tegola/observability/prometheus"
)

// The prometheus observer registers against prometheus.DefaultRegisterer, so
// these tests share one process-wide registry. Metric *names* are therefore
// shared too — assertions read specific label sets rather than whole families,
// and every tier gets a name unique to its test.

var obsKey = &cache.Key{MapName: "osm", Z: 6, X: 5, Y: 4}

func newObserver(t *testing.T) observability.Interface {
	t.Helper()

	o, err := prometheus.New(dict.Dict{})
	if err != nil {
		t.Fatalf("prometheus observer: %v", err)
	}

	return o
}

// tieredCache builds hot → durable through cache.For, with the given tier
// names, so the whole decorator stack is present exactly as in production.
func tieredCache(t *testing.T, hotType, durableType, hotName, durableName string, hot, durable *faketier.Tier, hotTimeoutMs int) cache.Interface {
	t.Helper()

	for cacheType, tier := range map[string]*faketier.Tier{hotType: hot, durableType: durable} {
		c := tier
		if err := cache.Register(cacheType, func(dict.Dicter) (cache.Interface, error) { return c, nil }); err != nil {
			t.Fatalf("register %v: %v", cacheType, err)
		}
	}

	hotLayer := map[string]interface{}{"type": hotType, "name": hotName}
	if hotTimeoutMs > 0 {
		hotLayer["timeout_ms"] = hotTimeoutMs
	}

	c, err := cache.For("multi", dict.Dict{
		"layers": []map[string]interface{}{
			hotLayer,
			{"type": durableType, "name": durableName},
		},
	})
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}

	return c
}

// seenFamily reports whether a metric family is published at all.
func seenFamily(t *testing.T, name string) bool {
	t.Helper()

	families, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		if family.GetName() == name {
			return true
		}
	}

	return false
}

// counter reads one labelled sample out of the default registry.
func counter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()

	families, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, m := range family.GetMetric() {
			matched := true
			for k, v := range labels {
				found := false
				for _, pair := range m.GetLabel() {
					if pair.GetName() == k && pair.GetValue() == v {
						found = true
						break
					}
				}
				if !found {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}

			if c := m.GetCounter(); c != nil {
				return c.GetValue()
			}
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
			if h := m.GetHistogram(); h != nil {
				return float64(h.GetSampleCount())
			}
		}
	}

	return 0
}

// TestBothFamiliesRegisterTogether is the one that would have panicked at
// startup on the first chain deployment with an observer configured.
//
// Registering tegola_cache_hits_total once without a tier label and once with
// one is a label-dimension mismatch, and newCache registers through a path that
// panics rather than returning the error. Separate families are what make it
// possible at all.
func TestBothFamiliesRegisterTogether(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")

	a := &Atlas{}
	a.SetCache(tieredCache(t, "obshot1", "obsdurable1", "hot1", "durable1", hot, durable, 0))

	// Panics here if the families collide.
	a.SetObservability(newObserver(t))

	if _, ok := a.GetCache().(observability.Cache); !ok {
		t.Fatal("the cache was not instrumented")
	}
}

// TestPerTierMetricsAreLabelled — a test that only checked "some counter moved"
// would miss the label being wrong, which is the entire point of the family.
func TestPerTierMetricsAreLabelled(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")
	durable.Seed(obsKey, []byte("tile"))

	a := &Atlas{}
	a.SetCache(tieredCache(t, "obshot2", "obsdurable2", "hot2", "durable2", hot, durable, 0))
	a.SetObservability(newObserver(t))

	before := counter(t, "tegola_cache_tier_hits_total", map[string]string{"tier": "durable2", "sub_command": "get"})
	missBefore := counter(t, "tegola_cache_tier_misses_total", map[string]string{"tier": "hot2", "sub_command": "get"})
	chainBefore := counter(t, "tegola_cache_hits_total", map[string]string{"sub_command": "get"})

	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); !hit || err != nil {
		t.Fatalf("get: got (%v, %v), expected (true, nil)", hit, err)
	}

	if got := counter(t, "tegola_cache_tier_hits_total", map[string]string{"tier": "durable2", "sub_command": "get"}); got != before+1 {
		t.Errorf("durable tier hits: got %v, expected %v", got, before+1)
	}
	if got := counter(t, "tegola_cache_tier_misses_total", map[string]string{"tier": "hot2", "sub_command": "get"}); got != missBefore+1 {
		t.Errorf("hot tier misses: got %v, expected %v", got, missBefore+1)
	}
	// One tile served, two tier lookups. This is why the families must not be
	// summed together.
	if got := counter(t, "tegola_cache_hits_total", map[string]string{"sub_command": "get"}); got != chainBefore+1 {
		t.Errorf("chain hits: got %v, expected %v", got, chainBefore+1)
	}
}

// TestReadTimeoutIsCountedAsBoth mirrors the write path: a bound-kill counts in
// both the general counter and the specific one, so it is distinguishable
// without being lost from the total.
func TestReadTimeoutIsCountedAsBoth(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	hot := faketier.New("hot")
	hot.GateOn(faketier.OpGet, gate)
	durable := faketier.New("durable")

	a := &Atlas{}
	a.SetCache(tieredCache(t, "obshot3", "obsdurable3", "hot3", "durable3", hot, durable, 50))
	a.SetObservability(newObserver(t))

	labels := map[string]string{"tier": "hot3", "sub_command": "get"}
	errsBefore := counter(t, "tegola_cache_tier_errors_total", labels)
	timeoutsBefore := counter(t, "tegola_cache_tier_read_timeouts_total", labels)
	histBefore := counter(t, "tegola_cache_tier_duration_seconds", labels)

	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); hit || err != nil {
		t.Fatalf("get: got (%v, %v), expected (false, nil) — the chain degrades to a miss", hit, err)
	}

	if got := counter(t, "tegola_cache_tier_errors_total", labels); got != errsBefore+1 {
		t.Errorf("errors: got %v, expected %v", got, errsBefore+1)
	}
	if got := counter(t, "tegola_cache_tier_read_timeouts_total", labels); got != timeoutsBefore+1 {
		t.Errorf("read timeouts: got %v, expected %v", got, timeoutsBefore+1)
	}
	// Instrumentation wraps *outside* the deadline, so the timed-out read is
	// in the histogram. Inside it, the very events that make a too-tight
	// timeout_ms diagnosable would be the ones missing.
	if got := counter(t, "tegola_cache_tier_duration_seconds", labels); got != histBefore+1 {
		t.Errorf("duration samples: got %v, expected %v", got, histBefore+1)
	}
}

// TestClientDisconnectIsNotATierFault — the read path's other half. A client
// going away fails every in-flight read, and counting those would make a busy
// service look permanently broken.
func TestClientDisconnectIsNotATierFault(t *testing.T) {
	gate := faketier.NewGate()
	defer gate.Release()

	hot := faketier.New("hot")
	hot.GateOn(faketier.OpGet, gate)
	durable := faketier.New("durable")

	a := &Atlas{}
	// A deadline generous enough that it cannot be what fires.
	a.SetCache(tieredCache(t, "obshot4", "obsdurable4", "hot4", "durable4", hot, durable, 60000))
	a.SetObservability(newObserver(t))

	labels := map[string]string{"tier": "hot4", "sub_command": "get"}
	errsBefore := counter(t, "tegola_cache_tier_errors_total", labels)
	timeoutsBefore := counter(t, "tegola_cache_tier_read_timeouts_total", labels)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.GetCache().Get(ctx, obsKey) //nolint:errcheck
	}()

	gate.WaitEntered(1)
	cancel()
	<-done

	if got := counter(t, "tegola_cache_tier_errors_total", labels); got != errsBefore {
		t.Errorf("errors: got %v, expected it unchanged at %v", got, errsBefore)
	}
	if got := counter(t, "tegola_cache_tier_read_timeouts_total", labels); got != timeoutsBefore {
		t.Errorf("read timeouts: got %v, expected it unchanged at %v", got, timeoutsBefore)
	}
}

// TestDoubleSetObservability is the assertion that passes vacuously if written
// against a cache that was never instrumented. It runs against a real one, and
// counts.
func TestDoubleSetObservability(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")
	durable.Seed(obsKey, []byte("tile"))

	a := &Atlas{}
	a.SetCache(tieredCache(t, "obshot5", "obsdurable5", "hot5", "durable5", hot, durable, 50))

	a.SetObservability(newObserver(t))
	a.SetObservability(newObserver(t))

	// Both decorators survive the second call, or the guarantees they carry
	// silently stop applying. Reached through the unwrapping helpers, because
	// the outermost object is now the observability wrapper — which forwards
	// neither interface, and is not meant to.
	if pool := a.CacheWritePool(); pool == nil {
		t.Error("the detachment decorator was stripped by the second SetObservability")
	}
	if _, ok := cache.ChainStatsOf(a.GetCache()); !ok {
		t.Error("the chain is no longer reachable beneath the wrappers")
	}

	labels := map[string]string{"tier": "durable5", "sub_command": "get"}
	before := counter(t, "tegola_cache_tier_hits_total", labels)

	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); !hit || err != nil {
		t.Fatalf("get: got (%v, %v), expected (true, nil)", hit, err)
	}

	// Once, not twice. Twice is what a stacked instrumentation produces.
	if got := counter(t, "tegola_cache_tier_hits_total", labels); got != before+1 {
		t.Errorf("durable tier hits: got %v, expected %v — the instrumentation stacked", got, before+1)
	}

	// And the deadline is still there on the hot tier.
	gate := faketier.NewGate()
	defer gate.Release()
	hot.GateOn(faketier.OpGet, gate)

	start := time.Now()
	a.GetCache().Get(context.Background(), obsKey) //nolint:errcheck
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the read took %v — the deadline decorator was stripped", elapsed)
	}
}

// TestNestedTierLabelsArePathQualified — without path qualification, two nested
// chains each holding a `redis` share a label and both series are wrong.
func TestNestedTierLabelsArePathQualified(t *testing.T) {
	hot := faketier.New("hot")
	inner1 := faketier.New("inner1")
	inner2 := faketier.New("inner2")
	inner2.Seed(obsKey, []byte("tile"))

	for cacheType, tier := range map[string]*faketier.Tier{
		"obshot6": hot, "obsinner6a": inner1, "obsinner6b": inner2,
	} {
		c := tier
		if err := cache.Register(cacheType, func(dict.Dicter) (cache.Interface, error) { return c, nil }); err != nil {
			t.Fatalf("register %v: %v", cacheType, err)
		}
	}

	c, err := cache.For("multi", dict.Dict{
		"layers": []map[string]interface{}{
			{"type": "obshot6", "name": "hot6"},
			{"type": "multi", "name": "nested6", "layers": []map[string]interface{}{
				{"type": "obsinner6a", "name": "inner6a"},
				{"type": "obsinner6b", "name": "inner6b"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}

	a := &Atlas{}
	a.SetCache(c)
	a.SetObservability(newObserver(t))

	before := counter(t, "tegola_cache_tier_hits_total", map[string]string{"tier": "nested6/inner6b", "sub_command": "get"})

	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); !hit || err != nil {
		t.Fatalf("get: got (%v, %v), expected (true, nil)", hit, err)
	}

	if got := counter(t, "tegola_cache_tier_hits_total", map[string]string{"tier": "nested6/inner6b", "sub_command": "get"}); got != before+1 {
		t.Errorf("nested tier hits: got %v, expected %v", got, before+1)
	}
	// The nested chain itself is a tier too, and it is labelled by its own name.
	if got := counter(t, "tegola_cache_tier_hits_total", map[string]string{"tier": "nested6", "sub_command": "get"}); got == 0 {
		t.Error("the nested chain was not instrumented as a tier in its own right")
	}
}

// TestPoolAndPromotionCollectors — the drop counter is the sole detection
// mechanism for pool exhaustion, so it has to actually reach the endpoint.
func TestPoolAndPromotionCollectors(t *testing.T) {
	hot := faketier.New("hot")
	durable := faketier.New("durable")

	a := &Atlas{}
	a.SetCache(tieredCache(t, "obshot7", "obsdurable7", "hot7", "durable7", hot, durable, 0))
	a.SetObservability(newObserver(t))

	families, err := promclient.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	seen := map[string]bool{}
	for _, family := range families {
		seen[family.GetName()] = true
	}

	for _, name := range []string{
		"tegola_cache_write_slots_in_flight",
		"tegola_cache_write_slots_capacity",
		"tegola_cache_writes_dropped_total",
		"tegola_cache_writes_abandoned_total",
		"tegola_cache_writes_timed_out_total",
		"tegola_cache_writes_failed_total",
		"tegola_cache_writes_completed_total",
		"tegola_cache_write_duration_seconds_total",
		"tegola_cache_promotions_total",
		"tegola_cache_promotions_dropped_total",
	} {
		if !seen[name] {
			t.Errorf("%v is not published", name)
		}
	}

	if got := counter(t, "tegola_cache_write_slots_capacity", nil); got != 256 {
		t.Errorf("capacity: got %v, expected the 256 default", got)
	}
}

// TestErrorsCounterIsPrefixed pins the rename. The bare "errors" name collided
// between any two cache instrumentations regardless of prefix, which is what
// made the two families unregisterable together.
func TestErrorsCounterIsPrefixed(t *testing.T) {
	hot := faketier.New("hot")
	hot.FailOn(faketier.OpGet, errors.New("hot tier is down"))
	durable := faketier.New("durable")

	a := &Atlas{}
	a.SetCache(tieredCache(t, "obshot8", "obsdurable8", "hot8", "durable8", hot, durable, 0))
	a.SetObservability(newObserver(t))

	// A counter vec exposes nothing until it has a sample, so produce one.
	if _, hit, err := a.GetCache().Get(context.Background(), obsKey); hit || err != nil {
		t.Fatalf("get: got (%v, %v), expected (false, nil)", hit, err)
	}

	if seenFamily(t, "errors") {
		t.Error(`a metric is still registered under the bare name "errors"`)
	}
	if !seenFamily(t, "tegola_cache_tier_errors_total") {
		t.Error("tegola_cache_tier_errors_total is not published")
	}
	if got := counter(t, "tegola_cache_tier_errors_total", map[string]string{"tier": "hot8", "sub_command": "get"}); got != 1 {
		t.Errorf("hot tier errors: got %v, expected 1", got)
	}
}
