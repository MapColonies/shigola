// Package admin is the Cache Admin API: seeding and purging the Tile Cache over
// HTTP, on a listener of its own.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/internal/cacheops"
	"github.com/MapColonies/shigola/internal/log"
)

type Config struct {
	// Atlas holds the maps and cache to work on. Nil means the default atlas.
	Atlas *atlas.Atlas
	// Token, when set, must be presented as a bearer token on /cache/*.
	Token string
}

func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /cache/purge", cfg.authorize(cfg.handle(cacheops.Purge)))

	return mux
}

// Start serves the Cache Admin API on port until the returned server is shut down.
func Start(cfg Config, port string) *http.Server {
	log.Infof("starting the Cache Admin API on port %v", port)

	srv := &http.Server{Addr: port, Handler: New(cfg)}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(err)
			os.Exit(1)
		}
	}()

	return srv
}

func (cfg Config) authorize(next http.Handler) http.Handler {
	if cfg.Token == "" {
		return next
	}

	want := []byte("Bearer " + cfg.Token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="shigola-admin"`)
			writeJSON(w, http.StatusUnauthorized, response{Error: "a valid bearer token is required"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

type response struct {
	Purged     int64  `json:"purged"`
	Skipped    int64  `json:"skipped"`
	Failed     int64  `json:"failed"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

func (cfg Config) handle(op cacheops.Op) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var req request
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, response{Error: "invalid request body: " + err.Error()})
			return
		}

		runs, err := plan(cfg.Atlas, op, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, response{Error: err.Error()})
			return
		}

		res, err := execute(r.Context(), cfg.Atlas, op, runs)
		resp := response{Purged: res.Done, Skipped: res.Skipped, Failed: res.Failed, DurationMs: time.Since(start).Milliseconds()}

		if r.Context().Err() != nil {
			log.Infof("cache admin %v: the client went away after %d tiles; the rest was cancelled", op, res.Done)
			return
		}

		status := http.StatusOK
		if err != nil {
			resp.Error = err.Error()
			status = http.StatusInternalServerError
			log.Errorf("cache admin %v: %v", op, err)
		}

		writeJSON(w, status, resp)
	})
}

func execute(ctx context.Context, a *atlas.Atlas, op cacheops.Op, runs []run) (cacheops.Result, error) {
	var total cacheops.Result

	for _, r := range runs {
		res, err := cacheops.Run(ctx, cacheops.Options{
			Op:              op,
			Atlas:           a,
			Targets:         r.targets,
			Grid:            r.grid,
			Concurrency:     runtime.NumCPU(),
			ContinueOnError: true,
		}, cacheops.BoundsSource(ctx, r.grid, r.boxes, r.zooms))

		total.Done += res.Done
		total.Skipped += res.Skipped
		total.Failed += res.Failed

		if err != nil {
			return total, err
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}

	return total, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil && !strings.Contains(err.Error(), "broken pipe") {
		log.Errorf("cache admin: writing response: %v", err)
	}
}
