package prometheus

import (
	"context"
	"errors"
	"strconv"
	"time"

	tegolaCache "github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/observability"
	"github.com/prometheus/client_golang/prometheus"
)

type cache struct {
	observeVars       []string
	cache             tegolaCache.Interface
	hitsCounter       *prometheus.CounterVec
	missesCounter     *prometheus.CounterVec
	inFlightGauge     prometheus.Gauge
	durationSeconds   *prometheus.HistogramVec
	responseSizeBytes *prometheus.HistogramVec
	errors            *prometheus.CounterVec
	readTimeouts      *prometheus.CounterVec
}

// registerOrReuse registers c, or returns the equivalent collector that is
// already registered.
//
// MustRegister panics on a duplicate, and there are two ways to reach one:
// SetObservability can be called more than once against the same process-wide
// default registry, and a chain registers the per-tier family once per tier.
// Reusing the existing collector is what makes re-instrumentation idempotent
// rather than fatal.
func registerOrReuse[T prometheus.Collector](registry prometheus.Registerer, c T) T {
	err := registry.Register(c)
	if err == nil {
		return c
	}

	var already prometheus.AlreadyRegisteredError
	if errors.As(err, &already) {
		if existing, ok := already.ExistingCollector.(T); ok {
			return existing
		}
	}

	panic(err)
}

func newCache(registry prometheus.Registerer, prefix string, observeVars []string, subCache tegolaCache.Interface) *cache {
	var c = cache{
		observeVars: observeVars,
		cache:       subCache,
	}

	c.inFlightGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_flight_requests",
		Help: "A gauge of requests currently being handled by the cache",
	})
	names := c.labelNames()

	c.hitsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prefix + "_hits_total",
			Help: "A counter of the number of tile hits",
		},
		names,
	)
	c.missesCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prefix + "_misses_total",
			Help: "A counter of the number of tile misses",
		},
		names,
	)
	// Prefixed. It was registered as a bare "errors" until 2026-08-10, unlike
	// every sibling in this constructor — which squats a maximally generic name
	// in the global metric namespace and, more immediately, collides between
	// *any* two cache instrumentations regardless of their prefixes. That
	// collision is what makes the whole-cache and per-tier families
	// unregisterable together. **This renames a metric that exists today.**
	c.errors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prefix + "_errors_total",
			Help: "A counter of the number of tile errors",
		},
		names,
	)
	c.readTimeouts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prefix + "_read_timeouts_total",
			Help: "A counter of reads abandoned because their timeout_ms deadline expired",
		},
		names,
	)

	c.durationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    prefix + "_duration_seconds",
			Help:    "A histogram of latencies for requests.",
			Buckets: []float64{.25, .5, 1, 2.5, 5, 10},
		},
		names,
	)

	c.responseSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    prefix + "_response_size_bytes",
			Help:    "A histogram of response sizes for requests.",
			Buckets: []float64{float64(500 * kb), float64(1 * mb), float64(5 * mb)},
		},
		names,
	)

	// Register our variables
	c.inFlightGauge = registerOrReuse(registry, c.inFlightGauge)
	c.hitsCounter = registerOrReuse(registry, c.hitsCounter)
	c.missesCounter = registerOrReuse(registry, c.missesCounter)
	c.durationSeconds = registerOrReuse(registry, c.durationSeconds)
	c.responseSizeBytes = registerOrReuse(registry, c.responseSizeBytes)
	c.errors = registerOrReuse(registry, c.errors)
	c.readTimeouts = registerOrReuse(registry, c.readTimeouts)

	return &c
}

