// Package cacheops seeds and purges tiles in the Tile Cache. It is the engine
// behind `shigola cache seed|purge` and the Cache Admin API.
package cacheops

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom/slippy"
)

type Op int

const (
	Seed Op = iota + 1
	Purge
)

func (o Op) String() string {
	if o == Purge {
		return "purge"
	}
	return "seed"
}

// TileSource yields the tiles of a run. Err is read once Channel is closed.
type TileSource interface {
	Channel() <-chan slippy.Tile
	Err() error
}

// Stopper is a TileSource that can stop producing early. Run stops one when
// it ends before the source is exhausted.
type Stopper interface {
	Stop()
}

type tileChan <-chan slippy.Tile

func (c tileChan) Channel() <-chan slippy.Tile { return c }
func (tileChan) Err() error                    { return nil }

// TileChan adapts a plain channel that cannot fail into a TileSource.
func TileChan(c <-chan slippy.Tile) TileSource { return tileChan(c) }

// Target is a map, optionally narrowed to some of its layers. With no layers a
// purge clears the whole-map tile and every layer tile, and a seed writes the
// whole-map tile; with layers either works on those and the whole-map tile,
// which contains them too.
type Target struct {
	Map    atlas.Map
	Layers []string
}

type Options struct {
	Op Op
	// Atlas holds the cache the run works on. Nil means the default atlas.
	Atlas       *atlas.Atlas
	Targets     []Target
	Grid        *tms.TileMatrixSet
	Concurrency int
	// Overwrite re-seeds tiles that are already cached. Seed only.
	Overwrite bool
	// WriteTiers restricts the tiers of a layered cache the run writes. Nil means all.
	WriteTiers []string
	// CollectGarbage runs the GC after each seeded tile. For a dedicated
	// process only: on a serving pod it stalls tile requests.
	CollectGarbage bool
	// LogThreshold logs seeded tiles that took at least this long.
	LogThreshold time.Duration
	// ContinueOnError attempts every tile even after one fails, counting the
	// failures. Run then returns the first failure once it is done.
	ContinueOnError bool
	// Draining, if set, is called once the source is exhausted and the run is
	// waiting on its workers; the func it returns is called when they finish.
	Draining func() (done func())
}

// Result counts map tiles, so one tile across two maps counts twice.
type Result struct {
	Done    int64
	Skipped int64
	Failed  int64
}

var ErrNoMaps = errors.New("cacheops: no maps to work on")

// ErrUnprocessable matches, through errors.Is, every target error that names
// maps and layers that exist but asks for work that cannot be done, as against
// a target that is simply wrong.
var ErrUnprocessable = errors.New("cacheops: the target cannot be honoured")

type unprocessable string

func (e unprocessable) Error() string      { return string(e) }
func (unprocessable) Is(target error) bool { return target == ErrUnprocessable }

// ErrNoLayerCollections is wrapped by an ErrInvalidTarget for a target naming
// layers of a map that does not serve them, so the tiles are never read.
var ErrNoLayerCollections error = unprocessable("cacheops: the map does not serve layer collections, so its layer tiles are never read")

// ErrParamsMap is wrapped by an ErrInvalidTarget for a seed naming layers of a
// map with query parameters, whose tiles are never cached.
var ErrParamsMap error = unprocessable("cacheops: the map has query parameters, so its tiles are not cached")

// ErrLayerZooms is wrapped by an ErrInvalidTarget for a seed naming a layer
// that is active at none of the zooms requested, so nothing would be written
// for it.
var ErrLayerZooms error = unprocessable("cacheops: a layer named is active at none of the requested zooms")

type ErrInvalidTarget struct {
	Map string
	Err error
}

func (e ErrInvalidTarget) Error() string {
	// the wrapped error may be one of this package's, prefixed already
	return fmt.Sprintf("cacheops: map (%v): %v", e.Map, strings.TrimPrefix(e.Err.Error(), "cacheops: "))
}

func (e ErrInvalidTarget) Unwrap() error { return e.Err }

type ErrTile struct {
	Op      Op
	MapName string
	Tile    slippy.Tile
	Err     error
}

func (e ErrTile) Error() string {
	verb := "seeding"
	if e.Op == Purge {
		verb = "purging"
	}
	return fmt.Sprintf("error %v map (%v) tile (%+v): %v", verb, e.MapName, e.Tile, e.Err)
}

func (e ErrTile) Unwrap() error { return e.Err }

type mapTile struct {
	target Target
	tile   slippy.Tile
}

