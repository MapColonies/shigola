package ogc

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// openAPI is the service's OpenAPI 3.0 definition.
//
// It is hand-maintained rather than generated: the surface is small and fixed by
// the specification, so a generator would add a build step and a dependency to
// describe something that changes only when the specification does. The cost is
// that it can drift from the routes — which is why a test walks the route table
// and fails if a route is undocumented.
//
//go:embed openapi.json
var openAPI []byte

// apiDocument is the parsed document, kept so that each request re-serialises
// rather than re-parses. Parsing is deferred to the first request so that a
// malformed document surfaces as a failing request rather than a panic at
// package initialisation, which would take the whole server down.
var (
	apiOnce     sync.Once
	apiDocument map[string]any
	apiErr      error
)

func parseAPI() {
	apiOnce.Do(func() {
		if err := json.Unmarshal(openAPI, &apiDocument); err != nil {
			apiErr = fmt.Errorf("the embedded OpenAPI document is not valid JSON: %w", err)
		}
	})
}

// HandleAPI serves the OpenAPI definition of this service.
//
// The document is static but for one thing: where the service is. A client
// reading a definition whose server URL points at the wrong host, or at the root
// of a service mounted behind a path prefix, sends every request to the wrong
// place — so that entry is filled in from the request.
func (s *Service) HandleAPI(w http.ResponseWriter, r *http.Request) {
	parseAPI()
	if apiErr != nil {
		writeError(w, http.StatusInternalServerError, apiErr)
		return
	}

	// A shallow copy is enough: only the top-level "servers" key is replaced,
	// and the rest of the document is shared, never mutated.
	doc := make(map[string]any, len(apiDocument))
	for k, v := range apiDocument {
		doc[k] = v
	}

	// No trailing slash: OpenAPI appends each path — which always begins with a
	// slash — to this URL verbatim, so a trailing one produces "//conformance".
	doc["servers"] = []map[string]string{
		{"url": strings.TrimSuffix(s.hrefRoot(r), "/"), "description": "this server"},
	}

	body, err := json.Marshal(doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", MediaTypeOpenAPI)
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body); err != nil {
		logf("ogc: writing the API definition: %v", err)
	}
}
