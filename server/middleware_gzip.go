package server

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GZipHandler is responsible for determining if the incoming request should be served gzipped data.
// All response data is assumed to be compressed prior to being passed to this handler.
//
// If the incoming request has the "Accept-Encoding" header set with the values of "gzip" or "*"
// the response header "Content-Encoding: gzip" is set and the compressed data is returned.
//
// If no "Accept-Encoding" header is present or "Accept-Encoding" has a value of "gzip;q=0" or
// "*;q=0" the response is decompressed prior to being sent to the client.
func GZipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		acceptEncoding := r.Header.Get("Accept-Encoding")
		if acceptEncoding == "" {
			// decompress
			next.ServeHTTP(&gzipDecompressResponseWriter{resp: w}, r)
			return
		}

		decompress := false
		for _, v := range strings.Split(acceptEncoding, ",") {
			if (strings.Contains(v, "gzip") || strings.Contains(v, "*")) && strings.HasSuffix(v, ";q=0") {
				decompress = true
			}
		}

		if decompress {
			next.ServeHTTP(&gzipDecompressResponseWriter{resp: w}, r)
			return
		}

		// set appropriate header
		w.Header().Set("Content-Encoding", "gzip")

		next.ServeHTTP(w, r)
		return
	})
}

// gzipDecompressResponseWriter is responsible for decompressing responses
// when the http status code == 200.
type gzipDecompressResponseWriter struct {
	status int
	resp   http.ResponseWriter
}

func (w *gzipDecompressResponseWriter) Header() http.Header {
	return w.resp.Header()
}

func (w *gzipDecompressResponseWriter) Write(b []byte) (int, error) {
	//	check that we have an OK response, if not, don't process the body
	if w.status != http.StatusOK {
		return w.resp.Write(b)
	}

	//	setup new gzip reader
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	defer r.Close()

	var respSize int64
	respSize, err = io.Copy(w.resp, r)
	if err != nil {
		return 0, err
	}
	w.resp.Header().Set("Content-Length", fmt.Sprintf("%d", respSize))
	return int(respSize), nil
}

func (w *gzipDecompressResponseWriter) WriteHeader(i int) {
	w.resp.Header().Del("Content-Length")
	w.status = i
	w.resp.WriteHeader(i)
}

// Flush forwards to the wrapped writer if it can be flushed.
//
// Without this the tile cache middleware's flush silently no-ops for every
// client that does not send Accept-Encoding: gzip — which is ordinary traffic,
// not a corner case, since GZipHandler substitutes this wrapper for exactly
// those requests. Asserting http.Flusher on the cache middleware's own writer
// looks like it covers the case and does not.
func (w *gzipDecompressResponseWriter) Flush() {
	if f, ok := w.resp.(http.Flusher); ok {
		f.Flush()
	}
}
