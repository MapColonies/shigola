package admin

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/MapColonies/shigola/internal/log"
)

//go:embed openapi.json
var openAPI []byte

//go:embed swaggerui/index.html swaggerui/swagger-ui.css swaggerui/swagger-ui-bundle.js
var swaggerUI embed.FS

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(openAPI); err != nil {
		log.Errorf("cache admin: writing the OpenAPI document: %v", err)
	}
}

func serveDocs(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, swaggerUI, "swaggerui/index.html")
}

func serveDocsAssets() http.Handler {
	return http.StripPrefix("/docs/", http.FileServerFS(subFS(swaggerUI, "swaggerui")))
}

func subFS(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
