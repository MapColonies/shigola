package atlas_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/go-spatial/geom/slippy"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/tms"
)

// keyRecorder is a cache that stores nothing and remembers the key of every
// call it was handed.
type keyRecorder struct {
	sync.Mutex
	set    []string
	purged []string
}

func (k *keyRecorder) Get(context.Context, *cache.Key) ([]byte, bool, error) {
	return nil, false, nil
}

func (k *keyRecorder) Set(_ context.Context, key *cache.Key, _ []byte) error {
	k.Lock()
	defer k.Unlock()
	k.set = append(k.set, key.String())

	return nil
}

func (k *keyRecorder) Purge(_ context.Context, key *cache.Key) error {
	k.Lock()
	defer k.Unlock()
	k.purged = append(k.purged, key.String())

	return nil
}

// TestNilGridIsRejected pins that all three seams that cut a tile refuse a nil
// grid, and refuse it under the same name.
//
// One name matters because the mistake is one mistake. Encode reaches
// ErrNilGrid on its own; SeedMapTile and PurgeMapTile guard before they call
// out, so a purge does not report cache.ErrNilGrid while a seed reports this
// one. A caller cannot then be right about which sentinel to test for only for
// the seam it happened to use.
func TestNilGridIsRejected(t *testing.T) {
	m := atlas.NewWebMercatorMap("osm")

	a := &atlas.Atlas{}
	a.SetCache(&keyRecorder{})
	a.AddMap(m)

	ctx := context.Background()

	if _, err := m.Encode(ctx, nil, slippy.Tile{Z: 3, X: 5, Y: 2}, nil); !errors.Is(err, atlas.ErrNilGrid) {
		t.Errorf("Encode(nil grid) = %v, want %v", err, atlas.ErrNilGrid)
	}

	if err := a.SeedMapTile(ctx, m, nil, 3, 5, 2); !errors.Is(err, atlas.ErrNilGrid) {
		t.Errorf("SeedMapTile(nil grid) = %v, want %v", err, atlas.ErrNilGrid)
	}

	if err := a.PurgeMapTile(ctx, m, nil, &shigola.Tile{Z: 3, X: 5, Y: 2}); !errors.Is(err, atlas.ErrNilGrid) {
		t.Errorf("PurgeMapTile(nil grid) = %v, want %v", err, atlas.ErrNilGrid)
	}
}

// TestSeedAndPurgeKeys pins, as literal strings, the cache keys the seed and
// purge seams write and remove.
//
// Those keys are the whole contract between `shigola cache seed` and the tile
// handler: a tile filed under a key the handler does not build is a tile
// nothing serves, and neither side reports anything wrong. The literals are
// deliberately not recomputed from cache.NewKey — a test that builds its
// expectation the way the code does cannot notice the two moving together.
func TestSeedAndPurgeKeys(t *testing.T) {
	type tcase struct {
		gridID string
		want   string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			grid, err := tms.Get(tc.gridID)
			if err != nil {
				t.Fatalf("Get(%v): %v", tc.gridID, err)
			}

			// The map lists WebMercatorQuad and is seeded in both schemes:
			// neither seam checks SupportsTileGrid, deliberately. Whether a
			// grid is one the map offers is a question about a request or a
			// run, answered where those are validated -- the OGC handler's
			// collectionGrid and the CLI's resolveSeedPurgeGrid -- and not
			// twice.
			m := atlas.NewWebMercatorMap("osm")

			rec := &keyRecorder{}
			a := &atlas.Atlas{}
			a.SetCache(rec)
			a.AddMap(m)

			ctx := context.Background()

			if err := a.SeedMapTile(ctx, m, grid, 3, 5, 2); err != nil {
				t.Fatalf("SeedMapTile: %v", err)
			}

			if err := a.PurgeMapTile(ctx, m, grid, &shigola.Tile{Z: 3, X: 5, Y: 2}); err != nil {
				t.Fatalf("PurgeMapTile: %v", err)
			}

			if got := rec.set; len(got) != 1 || got[0] != tc.want {
				t.Errorf("seeded keys = %q, want [%q]", got, tc.want)
			}

			if got := rec.purged; len(got) != 1 || got[0] != tc.want {
				t.Errorf("purged keys = %q, want [%q]", got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"web mercator quad": {gridID: tms.WebMercatorQuad, want: "WebMercatorQuad/osm/3/5/2"},
		"world crs84 quad":  {gridID: tms.WorldCRS84Quad, want: "WorldCRS84Quad/osm/3/5/2"},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