// Run works through src until it is exhausted, ctx is cancelled or a tile
// fails. A cancelled run returns what it did and a nil error.
func Run(ctx context.Context, opts Options, src TileSource) (Result, error) {
	var res Result

	if err := opts.validate(); err != nil {
		// release a source that is already producing
		if s, ok := src.(Stopper); ok {
			s.Stop()
		}
		return res, err
	}

	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	work := opts.worker(&res)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	tiles := make(chan mapTile)

	wg.Add(concurrency)
	for range concurrency {
		go func() {
			defer wg.Done()
			for mt := range tiles {
				if ctx.Err() != nil {
					continue
				}
				if err := work(ctx, mt); err != nil {
					errOnce.Do(func() { firstErr = err })
					if ctx.Err() == nil && opts.ContinueOnError {
						atomic.AddInt64(&res.Failed, 1)
						continue
					}
					cancel()
				}
			}
		}()
	}

feed:
	for tile := range src.Channel() {
		for _, target := range opts.Targets {
			if ctx.Err() != nil {
				break feed
			}
			select {
			case tiles <- mapTile{target: target, tile: tile}:
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(tiles)

	if ctx.Err() != nil {
		if s, ok := src.(Stopper); ok {
			s.Stop()
		}
		for range src.Channel() {
		}
	}

	if opts.Draining != nil {
		done := opts.Draining()
		defer done()
	}
	wg.Wait()

	err := firstErr
	if err == nil {
		err = src.Err()
	}
	if errors.Is(err, context.Canceled) {
		err = nil
	}

	return res, err
}

func (opts Options) validate() error {
	if len(opts.Targets) == 0 {
		return ErrNoMaps
	}
	if opts.Grid == nil {
		return atlas.ErrNilGrid
	}
	return ValidateTargets(opts.Op, opts.Targets)
}

// ValidateTargets reports the first target op cannot honour: a layer the map
// does not have, layers of a map that does not serve them, or a seed of layers
// of a map with query parameters.
func ValidateTargets(op Op, targets []Target) error {
	for _, t := range targets {
		if len(t.Layers) == 0 {
			continue
		}

		// a misspelt layer is reported as such, whatever else is wrong with the map
		for _, name := range t.Layers {
			if !slices.ContainsFunc(t.Map.Layers, func(l atlas.Layer) bool { return l.MVTName() == name }) {
				return ErrInvalidTarget{Map: t.Map.Name, Err: fmt.Errorf("no layer named %q", name)}
			}
		}

		if !t.Map.ServesLayerCollections() {
			return ErrInvalidTarget{Map: t.Map.Name, Err: ErrNoLayerCollections}
		}
		if op == Seed && len(t.Map.Params) > 0 {
			return ErrInvalidTarget{Map: t.Map.Name, Err: ErrParamsMap}
		}
	}

	return nil
}

// ValidateZooms reports the first layer a seed names that is active at none of
// zooms. A purge is not checked: one that clears nothing is harmless. Run cannot
// check this itself, as its tile source does not say which zooms it covers.
func ValidateZooms(op Op, targets []Target, zooms []uint) error {
	if op != Seed {
		return nil
	}

	for _, t := range targets {
		for _, name := range t.Layers {
			active := slices.ContainsFunc(zooms, func(z uint) bool {
				return slices.ContainsFunc(t.Map.FilterLayersByZoom(slippy.Zoom(z)).Layers, func(l atlas.Layer) bool { return l.MVTName() == name })
			})
			if !active {
				return ErrInvalidTarget{Map: t.Map.Name, Err: fmt.Errorf("%w: %q", ErrLayerZooms, name)}
			}
		}
	}

	return nil
}

func (opts Options) worker(res *Result) func(context.Context, mapTile) error {
	op := opts.purge
	if opts.Op == Seed {
		op = opts.seed
	}

	return func(ctx context.Context, mt mapTile) error {
		skipped, err := op(opts.intent(ctx), mt)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrTile{Op: opts.Op, MapName: mt.target.Map.Name, Tile: mt.tile, Err: err}
		}

		if skipped {
			atomic.AddInt64(&res.Skipped, 1)
		} else {
			atomic.AddInt64(&res.Done, 1)
		}

		return nil
	}
}

// intent derives the context every non-serving cache caller needs. Writes are
// synchronous so none are lost when the run ends, the run's own reads do not
// promote into the hot tier, writes are bounded to WriteTiers, and under
// Overwrite the tiers not written are purged so they stop serving the old tile.
func (opts Options) intent(ctx context.Context) context.Context {
	ctx = cache.WithSynchronousWrites(ctx)
	ctx = cache.WithoutPromotion(ctx)

	if len(opts.WriteTiers) > 0 {
		ctx = cache.WithWriteTiers(ctx, opts.WriteTiers)
	}
	if opts.Overwrite {
		ctx = cache.WithInvalidateUnwritten(ctx)
	}

	return ctx
}

// seed writes every key of the tile; it is skipped only when, without
// Overwrite, all of them are cached already. A key that fails does not stop the
// others, and the error names the keys that were written all the same.
func (opts Options) seed(ctx context.Context, mt mapTile) (skipped bool, err error) {
	start := time.Now()
	z, x, y := mt.tile.ZXY()
	m := mt.target.Map.FilterLayersByZoom(z)

	// the atlas never caches a map with query parameters, so there is nothing
	// to write
	if len(m.Params) > 0 {
		return true, nil
	}

	var c cache.Interface
	if !opts.Overwrite {
		if c = opts.Atlas.GetCache(); c == nil {
			return false, atlas.ErrMissingCache
		}
	}

	var written []string
	var errs []error
	skipped = true
	for _, layer := range mt.target.Keys(Seed, z) {
		if !opts.Overwrite {
			hit, err := opts.cached(ctx, c, m.Name, layer, mt.tile)
			if err != nil {
				errs = append(errs, fmt.Errorf("%v: %w", keyName(layer), err))
				skipped = false
				continue
			}
			if hit {
				log.Infof("cache seed set to not overwrite existing tiles. skipping map (%v) layer (%v) tile (%v/%v/%v)", m.Name, layer, z, x, y)
				continue
			}
		}

		skipped = false
		if err := opts.Atlas.SeedLayerTile(ctx, m, layer, opts.Grid, uint(z), x, y); err != nil {
			if ctx.Err() != nil {
				return false, err
			}
			errs = append(errs, fmt.Errorf("%v: %w", keyName(layer), err))
			continue
		}
		written = append(written, keyName(layer))
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		if len(written) > 0 {
			// the cache changed all the same, and under Overwrite the other tiers
			// were purged for these keys
			err = fmt.Errorf("wrote %v, but: %w", strings.Join(written, ", "), err)
		}
		return false, err
	}
	if skipped {
		return true, nil
	}

	if opts.CollectGarbage {
		// large tile buffers are not reclaimed promptly otherwise (golang/go#14045)
		runtime.GC()
	}

	if took := time.Since(start); took >= opts.LogThreshold {
		log.Infof("seeding map (%v) tile (%v/%v/%v) took: %dms", m.Name, z, x, y, took.Milliseconds())
	}

	return false, nil
}

// keyName names a key's layer segment for an error: "" is the whole-map tile.
func keyName(layer string) string {
	if layer == "" {
		return "the whole-map tile"
	}
	return fmt.Sprintf("layer (%v)", layer)
}

func (opts Options) cached(ctx context.Context, c cache.Interface, mapName, layer string, tile slippy.Tile) (bool, error) {
	z, x, y := tile.ZXY()
	key, err := cache.NewKey(opts.Grid, mapName, layer, uint(z), x, y)
	if err != nil {
		return false, err
	}

	_, hit, err := c.Get(ctx, &key)
	if err != nil {
		return false, fmt.Errorf("error reading from cache: %w", err)
	}

	return hit, nil
}

func (opts Options) purge(ctx context.Context, mt mapTile) (skipped bool, err error) {
	m := mt.target.Map
	z, x, y := mt.tile.ZXY()
	log.Infof("purging map (%v) tile (%v/%v/%v)", m.Name, z, x, y)

	if len(m.Params) > 0 {
		return true, nil
	}

	c := opts.Atlas.GetCache()
	if c == nil {
		return false, atlas.ErrMissingCache
	}

	for _, layer := range mt.target.Keys(Purge, z) {
		key, err := cache.NewKey(opts.Grid, m.Name, layer, uint(z), x, y)
		if err != nil {
			return false, err
		}
		if err := c.Purge(ctx, &key); err != nil {
			return false, err
		}
	}

	return false, nil
}

// Keys names the layer segment of every cache key op touches for one tile of
// t at zoom z: the whole-map tile (""), and the target's layers active at z,
// each once. A target naming no layers is every layer for a purge, none for a
// seed.
func (t Target) Keys(op Op, z slippy.Zoom) []string {
	keys := []string{""}
	if op == Seed && len(t.Layers) == 0 {
		return keys
	}

	for _, l := range t.Map.FilterLayersByZoom(z).Layers {
		name := l.MVTName()
		// several layers may share a name, one per zoom band, and share its key
		if slices.Contains(keys, name) {
			continue
		}
		if len(t.Layers) == 0 || slices.Contains(t.Layers, name) {
			keys = append(keys, name)
		}
	}

	return keys
}
