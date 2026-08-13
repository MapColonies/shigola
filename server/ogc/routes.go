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
}

// Routes returns every route this surface serves, in the order they should be
// registered.
func (s *Service) Routes() []Route {
	return []Route{
		{http.MethodGet, "/", s.HandleLandingPage},
		{http.MethodGet, "/conformance", s.HandleConformance},
		{http.MethodGet, "/tileMatrixSets", s.HandleTileMatrixSets},
		{http.MethodGet, "/tileMatrixSets/:tile_matrix_set_id", s.HandleTileMatrixSet},
	}
}
