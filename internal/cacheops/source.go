package cacheops

import (
	"context"
	"sync"

	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom/slippy"
)

type source struct {
	ch   chan slippy.Tile
	stop context.CancelFunc
	mu   sync.Mutex
	err  error
}

func (s *source) Stop() { s.stop() }

func (s *source) Channel() <-chan slippy.Tile { return s.ch }

func (s *source) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// BoundsSource yields, once each, the tiles of grid at zooms that overlap any
// of the lng/lat boxes. Coordinates outside the grid's extent are clipped.
func BoundsSource(ctx context.Context, grid *tms.TileMatrixSet, boxes []tms.BoundingBox, zooms []uint) TileSource {
	ctx, stop := context.WithCancel(ctx)
	s := &source{ch: make(chan slippy.Tile), stop: stop}

	go func() {
		defer close(s.ch)
		defer stop()

		for _, z := range zooms {
			seen := map[tms.Tile]bool{}

			for _, b := range boxes {
				tiles, err := grid.Tiles(b.Left, b.Bottom, b.Right, b.Top, []int{int(z)}, true)
				if err != nil {
					s.mu.Lock()
					s.err = err
					s.mu.Unlock()
					return
				}

				for _, t := range tiles {
					if seen[t] {
						continue
					}
					seen[t] = true

					select {
					case s.ch <- slippy.Tile{Z: slippy.Zoom(t.Z), X: uint(t.X), Y: uint(t.Y)}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return s
}