// labelNames returns the label name based on the configured observeVars and "sub_command"
func (co *cache) labelNames() (names []string) {
	names = []string{"sub_command"}
	for _, key := range co.observeVars {
		if name := observability.LabelForObserveVar(key); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// labels returns prometheus.Labels based on the configured observeVars
func (co *cache) labels(cmd string, key *tegolaCache.Key) (lbs prometheus.Labels) {
	lbs = make(prometheus.Labels)
	for _, keyName := range co.observeVars {
		switch keyName {
		case observability.ObserveVarMapName:
			lbs["map_name"] = key.MapName
		case observability.ObserveVarLayerName:
			lbs["layer_name"] = key.LayerName
		case observability.ObserveVarTileX:
			lbs["x"] = strconv.FormatInt(int64(key.X), 10)
		case observability.ObserveVarTileY:
			lbs["y"] = strconv.FormatInt(int64(key.Y), 10)
		case observability.ObserveVarTileZ:
			lbs["z"] = strconv.FormatInt(int64(key.Z), 10)
		}
	}
	lbs["sub_command"] = cmd
	return lbs
}

// Get will record metrics around the getting the tile from the sub cache
func (co *cache) Get(ctx context.Context, key *tegolaCache.Key) ([]byte, bool, error) {
	co.inFlightGauge.Inc()
	lbs := co.labels("get", key)
	now := time.Now()
	body, ok, err := co.cache.Get(ctx, key)
	// Observed outside the deadline, deliberately: instrumenting inside it
	// would drop timed-out reads from the histogram — the very events that
	// make a too-tight timeout_ms diagnosable.
	co.durationSeconds.With(lbs).Observe(time.Since(now).Seconds())
	if err != nil {
		co.countReadError(ctx, lbs, err)
		co.inFlightGauge.Dec()
		return body, ok, err
	}
	if ok {
		co.hitsCounter.With(lbs).Add(1)
	} else {
		co.missesCounter.With(lbs).Add(1)
	}

	co.responseSizeBytes.With(lbs).Observe(float64(len(body)))
	co.inFlightGauge.Dec()
	return body, ok, nil
}

// countReadError attributes a failed read.
//
//	ErrTierTimeout          -> _errors_total and _read_timeouts_total
//	any other error         -> _errors_total
//	parent context done     -> neither
//
// The last row is the point. The middleware passes the request context, so a
// client disconnecting mid-request fails every in-flight read — and counting
// those against the cache would make a busy service look permanently broken.
// Only a deadline the cache derived itself is a cache fault, and it says so by
// returning the typed error.
func (co *cache) countReadError(ctx context.Context, lbs prometheus.Labels, err error) {
	var tierTimeout tegolaCache.ErrTierTimeout
	if errors.As(err, &tierTimeout) {
		co.errors.With(lbs).Add(1)
		co.readTimeouts.With(lbs).Add(1)
		return
	}

	if ctx.Err() != nil {
		return
	}

	co.errors.With(lbs).Add(1)
}

// Set will observe metrics around setting the tile via the sub cache.
func (co *cache) Set(ctx context.Context, key *tegolaCache.Key, body []byte) error {
	co.inFlightGauge.Inc()
	lbs := co.labels("set", key)
	now := time.Now()
	err := co.cache.Set(ctx, key, body)
	co.durationSeconds.With(lbs).Observe(time.Since(now).Seconds())
	if err != nil {
		co.errors.With(lbs).Add(1)
		co.inFlightGauge.Dec()
		return err
	}
	co.responseSizeBytes.With(lbs).Observe(float64(len(body)))
	co.inFlightGauge.Dec()
	return nil
}

// Purge will record the metrics around purging the tile from the sub cache.
func (co *cache) Purge(ctx context.Context, key *tegolaCache.Key) error {
	co.inFlightGauge.Inc()
	lbs := co.labels("purge", key)
	now := time.Now()
	err := co.cache.Purge(ctx, key)
	co.durationSeconds.With(lbs).Observe(time.Since(now).Seconds())
	if err != nil {
		co.errors.With(lbs).Add(1)
	}
	co.inFlightGauge.Dec()
	return nil
}

// Original returns the cache this one instruments. It is the cache.Wrapped
// half of observability.Cache, which is how atlas unwraps an already
// instrumented cache instead of instrumenting it a second time. The method was
// named Wrapped() until 2026-08-10, which no interface required and nothing
// called, so the assertion below is the whole point of the rename.
func (co cache) Original() tegolaCache.Interface { return co.cache }
func (co cache) IsObserver() bool                { return true }

var (
	_ observability.Cache = (*cache)(nil)
	_ tegolaCache.Wrapped = (*cache)(nil)
)
