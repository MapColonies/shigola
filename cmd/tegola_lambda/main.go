package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/akrylysov/algnhsa"
	"github.com/dimfeld/httptreemux"
	"github.com/go-spatial/geom/encoding/mvt"

	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/cmd/internal/register"
	"github.com/go-spatial/tegola/config"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/internal/build"
	"github.com/go-spatial/tegola/internal/log"
	"github.com/go-spatial/tegola/server"
)

// mux is a reference to the http muxer. it's stored as a package
// var so we can take advantage of Lambda's "Global State".
var mux *httptreemux.TreeMux

const DefaultConfLocation = "config.toml"

// instantiate the server during the init() function and then store
// the muxer in a package variable. This allows us to take advantage
// of "Global State" to avoid needing to re-parse the config, connect
// to databases, tile caches, etc. on each function invocation.
//
// For more info, see Using Global State:
// https://docs.aws.amazon.com/lambda/latest/dg/go-programming-model-handler-types.html
func init() {
	var err error

	// override the URLRoot func with a lambda specific one
	server.URLRoot = URLRoot

	confLocation := DefaultConfLocation

	// check if the env TEGOLA_CONFIG is set
	if os.Getenv("TEGOLA_CONFIG") != "" {
		confLocation = os.Getenv("TEGOLA_CONFIG")
	}

	// read our config
	conf, err := config.Load(confLocation)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}

	// validate our config
	if err = conf.Validate(); err != nil {
		log.Error(err)
		os.Exit(1)
	}

	// init our providers
	// but first convert []env.Map -> []dict.Dicter
	provArr := make([]dict.Dicter, len(conf.Providers))
	for i := range provArr {
		provArr[i] = conf.Providers[i]
	}

	// register the providers
	providers, err := register.Providers(provArr, nil)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}

	// register the maps
	if err = register.Maps(nil, conf.Maps, providers); err != nil {
		log.Error(err)
		os.Exit(1)
	}

	// check if a cache backend is provided
	if len(conf.Cache) != 0 {
		// register the cache backend
		cacher, err := register.Cache(conf.Cache)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
		if cacher != nil {
			atlas.SetCache(cacher)
		}
	}

	// set our server version
	server.Version = build.Version
	if conf.Webserver.HostName.Host != "" {
		u := url.URL(conf.Webserver.HostName)
		server.HostName = &u
	}

	// set user defined response headers
	for name, value := range conf.Webserver.Headers {
		// cast to string
		val := fmt.Sprintf("%v", value)
		// check that we have a value set
		if val == "" {
			log.Errorf("webserver.header (%v) has no configured value", val)
			os.Exit(1)
		}

		server.Headers[name] = val
	}

	if conf.Webserver.URIPrefix != "" {
		server.URIPrefix = string(conf.Webserver.URIPrefix)
	}

	// http route setup
	mux = server.NewRouter(nil)
}

// synchronousCacheWrites makes every request write to the cache inline.
//
// Lambda freezes the execution environment as soon as the handler returns, so a
// detached write has no goroutine left to run on: it is lost, silently, on
// every cache miss. There is no shutdown hook to drain from either — a freeze
// is not a shutdown.
//
// The cost is the one detachment exists to avoid: the response waits on the
// cache save. On Lambda that trade is already made for us, since billing runs
// until the handler returns and a background write would be unbilled and
// unfinished rather than free.
func synchronousCacheWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(cache.WithSynchronousWrites(r.Context())))
	})
}

func main() {
	build.Commands = []string{"lambda"}
	// the second argument here tells algnhsa to watch for the MVT MimeType Content-Type headers
	// if it detects this in the response the payload will be base64 encoded. Lambda needs to be configured
	// to handle binary responses so it can convert the base64 encoded payload back into binary prior
	// to sending to the client
	algnhsa.ListenAndServe(synchronousCacheWrites(mux), &algnhsa.Options{
		BinaryContentTypes: []string{mvt.MimeType},
		UseProxyPath:       true,
	})
}

// URLRoot overrides the default server.URLRoot function in order to include the "stage" part of the root
// that is part of lambda's URL scheme
func URLRoot(r *http.Request) *url.URL {
	u := url.URL{
		Scheme: scheme(r),
		Host:   r.Header.Get("Host"),
	}

	// read the request context to pull out the lambda "stage" so it can be prepended to the URL Path
	if ctx, ok := algnhsa.APIGatewayV1RequestFromContext(r.Context()); ok {
		u.Path = ctx.RequestContext.Stage
	}

	return &u
}

// various checks to determine if the request is http or https. the scheme is needed for the TileJSON URLs
// r.URL.Scheme can be empty if a relative request is issued from the client. (i.e. GET /foo.html)
func scheme(r *http.Request) string {
	if r.Header.Get("X-Forwarded-Proto") != "" {
		return r.Header.Get("X-Forwarded-Proto")
	} else if r.TLS != nil {
		return "https"
	}

	return "http"
}
