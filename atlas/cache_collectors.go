package atlas

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/observability"
)

// cacheStatsCollector publishes the write pool's and the chain's counters.
//
// They cannot publish themselves: observability imports cache, so cache can
// never import observability and nothing in it can emit a metric. They cross
// the cycle as plain snapshot structs and this collector reads them on each
// scrape — the same bridge atlas already provides for per-map collectors, just
// pointed somewhere new.
//
// Without it the drop counter does not exist anywhere, and pool exhaustion —
// which is otherwise indistinguishable from a healthy chain: same latencies,
// same status codes, just an unexplained rise in miss rate — has no signal
// beyond a rate-limited log line.
type cacheStatsCollector struct {
	pool  *cache.WritePool
	chain cache.ChainStatsHolder

	slotsInFlight *prometheus.Desc
	slotsCapacity *prometheus.Desc

	dropped   *prometheus.Desc
	abandoned *prometheus.Desc
	timedOut  *prometheus.Desc

	failed        *prometheus.Desc
	completed     *prometheus.Desc
	writeDuration *prometheus.Desc

	promotions        *prometheus.Desc
	promotionsDropped *prometheus.Desc
}

var _ prometheus.Collector = (*cacheStatsCollector)(nil)

func newCacheStatsCollector(prefix string, pool *cache.WritePool, chain cache.ChainStatsHolder) *cacheStatsCollector {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prefix+name, help, nil, nil)
	}

	return &cacheStatsCollector{
		pool:  pool,
		chain: chain,

		slotsInFlight: desc("_write_slots_in_flight",
			"Detached cache writes currently in flight. Alert on this approaching capacity: drops only begin once the pool is already exhausted."),
		slotsCapacity: desc("_write_slots_capacity",
			"Configured detached write slots (TEGOLA_OPTIONS=DetachedWriteSlots)."),

		dropped: desc("_writes_dropped_total",
			"Detached writes never attempted because the pool was full at admission."),
		abandoned: desc("_writes_abandoned_total",
			"Detached writes still running when the shutdown drain deadline expired."),
		timedOut: desc("_writes_timed_out_total",
			"Detached writes killed by DetachedWriteTimeoutMs. Also counted in writes_failed_total. The earliest warning that the durable tier is degrading."),

		failed:    desc("_writes_failed_total", "Detached writes attempted that returned an error."),
		completed: desc("_writes_completed_total", "Detached writes attempted that succeeded."),
		writeDuration: desc("_write_duration_seconds_total",
			"Cumulative duration of completed detached writes. A mean when divided by writes_completed_total; read the tail from tegola_cache_tier_duration_seconds{sub_command=\"set\"} instead."),

		promotions:        desc("_promotions_total", "Tiles promoted into an earlier tier after a lower-tier hit."),
		promotionsDropped: desc("_promotions_dropped_total", "Promotions the write pool refused at admission."),
	}
}

func (c *cacheStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	if c.pool != nil {
		ch <- c.slotsInFlight
		ch <- c.slotsCapacity
		ch <- c.dropped
		ch <- c.abandoned
		ch <- c.timedOut
		ch <- c.failed
		ch <- c.completed
		ch <- c.writeDuration
	}
	if c.chain != nil {
		ch <- c.promotions
		ch <- c.promotionsDropped
	}
}

func (c *cacheStatsCollector) Collect(ch chan<- prometheus.Metric) {
	if c.pool != nil {
		// One snapshot per scrape, so the emitted values are consistent with
		// each other rather than sampled at eight different instants.
		s := c.pool.Stats()

		ch <- prometheus.MustNewConstMetric(c.slotsInFlight, prometheus.GaugeValue, float64(s.InFlight))
		ch <- prometheus.MustNewConstMetric(c.slotsCapacity, prometheus.GaugeValue, float64(s.Capacity))
		ch <- prometheus.MustNewConstMetric(c.dropped, prometheus.CounterValue, float64(s.Dropped))
		ch <- prometheus.MustNewConstMetric(c.abandoned, prometheus.CounterValue, float64(s.Abandoned))
		ch <- prometheus.MustNewConstMetric(c.timedOut, prometheus.CounterValue, float64(s.TimedOut))
		ch <- prometheus.MustNewConstMetric(c.failed, prometheus.CounterValue, float64(s.Failed))
		ch <- prometheus.MustNewConstMetric(c.completed, prometheus.CounterValue, float64(s.Completed))
		ch <- prometheus.MustNewConstMetric(c.writeDuration, prometheus.CounterValue, float64(s.WriteNanos)/float64(1e9))
	}

	if c.chain != nil {
		s := c.chain.ChainStats()

		ch <- prometheus.MustNewConstMetric(c.promotions, prometheus.CounterValue, float64(s.Promotions))
		ch <- prometheus.MustNewConstMetric(c.promotionsDropped, prometheus.CounterValue, float64(s.PromotionsDropped))
	}
}

// cacheCollectors returns the collectors for the configured cache, or nil if
// there is nothing to publish.
func cacheCollectors(c cache.Interface) []observability.Collector {
	pool := cache.WritePoolOf(c)
	chain, _ := cache.ChainStatsOf(c)

	if pool == nil && chain == nil {
		return nil
	}

	return []observability.Collector{newCacheStatsCollector("tegola_cache", pool, chain)}
}
