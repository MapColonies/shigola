// Package server implements the http frontend
package server

import (
	"net/http"
	"net/url"
	"os"

	"github.com/dimfeld/httptreemux"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/internal/build"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/observability"
	"github.com/MapColonies/shigola/server/ogc"
)

const (
	// MaxTileSize is 500k. Currently, just throws a warning when tile
	// is larger than MaxTileSize
	MaxTileSize = 500000

	// QueryKeyDebug is a common query string key used throughout the pacakge
	// the value should always be a boolean
	QueryKeyDebug = "debug"
)

var (
	// HostName is the name of the host to use for construction of URLS.
	// configurable via the tegola config.toml file (set in main.go)
	HostName *url.URL

	// Port is the port the server is listening on, used for construction of URLS.
	// configurable via the tegola config.toml file (set in main.go)
	Port string

	// SSLCert is a filepath to an SSL cert, this will be used to enable https
	SSLCert string

	// SSLKey is a filepath to an SSL key, this will be used to enable https
	SSLKey string

	// Headers is the map of user defined response headers.
	// configurable via the tegola config.toml file (set in main.go)
	Headers = map[string]string{}

	// URIPrefix sets a prefix on all server endpoints. This is often used
	// when the server sits behind a reverse proxy with a prefix (i.e. /tegola)
	URIPrefix = "/"

	// ProxyProtocol is a custom protocol that will be used to generate the URLs
	// this server includes in its responses. This is useful when the server sits
	// behind a reverse proxy
	// (See https://github.com/go-spatial/tegola/pull/967)
	ProxyProtocol string

	// DefaultCORSHeaders define the default CORS response headers added to all requests
	DefaultCORSHeaders = map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": "GET, OPTIONS",
	}
)

// NewRouter set's up our routes.
func NewRouter(a *atlas.Atlas) *httptreemux.TreeMux {
	o := a.Observer()
	r := httptreemux.New()
	group := r.NewGroup(URIPrefix)

	// one handler to respond to all OPTIONS requests for registered routes with our CORS headers
	r.OptionsHandler = corsHandler

	if o != nil && o != observability.NullObserver {
		const (
			metricsRoute = "/metrics"
		)
		if h := o.Handler(metricsRoute); h != nil {
			// Only set up the /metrics endpoint if we have a configured observer
			log.Infof("setting up observer: %v", o.Name())
			group.UsingContext().Handler(http.MethodGet, metricsRoute, h)
		}
	}

	// map tiles
	hMapLayerZXY := HandleMapLayerZXY{Atlas: a}
	group.UsingContext().
		Handler(observability.InstrumentAPIHandler(http.MethodGet, "/maps/:map_name/:z/:x/:y", o, HeadersHandler(GZipHandler(TileCacheHandler(a, hMapLayerZXY)))))
	group.UsingContext().
		Handler(observability.InstrumentAPIHandler(http.MethodGet, "/maps/:map_name/:layer_name/:z/:x/:y", o, HeadersHandler(GZipHandler(TileCacheHandler(a, hMapLayerZXY)))))

	// OGC API - Tiles surface. Mounted with the same middleware as the native
	// routes so that headers, CORS and instrumentation behave identically.
	// It takes over "/" for the landing page (ADR-0003), which it can now do
	// unconditionally: the embedded viewer that used to be displaced to /viewer
	// no longer exists.
	ogcService := ogc.New(ogc.Config{
		Atlas: a,
		// Wrapped rather than passed directly: URLRoot is a package variable
		// that deployments such as lambda replace, and the replacement can
		// happen after the router is built.
		URLRoot:   func(r *http.Request) *url.URL { return URLRoot(r) },
		URIPrefix: URIPrefix,
		// Read here rather than assigned into a package variable by each
		// entrypoint, which is what the removed server.Version was: every
		// binary that builds a router gets the version right without having to
		// remember to set it.
		Version: build.Version,
	})
	for _, route := range ogcService.Routes() {
		var handler http.Handler = route.Handler
		if route.Gzipped {
			// The handler writes gzipped tile bytes; this negotiates whether the
			// client gets them compressed or decompressed, as on the native
			// tile routes.
			handler = GZipHandler(handler)
		}

		group.UsingContext().
			Handler(observability.InstrumentAPIHandler(route.Method, route.Path, o, HeadersHandler(handler)))
	}

	return r
}

// Start starts the tile server binding to the provided port
func Start(a *atlas.Atlas, port string) *http.Server {
	// notify the user the server is starting
	log.Infof("starting shigola server (%v) on port %v", build.Version, port)

	srv := &http.Server{Addr: port, Handler: NewRouter(a)}

	// start our server
	go func() {
		var err error

		if SSLCert+SSLKey != "" {
			err = srv.ListenAndServeTLS(SSLCert, SSLKey)
		} else {
			err = srv.ListenAndServe()
		}

		switch err {
		case nil:
			// noop
			return
		case http.ErrServerClosed:
			log.Info("http server closed")
			return
		default:
			log.Error(err)
			os.Exit(1)
			return
		}
	}()

	return srv
}

// hostName determines whether to use an user defined HostName
// or the host from the incoming request
func hostName(r *http.Request) *url.URL {
	// if the HostName has been configured, don't mutate it
	if HostName != nil {
		return HostName
	}

	// favor the r.URL.Host attribute in case tegola is behind a proxy
	// https://stackoverflow.com/questions/42921567/what-is-the-difference-between-host-and-url-host-for-golang-http-request
	if r.URL != nil && r.URL.Host != "" {
		return r.URL
	}

	return &url.URL{
		Host: r.Host,
	}
}

const (
	HeaderXForwardedProto = "X-Forwarded-Proto"
)

// various checks to determine if the request is http or https. the scheme is needed for the TileURLs
// r.URL.Scheme can be empty if a relative request is issued from the client. (i.e. GET /foo.html)
func scheme(r *http.Request) string {
	if ProxyProtocol != "" {
		return ProxyProtocol
	}

	if r.Header.Get(HeaderXForwardedProto) != "" {
		return r.Header.Get(HeaderXForwardedProto)
	}

	if r.TLS != nil {
		return "https"
	}

	return "http"
}

// URLRoot builds a string containing the scheme, host and port based on a combination of user defined values,
// headers and request parameters. The function is public so it can be overridden for other implementations.
var URLRoot = func(r *http.Request) *url.URL {
	return &url.URL{
		Scheme: scheme(r),
		Host:   hostName(r).Host,
	}
}

// corsHandler is used to respond to all OPTIONS requests for registered routes
func corsHandler(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	setHeaders(w)
}

// setHeaders sets default headers and user defined headers
func setHeaders(w http.ResponseWriter) {
	// add our default CORS headers
	for name, val := range DefaultCORSHeaders {
		if val == "" {
			log.Warnf("default CORS header (%s) has no value", name)
		}

		w.Header().Set(name, val)
	}

	// set user defined headers
	for name, val := range Headers {
		if val == "" {
			log.Warnf("header (%s) has no value", name)
		}

		w.Header().Set(name, val)
	}
}
