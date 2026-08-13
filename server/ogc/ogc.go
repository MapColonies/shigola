// Package ogc serves the OGC API - Tiles surface: a landing page, a
// conformance declaration, collections, tilesets, tiles, and the tiling schemes
// they are cut in.
//
// It is additive to tegola's native routes, which keep serving the same tiles at
// the same URLs. The one thing it takes over is "/", the landing page's required
// location, which displaces the embedded viewer to /viewer (ADR-0003).
//
// The package deliberately does not import tegola/server: the server mounts this
// surface, so the dependency runs one way. Everything this package needs from
// its host — where the service is reachable, what it is mounted under, which
// atlas to read — arrives in Config.
package ogc

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/go-spatial/tegola/atlas"
)

// Config is what the OGC surface needs from the server mounting it.
type Config struct {
	// Atlas holds the maps this surface publishes as collections. Required.
	Atlas *atlas.Atlas
	// URLRoot returns the scheme and host the service is reachable at. It is a
	// function because the answer depends on the request — forwarding headers, a
	// configured hostname — and because deployments such as lambda override it.
	// Required.
	URLRoot func(*http.Request) *url.URL
	// URIPrefix is the path the service is mounted under, e.g. "/tegola" behind
	// a reverse proxy. Empty means "/".
	URIPrefix string
}

// Service serves the OGC API - Tiles resources for one atlas.
type Service struct {
	cfg Config
}

// New returns a Service for cfg.
func New(cfg Config) *Service {
	if cfg.URIPrefix == "" {
		cfg.URIPrefix = "/"
	}

	return &Service{cfg: cfg}
}

// href builds the absolute URL of a resource within this service, so that every
// link a client follows carries the same scheme, host and mount prefix it
// arrived on.
func (s *Service) href(r *http.Request, elem ...string) string {
	u := *s.cfg.URLRoot(r)
	u.Path = path.Join(append([]string{s.cfg.URIPrefix}, elem...)...)

	return u.String()
}

// hrefTemplate builds a URL whose path ends in URI template variables.
//
// Templates cannot go through href: path.Join would leave "{tileMatrix}" alone
// but url.URL escapes the braces when the URL is formatted, and a percent-escaped
// template is no longer a template a client can fill in.
func (s *Service) hrefTemplate(r *http.Request, base []string, template ...string) string {
	return s.href(r, base...) + "/" + strings.Join(template, "/")
}
