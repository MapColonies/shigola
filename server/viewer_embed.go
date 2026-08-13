//go:build !noViewer && go1.16
// +build !noViewer,go1.16

package server

import (
	"net/http"
	"path"

	"github.com/dimfeld/httptreemux"

	"github.com/go-spatial/tegola/observability"
	"github.com/go-spatial/tegola/ui"
)

// ViewerPath is where the embedded viewer is served from.
//
// It used to be "/", which OGC API - Tiles requires for the landing page, so the
// viewer moved here — the one non-additive change in the OGC work (ADR-0003).
const ViewerPath = "/viewer"

// setupViewer in this file is used for registering the viewer routes when the viewer
// is included in the build (default)
func setupViewer(o observability.Interface, group *httptreemux.Group) {
	// We need to Strip the URIPrefix and the viewer's own path from the request
	// path before serving the file. The prefix is for when the server sits
	// behind a reverse proxy (i.e. /tegola).
	prefix := path.Join(URIPrefix, ViewerPath)
	fileServer := http.StripPrefix(prefix, http.FileServer(ui.GetDistFileSystem()))

	// The viewer's assets are referenced relatively (vite is configured with an
	// empty base), so they resolve against the document's own URL. Without the
	// trailing slash a browser resolves them one level up — against the service
	// root — and every asset 404s. Hence the redirect rather than serving the
	// index here.
	group.UsingContext().Handler(observability.InstrumentViewerHandler(http.MethodGet, ViewerPath, o,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
		}),
	))
	group.UsingContext().Handler(observability.InstrumentViewerHandler(http.MethodGet, ViewerPath+"/*path", o, fileServer))
}
