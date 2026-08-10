package cache

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/tegola"
	"github.com/go-spatial/tegola/atlas"
	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/internal/log"
)

type seedPurgeWorkerTileError struct {
	Purge bool
	Tile  slippy.Tile
	Err   error
}

func (s seedPurgeWorkerTileError) Error() string {
	cmd := "seeding"
	if s.Purge {
		cmd = "purging"
	}
	return fmt.Sprintf("error %v tile (%+v): %v", cmd, s.Tile, s.Err)
}

// withCacheIntent derives the context every non-serving cache caller needs
// before it reaches the cache seam. All four caller-intent values are set here.
//
// **WithSynchronousWrites** is what stops the seed losing writes. On the serve
// path a cache write is handed to a detached pool and the handler returns; here
// there is no response to protect, and the process exits as soon as the last
// tile is generated — so a detached write would be dropped or abandoned at exit
// and `tegola cache seed` would exit 0 having populated an unknown fraction of
// what it reported. It also restores the error path strict Set exists for:
// written inline, a joined tier error reaches the worker, which marks the tile
// failed.
//
// **WithoutPromotion** stops the seed's own reads warming the hot tier.
// `seed` without --overwrite reads every tile through the cache before deciding
// whether to generate it, so with promotion on, a run over a large area would
// promote every durable-tier tile into the hot tier, in seed order, at seeding
// throughput — overwriting the live working set with cold tiles. Its scope is
// the seed's own reads: a concurrent *serve* request promoting during a seed
// run belongs to a different caller and is not suppressed by this.
//
// **WithWriteTiers** bounds what the run may write. It also bounds promotion,
// so --cache-tiers means one thing and means it completely.
//
// **WithInvalidateUnwritten** makes the chain purge the tiers it did not write,
// after writing the ones it did. Only under --overwrite: without it seed skips
// existing tiles, so there is nothing to invalidate. With it the operator is
// stating the content changed, which is exactly when a stale hot tier must stop
// serving — and with a durable-only default, a re-seed would otherwise leave
// the hot tier serving pre-update tiles until TTL expiry, so the command
// documented as the invalidation mechanism would not invalidate what users are
// served. Harmless when every tier is a target: there is then nothing left to
// purge.
func withCacheIntent(worker func(context.Context, MapTile) error) func(context.Context, MapTile) error {
	return func(ctx context.Context, mt MapTile) error {
		ctx = cache.WithSynchronousWrites(ctx)
		ctx = cache.WithoutPromotion(ctx)

		if len(seedWriteTiers) > 0 {
			ctx = cache.WithWriteTiers(ctx, seedWriteTiers)
		}
		if cacheOverwrite {
			ctx = cache.WithInvalidateUnwritten(ctx)
		}

		return worker(ctx, mt)
	}
}

func seedWorker(overwrite bool, logThresholdMs int64) func(ctx context.Context, mt MapTile) error {
	return func(ctx context.Context, mt MapTile) error {
		// track how long the tile generation is taking
		t := time.Now()

		//	lookup the Map
		m, err := atlas.GetMap(mt.MapName)
		if err != nil {
			return seedPurgeWorkerTileError{
				Tile: mt.Tile,
				Err:  err,
			}
		}

		z, x, y := mt.Tile.ZXY()

		//	filter down the layers we need for this zoom
		m = m.FilterLayersByZoom(z)

		//	check if overwriting the cache is not ok
		if !overwrite {
			//	lookup our cache
			c := atlas.GetCache()
			if c == nil {
				return fmt.Errorf("error fetching cache: %v", err)
			}

			//	cache key
			key := cache.Key{
				MapName: mt.MapName,
				Z:       uint(z),
				X:       x,
				Y:       y,
			}

			//	read the tile from the cache
			_, hit, err := c.Get(ctx, &key)
			if err != nil {
				return fmt.Errorf("error reading from cache: %v", err)
			}
			//	if we have a cache hit, then skip processing this tile
			if hit {
				log.Infof("cache seed set to not overwrite existing tiles. skipping map (%v) tile (%v/%v/%v)", mt.MapName, z, x, y)
				return nil
			}
		}

		//	seed the tile
		if err = atlas.SeedMapTile(ctx, m, uint(z), x, y); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return seedPurgeWorkerTileError{
				Tile: mt.Tile,
				Err:  err,
			}
		}

		//	TODO: this is a hack to get around large arrays not being garbage collected
		//	https://github.com/golang/go/issues/14045 - should be addressed in Go 1.11
		runtime.GC()

		durationMs := time.Now().Sub(t).Nanoseconds() / 1000000
		if durationMs >= logThresholdMs {
			log.Infof("seeding map (%v) tile (%v/%v/%v) took: %dms", mt.MapName, z, x, y, durationMs)
		}

		return nil
	}

}

func purgeWorker(ctx context.Context, mt MapTile) error {

	z, x, y := mt.Tile.ZXY()

	log.Infof("purging map (%v) tile (%v/%v/%v)", mt.MapName, z, x, y)

	//	lookup the Map
	m, err := atlas.GetMap(mt.MapName)
	if err != nil {
		return seedPurgeWorkerTileError{
			Purge: true,
			Tile:  mt.Tile,
			Err:   err,
		}
	}

	//	purge the tile
	ttile := tegola.TileFromSlippyTile(mt.Tile)

	if err = atlas.PurgeMapTile(ctx, m, ttile); err != nil {
		return seedPurgeWorkerTileError{
			Purge: true,
			Tile:  mt.Tile,
			Err:   err,
		}
	}

	return nil
}
