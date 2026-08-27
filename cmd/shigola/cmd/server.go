package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/build"
	gdcmd "github.com/MapColonies/shigola/internal/cmd"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/observability"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/server"
	"github.com/go-spatial/cobra"
)

var (
	serverPort      string
	serverNoCache   bool
	defaultHTTPPort = ":8080"
)

var serverCmd = &cobra.Command{
	Use:     "serve",
	Short:   "Use shigola as a tile server",
	Aliases: []string{"server"},
	Long:    `Use tegola as a vector tile server. Maps tiles will be served at /maps/:map_name/:z/:x/:y`,
	Run: func(cmd *cobra.Command, args []string) {
		gdcmd.New()
		gdcmd.OnComplete(provider.Cleanup)
		gdcmd.OnComplete(observability.Cleanup)

		// check config for server port setting
		// if you set the port via the command line it will override the port setting in the config
		if serverPort == defaultHTTPPort && conf.Webserver.Port != "" {
			serverPort = string(conf.Webserver.Port)
		}

		if conf.Webserver.HostName.Host != "" {
			u := url.URL(conf.Webserver.HostName)
			server.HostName = &u
		}

		// set our server version
		build.Commands = append(build.Commands, cmd.Name())
		atlas.StartSubProcesses()

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

		if conf.Webserver.ProxyProtocol != "" {
			server.ProxyProtocol = string(conf.Webserver.ProxyProtocol)
		}

		if conf.Webserver.SSLCert+conf.Webserver.SSLKey != "" {
			if conf.Webserver.SSLCert == "" {
				// error
				log.Error("config must have both or nether ssl_key and ssl_cert, missing ssl_cert")
				os.Exit(1)
			}

			if conf.Webserver.SSLKey == "" {
				// error
				log.Error("config must have both or nether ssl_key and ssl_cert, missing ssl_key")
				os.Exit(1)
			}

			server.SSLCert = string(conf.Webserver.SSLCert)
			server.SSLKey = string(conf.Webserver.SSLKey)
		}

		// Drain the detached write pool at shutdown, so a rolling deploy does
		// not silently discard up to a poolful of writes.
		//
		// Registration order is load-bearing and both wrong placements fail
		// silently. gdcmd.OnComplete runs in *reverse* registration order, and
		// the required execution order is:
		//
		//	1. srv.Shutdown        stop accepting requests, so no new writes
		//	2. pool drain          let the in-flight ones finish
		//	3. observability       push final metrics, including how it went
		//	4. provider cleanup
		//
		// so this must be registered after observability.Cleanup and before
		// shutdown(srv). Registered earlier it drains while requests are still
		// arriving; later, it drains after the metrics reporting its outcome
		// were already flushed.
		if pool := atlas.CacheWritePool(); pool != nil {
			gdcmd.OnComplete(func() { pool.Drain(cache.DetachedWriteDrain()) })
		}

		// start our webserver
		srv := server.Start(nil, serverPort)
		shutdown(srv)
		<-gdcmd.Cancelled()
		gdcmd.Complete()
	},
}

func shutdown(srv *http.Server) {
	gdcmd.OnComplete(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel() // releases resources if slowOperation completes before timeout elapses
		srv.Shutdown(ctx)
	})
}
