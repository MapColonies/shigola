package observability

import (
	"net/http"
	"sort"

	"github.com/prometheus/client_golang/prometheus"

	tegolaCache "github.com/MapColonies/shigola/cache"

	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/log"
)

const (
	ObserveVarMapName   = ":map_name"
	ObserveVarLayerName = ":layer_name"
	ObserveVarTileX     = ":x"
	ObserveVarTileY     = ":y"
	ObserveVarTileZ     = ":z"
)

type Collector = prometheus.Collector

// ErrObserverAlreadyExists is returned if an observer try to register to an observer type that has already registered
type ErrObserverAlreadyExists string

func (err ErrObserverAlreadyExists) Error() string {
	return "Observer " + string(err) + " already exists"
}

// ErrObserverIsNotRegistered is returned if a requested observer type has not been already registered
type ErrObserverIsNotRegistered string

func (err ErrObserverIsNotRegistered) Error() string {
	return "Observer " + string(err) + " is not registered"
}

// InitFunc is a function that is used to configure a observer
type InitFunc func(dicter dict.Dicter) (Interface, error)

// CleanUpFunc is a function that is called before the process ends.
type CleanUpFunc func()

// Interface
type Interface interface {

	// Handler returns a http.Handler for the metrics route
	Handler(route string) http.Handler

	// Returns the name of observer
	Name() string

	// Init should setup any processing that should be done once the application is actually starting
	Init()
	// Shutdown should shutdown any subsystems that Init setup
	Shutdown()

	// MustRegister will register any custom collectors. It will panic if there is an error
	MustRegister(collectors ...Collector)

	// CollectorConfig returns the config for a given key, or nil, if the key does not exist
	CollectorConfig(key string) map[string]interface{}

	APIObserver
	CacheObserver
}

type APIObserver interface {
	// InstrumentedAPIHttpHandler returns an http.Handler that will instrument the given http handler, for the
	// route and method that was given
	InstrumentedAPIHttpHandler(method, route string, handler http.Handler) http.Handler
}

type CacheObserver interface {
	InstrumentedCache(cacheObject tegolaCache.Interface) tegolaCache.Interface
}

// TieredCacheObserver instruments one tier of a composite cache, labelled by
// tier name.
//
// A separate optional interface, asserted for, rather than a method on
// CacheObserver: widening that would break internal/observer.Null and every
// other implementer for no benefit, since an observer with no notion of tiers
// simply declines to implement this and the descent stops.
//
// Implementations must emit a *different metric family* from InstrumentedCache.
// The two count different things — a whole-cache hit is one tile served from
// somewhere in the chain, tier hits are per-tier lookups, several per request —
// and registering the same metric name once with a tier label and once without
// is a label-dimension mismatch, which prometheus turns into a startup panic.
type TieredCacheObserver interface {
	InstrumentedTierCache(tier string, cacheObject tegolaCache.Interface) tegolaCache.Interface
}

type Cache interface {
	tegolaCache.Interface
	tegolaCache.Wrapped
	IsObserver() bool
}

// Observer is able to be observed via the collectors it provides
type Observer interface {
	// Collectors should return a set of collectors that will be registered by the default observability provider,
	// to get the configuration; use the provided function and your config key.
	Collectors(prefix string, config func(configKey string) map[string]interface{}) ([]Collector, error)
}

// InstrumentAPIHandler is a convenience  function
func InstrumentAPIHandler(method, route string, observer APIObserver, handler http.Handler) (string, string, http.Handler) {
	if observer == nil {
		return method, route, handler
	}
	return method, route, observer.InstrumentedAPIHttpHandler(method, route, handler)
}

type observerFunctions struct {
	Init    InitFunc
	CleanUp CleanUpFunc
}

// observers is a list of the current that have been registered with the system
var observers = map[string]observerFunctions{
	"none": {
		Init: noneInit,
	},
}

// Register is called by the init functions of the observers
func Register(observerType string, init InitFunc, cleanup CleanUpFunc) error {
	if observers == nil {
		observers = make(map[string]observerFunctions)
	}

	if _, ok := observers[observerType]; ok {
		return ErrObserverAlreadyExists(observerType)
	}
	observers[observerType] = observerFunctions{
		Init:    init,
		CleanUp: cleanup,
	}
	return nil
}

// Registered returns the set of Registered observers
func Registered() []string {
	obs := make([]string, 0, len(observers))
	for k := range observers {
		obs = append(obs, k)
	}
	sort.Strings(obs)
	return obs
}

// For function returns a configured observer for the given type, and provided config map
func For(observerType string, config dict.Dicter) (Interface, error) {
	// none should always be registered
	if observerType == "" {
		observerType = "none"
	}
	o, ok := observers[observerType]
	if !ok {
		i, _ := noneInit(nil)
		log.Errorf("did not find %v", observerType)
		return i, ErrObserverIsNotRegistered(observerType)
	}
	return o.Init(config)
}

// Cleanup should be called before shutting down the process; this allows
// and observers to clean, and maybe push any results it may have collected.
func Cleanup() {
	for _, o := range observers {
		if o.CleanUp == nil {
			continue
		}
		o.CleanUp()
	}
}

func LabelForObserveVar(key string) string {
	switch key {
	case ObserveVarMapName:
		return "map_name"
	case ObserveVarLayerName:
		return "layer_name"
	case ObserveVarTileX:
		return "x"
	case ObserveVarTileY:
		return "y"
	case ObserveVarTileZ:
		return "z"
	default:
		return ""
	}
}
