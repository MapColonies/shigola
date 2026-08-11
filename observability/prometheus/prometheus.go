package prometheus

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-spatial/tegola/internal/build"
	"github.com/prometheus/client_golang/prometheus/push"

	"github.com/go-spatial/tegola/internal/p"

	tegolaCache "github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/internal/log"
	"github.com/go-spatial/tegola/observability"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type byteSize uint64

const (
	b  byteSize = 1
	kb byteSize = 1 << (10 * iota)
	mb
	gb
)

const (
	Name = "prometheus"

	httpAPI    = "tegola_api"
	httpViewer = "tegola_viewer"

	// cachePrefix is the whole-cache family: one hit means "served from
	// somewhere". No tier label.
	cachePrefix = "tegola_cache"
	// tierCachePrefix is the per-tier family, carrying a tier label. Its
	// cardinality is the whole-cache cardinality multiplied by the tier count.
	tierCachePrefix = "tegola_cache_tier"
)

func init() {
	err := observability.Register(Name, New, cleanUp)
	if err != nil {
		panic(err)
	}
}

type observer struct {
	// URLPrefix is the server's prefix
	URLPrefix string

	// observeVars are the vars (:foo) in a url that should be turned into a label
	// Default values for this via new is []string{":map_name",":layer_name",":z"}
	observeVars []string

	httpHandlers map[string]*httpHandler
	registry     prometheus.Registerer

	publishedBuildInfo sync.Once
	initCall           sync.Once
	pushURL            string
	pushCadenceSeconds int
	pushCleanupFuncIdx int
}

func New(config dict.Dicter) (observability.Interface, error) {
	// We don't have anything for now for the config
	var obs observer
	obs.registry = prometheus.DefaultRegisterer
	obs.httpHandlers = make(map[string]*httpHandler)
	obs.pushCleanupFuncIdx = -1

	// Are we pushing our metrics?
	pushURL, _ := config.String("push_url", nil)
	if pushURL != "" {
		obs.pushURL = pushURL
		obs.pushCadenceSeconds, _ = config.Int("push_cadence", p.Int(10))
	}

	obs.observeVars, _ = config.StringSlice("variables")
	if len(obs.observeVars) == 0 {
		obs.observeVars = []string{":map_name", ":layer_name", ":z"}
	}

	NewBuildInfo(obs.registry)

	return &obs, nil
}

func (*observer) Name() string { return Name }

func (observer) Handler(string) http.Handler { return promhttp.Handler() }
func (obs *observer) Init()                  { obs.initCall.Do(obs.init) }
func (obs *observer) init() {
	obs.PublishBuildInfo()
	if obs == nil || obs.pushURL == "" {
		return
	}

	// Start up the push
	// we need to setup a clean up routine to push the metrics when we are shutting down.
	pusher := push.New(obs.pushURL, strings.Join(build.Commands, "_")).Gatherer(prometheus.DefaultGatherer)

	var (
		wg            sync.WaitGroup
		ticker        *time.Ticker
		cancel        context.CancelFunc
		ctx           context.Context
		errorReported bool
	)

	if obs.pushCadenceSeconds > 0 {
		ticker = time.NewTicker(time.Duration(obs.pushCadenceSeconds) * time.Second)
		ctx, cancel = context.WithCancel(context.Background())
		wg.Add(1)
		go func() {
			// start up our cadence
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := pusher.Add(); err != nil && !errorReported {
						log.Errorf("could not push to Pushgateway (%v): %v", obs.pushURL, err)
						errorReported = true
					}
				}
			}
		}()
	}

	cleanUpFunctionsLck.Lock()
	obs.pushCleanupFuncIdx = len(cleanUpFunctions)
	cleanUpFunctions = append(cleanUpFunctions, func() {
		if ticker != nil {
			ticker.Stop()
		}
		if cancel != nil {
			cancel()
		}
		if err := pusher.Add(); err != nil {
			log.Errorf("could not push to Pushgateway (%v): %v", obs.pushURL, err)
		}
		wg.Wait()
	})
	cleanUpFunctionsLck.Unlock()
}

func (obs *observer) Shutdown() {
	if obs.pushCleanupFuncIdx < 0 {
		return
	}
	cleanUpFunctionsLck.Lock()
	cleanUpFunctions[obs.pushCleanupFuncIdx]()
	cleanUpFunctions[obs.pushCleanupFuncIdx] = nil
	obs.pushCleanupFuncIdx = -1
	cleanUpFunctionsLck.Unlock()
}

