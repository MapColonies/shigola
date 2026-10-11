package server

import (
	"net/http"
	"strings"
)

// DefaultTileCacheControl is what tiles carry when neither TileCacheControl
// nor a generic Cache-Control header is configured.
const DefaultTileCacheControl = "public, max-age=60, stale-while-revalidate=300"

var (
	// TileCacheControl is the Cache-Control of tile responses.
	TileCacheControl string
	// MetadataCacheControl is the Cache-Control of every other response.
	MetadataCacheControl string
)

// CacheControlHandler sets the Cache-Control of next's response by surface,
// overriding a generic Cache-Control header, and marks errors no-store.
func CacheControlHandler(tile bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &cacheControlWriter{ResponseWriter: w, value: cacheControl(tile)}
		next.ServeHTTP(cw, r)
		if !cw.wroteHeader && cw.value != "" {
			w.Header().Set("Cache-Control", cw.value)
		}
	})
}

func cacheControl(tile bool) string {
	if !tile {
		return MetadataCacheControl
	}
	if TileCacheControl != "" {
		return TileCacheControl
	}
	if HeadersSetCacheControl() {
		return ""
	}
	return DefaultTileCacheControl
}

// HeadersSetCacheControl reports whether Headers carries a Cache-Control.
func HeadersSetCacheControl() bool {
	for name := range Headers {
		if strings.EqualFold(name, "Cache-Control") {
			return true
		}
	}
	return false
}

type cacheControlWriter struct {
	http.ResponseWriter
	value       string
	wroteHeader bool
}

func (w *cacheControlWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		switch {
		case code >= 400:
			w.Header().Set("Cache-Control", "no-store")
		case w.value != "":
			w.Header().Set("Cache-Control", w.value)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *cacheControlWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *cacheControlWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *cacheControlWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
