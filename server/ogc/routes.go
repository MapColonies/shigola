package ogc

import "net/http"

// Route is one HTTP route of the OGC surface.
//
// Routes are returned rather than registered so that the mounting server applies
// its own middleware — headers, CORS, instrumentation — to them exactly as it
// does to its native routes, and so this package needs no knowledge of that
// middleware.
type Route struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
	// Gzipped marks a route whose handler writes gzipped bytes, so the host can
	// wrap it in the middleware that either declares the encoding or
	// decompresses for a client that cannot accept it.
	Gzipped bool
}

// Routes returns every route this surface serves, in the order they should be
// registered.
func (s *Service) Routes() []Route {
	return []Route{
		{Method: http.MethodGet, Path: "/", Handler: s.HandleLandingPage},
		{Method: http.MethodGet, Path: "/conformance", Handler: s.HandleConformance},
		{Method: http.MethodGet, Path: "/tileMatrixSets", Handler: s.HandleTileMatrixSets},
		{Method: http.MethodGet, Path: "/tileMatrixSets/:tile_matrix_set_id", Handler: s.HandleTileMatrixSet},

		{Method: http.MethodGet, Path: "/collections", Handler: s.HandleCollections},
		{Method: http.MethodGet, Path: "/collections/:collection_id", Handler: s.HandleCollection},
		{Method: http.MethodGet, Path: "/collections/:collection_id/tiles", Handler: s.HandleTileSets},
		{Method: http.MethodGet, Path: "/collections/:collection_id/tiles/:tile_matrix_set_id", Handler: s.HandleTileSet},
		// z/y/x, not tegola's native z/x/y
		{
			Method:  http.MethodGet,
			Path:    "/collections/:collection_id/tiles/:tile_matrix_set_id/:tile_matrix/:tile_row/:tile_col",
			Handler: s.HandleTile,
			Gzipped: true,
		},
	}
}