// MustRegister registers collectors, treating an exact duplicate as a no-op.
//
// The registry is process-wide, so anything registered from a per-object
// lifecycle — the cache write-pool collectors, per-map collectors — is
// registered again whenever that object is rebuilt or a second observer is
// constructed. MustRegister on the raw registry panics on that, which makes
// SetObservability single-shot in practice.
//
// Only AlreadyRegisteredError is tolerated, and that is narrow: the registry
// returns it when an *equal* collector is already present. A genuinely
// conflicting registration — same metric name, different label dimensions —
// returns a different error and still panics, which is what should happen.
func (obs *observer) MustRegister(collectors ...observability.Collector) {
	for _, c := range collectors {
		if err := obs.registry.Register(c); err != nil {
			var already prometheus.AlreadyRegisteredError
			if errors.As(err, &already) {
				continue
			}
			panic(err)
		}
	}
}

func (_ *observer) CollectorConfig(_ string) map[string]interface{} {
	return make(map[string]interface{})
}

func (obs *observer) PublishBuildInfo() { obs.publishedBuildInfo.Do(PublishBuildInfo) }

func (obs *observer) InstrumentedAPIHttpHandler(method, route string, next http.Handler) http.Handler {
	if obs == nil {
		return next
	}
	handler := obs.httpHandlers[httpAPI]
	if handler == nil {
		// need to initialize the handler
		handler = newHttpHandler(obs.registry, httpAPI, obs.URLPrefix, obs.observeVars)
		obs.httpHandlers[httpAPI] = handler
	}
	return handler.InstrumentedHttpHandler(method, route, next)
}

func (obs *observer) InstrumentedViewerHttpHandler(method, route string, next http.Handler) http.Handler {
	if obs == nil {
		return next
	}
	handler := obs.httpHandlers[httpViewer]
	if handler == nil {
		// need to initialize the handler
		handler = newHttpHandler(obs.registry, httpViewer, obs.URLPrefix, obs.observeVars)
		obs.httpHandlers[httpViewer] = handler
	}
	return handler.InstrumentedHttpHandler(method, route, next)
}

func (obs *observer) InstrumentedCache(cacheObject tegolaCache.Interface) tegolaCache.Interface {
	if obs == nil {
		// if we are nil assume no metrics recording is going to happen
		return cacheObject
	}
	return newCache(obs.registry, cachePrefix, obs.observeVars, cacheObject)
}

// InstrumentedTierCache instruments one tier of a composite cache.
//
// A separate metric family, not the same one with a tier label added. Two
// reasons, and the second would matter on its own:
//
// It panics otherwise. WrapRegistererWith merges the label into constLabels,
// const label *names* feed dimHash, and the registry rejects a second
// descriptor with the same fully-qualified name and a different dimHash —
// through MustRegister, which panics rather than returning. Registering
// tegola_cache_hits_total once without a tier label and once with one would
// therefore fail at startup on the first chain deployment with an observer
// configured.
//
// And they count different things. A whole-cache hit is one tile served from
// somewhere in the chain; tier hits are per-tier lookups, several per request.
// sum(tegola_cache_tier_hits_total) is *not* the chain hit count, and separate
// names make that impossible to get wrong by accident.
func (obs *observer) InstrumentedTierCache(tier string, cacheObject tegolaCache.Interface) tegolaCache.Interface {
	if obs == nil {
		return cacheObject
	}

	registry := prometheus.WrapRegistererWith(prometheus.Labels{"tier": tier}, obs.registry)

	return newCache(registry, tierCachePrefix, obs.observeVars, cacheObject)
}

var _ observability.TieredCacheObserver = (*observer)(nil)

var (
	cleanUpFunctionsLck sync.Mutex
	cleanUpFunctions    []func()
)

func cleanUp() {
	cleanUpFunctionsLck.Lock()
	for i := range cleanUpFunctions {
		if cleanUpFunctions[i] != nil {
			cleanUpFunctions[i]()
			cleanUpFunctions[i] = nil
		}
	}
	cleanUpFunctions = cleanUpFunctions[:0]
	cleanUpFunctionsLck.Unlock()
}
