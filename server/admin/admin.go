// Package admin is the Cache Admin API: seeding and purging the Tile Cache over
// HTTP, on a listener of its own.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"syscall"
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
	// MaxTilesPerRequest caps the cache keys one request may touch, counted
	// across every tile matrix set, zoom and layer. Zero means 10000.
	MaxTilesPerRequest int
	// MaxConcurrentRequests caps the requests worked on at once. Zero means 4.
	MaxConcurrentRequests int
	// RequestTimeout cancels a request's remaining work. Zero means 60s.
	RequestTimeout time.Duration
}

const (
	DefaultMaxTilesPerRequest    = 10000
	DefaultMaxConcurrentRequests = 4
	DefaultRequestTimeout        = 60 * time.Second
)

func (cfg Config) withDefaults() Config {
	if cfg.MaxTilesPerRequest <= 0 {
		cfg.MaxTilesPerRequest = DefaultMaxTilesPerRequest
	}
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = DefaultMaxConcurrentRequests
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	return cfg
}

// Route is one documented route of the API.
type Route struct {
	Method string
	Path   string
}

func (cfg Config) routes() map[Route]http.Handler {
	slots := make(chan struct{}, cfg.MaxConcurrentRequests)

	return map[Route]http.Handler{
		{http.MethodPost, "/cache/purge"}: cfg.authorize(limit(slots, cfg.handle(cacheops.Purge))),
		{http.MethodPost, "/cache/seed"}:  cfg.authorize(limit(slots, cfg.handle(cacheops.Seed))),
		{http.MethodGet, "/openapi.json"}: http.HandlerFunc(serveOpenAPI),
		{http.MethodGet, "/docs"}:         http.HandlerFunc(serveDocs),
	}
}

// Routes lists the routes New serves, apart from the Swagger UI assets.
func Routes() []Route {
	var out []Route
	for r := range (Config{}).withDefaults().routes() {
		out = append(out, r)
	}
	return out
}

func New(cfg Config) http.Handler {
	cfg = cfg.withDefaults()
	mux := http.NewServeMux()
	for r, h := range cfg.routes() {
		mux.Handle(r.Method+" "+r.Path, h)
	}
	mux.Handle("GET /docs/", serveDocsAssets())

	return mux
}

// maxBodyBytes bounds a request body; a long tile list is still far below it.
const maxBodyBytes = 1 << 20

// Start serves the Cache Admin API on port until the returned server is shut down.
func Start(cfg Config, port string) *http.Server {
	log.Infof("starting the Cache Admin API on port %v", port)

	srv := &http.Server{Addr: port, Handler: New(cfg), ReadHeaderTimeout: 10 * time.Second}
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

// limit turns a request away with 429 while every slot is taken.
func limit(slots chan struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, response{Error: "too many cache requests in progress, retry shortly"})
		}
	})
}

// response carries purged or seeded, whichever the route does.
type response struct {
	Purged     *int64 `json:"purged,omitempty"`
	Seeded     *int64 `json:"seeded,omitempty"`
	Skipped    int64  `json:"skipped"`
	Failed     int64  `json:"failed"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

func (cfg Config) handle(op cacheops.Op) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var req request
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, response{Error: "invalid request body: " + err.Error()})
			return
		}

		j, err := plan(cfg.Atlas, op, req)
		if errors.Is(err, cacheops.ErrSeedLayers) {
			writeJSON(w, http.StatusUnprocessableEntity, response{Error: err.Error()})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, response{Error: err.Error()})
			return
		}

		n, err := count(op, j, int64(cfg.MaxTilesPerRequest))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, response{Error: err.Error()})
			return
		}
		if n > int64(cfg.MaxTilesPerRequest) {
			writeJSON(w, http.StatusUnprocessableEntity, response{Error: fmt.Sprintf(
				"the request covers %d tiles, over the limit of %d per request. narrow it, or use `shigola cache %v` for bulk work",
				n, cfg.MaxTilesPerRequest, op)})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), cfg.RequestTimeout)
		defer cancel()

		res, err := execute(ctx, cfg.Atlas, op, j)
		resp := response{Skipped: res.Skipped, Failed: res.Failed, DurationMs: time.Since(start).Milliseconds()}
		if op == cacheops.Seed {
			resp.Seeded = &res.Done
		} else {
			resp.Purged = &res.Done
		}

		if r.Context().Err() != nil {
			log.Infof("cache admin %v: the client went away after %d tiles; the rest was cancelled", op, res.Done)
			return
		}

		status := http.StatusOK
		if errors.Is(err, context.DeadlineExceeded) {
			resp.Error = fmt.Sprintf("the request timed out after %v. repeat it to finish", cfg.RequestTimeout)
			writeJSON(w, http.StatusServiceUnavailable, resp)
			return
		}
		if err != nil {
			resp.Error = err.Error()
			status = http.StatusInternalServerError
			log.Errorf("cache admin %v: %v", op, err)
		}

		writeJSON(w, status, resp)
	})
}

func execute(ctx context.Context, a *atlas.Atlas, op cacheops.Op, j job) (cacheops.Result, error) {
	var (
		total    cacheops.Result
		firstErr error
	)

	for _, r := range j.runs {
		res, err := cacheops.Run(ctx, cacheops.Options{
			Op:              op,
			Atlas:           a,
			Targets:         r.targets,
			Grid:            r.grid,
			Concurrency:     runtime.NumCPU(),
			ContinueOnError: true,
			Overwrite:       j.overwrite,
			WriteTiers:      j.writeTiers,
		}, cacheops.BoundsSource(ctx, r.grid, r.boxes, r.zooms))

		total.Done += res.Done
		total.Skipped += res.Skipped
		total.Failed += res.Failed

		// a failed grid does not stop the others: every tile is still attempted
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}

	return total, firstErr
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil && !errors.Is(err, syscall.EPIPE) {
		log.Errorf("cache admin: writing response: %v", err)
	}
}
