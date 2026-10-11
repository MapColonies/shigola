// Package cacheops seeds and purges tiles in the Tile Cache. It is the engine
// behind `shigola cache seed|purge` and the Cache Admin API.
package cacheops

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MapColonies/shigola"
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

type Options struct {
	Op Op
	// Atlas holds the cache the run works on. Nil means the default atlas.
	Atlas       *atlas.Atlas
	Maps        []atlas.Map
	Grid        *tms.TileMatrixSet
	Concurrency int
	// Overwrite re-seeds tiles that are already cached. Seed only.
	Overwrite bool
	// WriteTiers restricts the tiers of a layered cache the run writes. Nil means all.
	WriteTiers []string
	// LogThreshold logs seeded tiles that took at least this long.
	LogThreshold time.Duration
	// Draining, if set, is called once the source is exhausted and the run is
	// waiting on its workers; the func it returns is called when they finish.
	Draining func() (done func())
}

// Result counts map tiles, so one tile across two maps counts twice.
type Result struct {
	Done    int64
	Skipped int64
}

var ErrNoMaps = errors.New("cacheops: no maps to work on")

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
	m    atlas.Map
	tile slippy.Tile
}

// Run works through src until it is exhausted, ctx is cancelled or a tile
// fails. A cancelled run returns what it did and a nil error.
func Run(ctx context.Context, opts Options, src TileSource) (Result, error) {
	var res Result

	if len(opts.Maps) == 0 {
		return res, ErrNoMaps
	}
	if opts.Grid == nil {
		return res, atlas.ErrNilGrid
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
					cancel()
				}
			}
		}()
	}

feed:
	for tile := range src.Channel() {
		for _, m := range opts.Maps {
			if ctx.Err() != nil {
				break feed
			}
			select {
			case tiles <- mapTile{m: m, tile: tile}:
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

func (opts Options) worker(res *Result) func(context.Context, mapTile) error {
	op := opts.purge
	if opts.Op == Seed {
		op = opts.seed
	}

	return func(ctx context.Context, mt mapTile) error {
		skipped, err := op(opts.intent(ctx), mt)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return ErrTile{Op: opts.Op, MapName: mt.m.Name, Tile: mt.tile, Err: err}
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

func (opts Options) seed(ctx context.Context, mt mapTile) (skipped bool, err error) {
	start := time.Now()
	z, x, y := mt.tile.ZXY()

	if !opts.Overwrite {
		c := opts.Atlas.GetCache()
		if c == nil {
			return false, atlas.ErrMissingCache
		}

		key, err := cache.NewKey(opts.Grid, mt.m.Name, "", uint(z), x, y)
		if err != nil {
			return false, err
		}

		_, hit, err := c.Get(ctx, &key)
		if err != nil {
			return false, fmt.Errorf("error reading from cache: %w", err)
		}
		if hit {
			log.Infof("cache seed set to not overwrite existing tiles. skipping map (%v) tile (%v/%v/%v)", mt.m.Name, z, x, y)
			return true, nil
		}
	}

	m := mt.m.FilterLayersByZoom(z)
	if err := opts.Atlas.SeedMapTile(ctx, m, opts.Grid, uint(z), x, y); err != nil {
		return false, err
	}

	// large tile buffers are not reclaimed promptly otherwise (golang/go#14045)
	runtime.GC()

	if took := time.Since(start); took >= opts.LogThreshold {
		log.Infof("seeding map (%v) tile (%v/%v/%v) took: %dms", mt.m.Name, z, x, y, took.Milliseconds())
	}

	return false, nil
}

func (opts Options) purge(ctx context.Context, mt mapTile) (skipped bool, err error) {
	z, x, y := mt.tile.ZXY()
	log.Infof("purging map (%v) tile (%v/%v/%v)", mt.m.Name, z, x, y)

	return false, opts.Atlas.PurgeMapTile(ctx, mt.m, opts.Grid, shigola.TileFromSlippyTile(mt.tile))
}
