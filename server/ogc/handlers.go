package ogc

import (
	"net/http"

	"github.com/dimfeld/httptreemux"

	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/tms"
)

// logf keeps the package's logging in one place.
func logf(format string, args ...any) { log.Errorf(format, args...) }

// HandleLandingPage serves "/", the document every OGC API client starts from.
func (s *Service) HandleLandingPage(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, format, LandingPage{
		Title:       "shigola",
		Description: "OGC API - Tiles served by shigola",
		Links: []Link{
			{Rel: relSelf, Href: s.hrefRoot(r), Type: MediaTypeJSON, Title: "this document"},
			{Rel: relServiceDesc, Href: s.href(r, "api"), Type: MediaTypeOpenAPI, Title: "the API definition"},
			{Rel: relConformance, Href: s.href(r, "conformance"), Type: MediaTypeJSON, Title: "conformance classes implemented by this server"},
			{Rel: relData, Href: s.href(r, "collections"), Type: MediaTypeJSON, Title: "the collections in this dataset"},
			{Rel: relTilingSchemes, Href: s.href(r, "tileMatrixSets"), Type: MediaTypeJSON, Title: "the tiling schemes this server supports"},
		},
	})
}

// HandleConformance serves the conformance declaration.
func (s *Service) HandleConformance(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, format, Conformance{ConformsTo: conformsTo})
}

// HandleTileMatrixSets serves the list of tiling schemes this build can serve.
//
// It lists only servable schemes: advertising one the server would then refuse a
// tile in describes a capability that does not exist.
func (s *Service) HandleTileMatrixSets(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	grids := tms.List()
	items := make([]TileMatrixSetItem, 0, len(grids))

	for _, grid := range grids {
		items = append(items, TileMatrixSetItem{
			ID:    grid.ID(),
			Title: grid.Title(),
			URI:   grid.URI(),
			CRS:   grid.CRSURI(),
			Links: []Link{
				{Rel: relSelf, Href: s.href(r, "tileMatrixSets", grid.ID()), Type: MediaTypeJSON, Title: grid.ID()},
			},
		})
	}

	writeJSON(w, format, TileMatrixSets{TileMatrixSets: items})
}

// HandleTileMatrixSet serves one tiling scheme's definition.
//
// The body is the bundled OGC document itself, served verbatim rather than
// re-marshalled from the parsed model, so a client comparing it against the OGC
// registry sees the same document.
func (s *Service) HandleTileMatrixSet(w http.ResponseWriter, r *http.Request) {
	format, err := negotiate(r, FormatJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	id := httptreemux.ContextParams(r.Context())["tile_matrix_set_id"]

	grid, err := tms.Get(id)
	if err != nil {
		// Registered-but-gated and entirely unknown are both "this server does
		// not serve that scheme" as far as a client is concerned.
		writeError(w, http.StatusNotFound, err)
		return
	}

	body, err := grid.DefinitionJSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", format.mediaType())
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body); err != nil {
		logf("ogc: writing tile matrix set %v: %v", id, err)
	}
}
