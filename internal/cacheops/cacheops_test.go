package cacheops_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/multi"
	"github.com/MapColonies/shigola/internal/cacheops"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/provider/test"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom/slippy"
)

func webMercator(t *testing.T) *tms.TileMatrixSet {
	t.Helper()

	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		t.Fatalf("tms.Get: %v", err)
	}

	return grid
}

func key(t *testing.T, grid *tms.TileMatrixSet, mapName string, z, x, y uint) *cache.Key {
	t.Helper()

	k, err := cache.NewKey(grid, mapName, "", z, x, y)
	if err != nil {
		t.Fatalf("cache.NewKey: %v", err)
	}

	return &k
}

func tiles(ts ...slippy.Tile) cacheops.TileSource {
	ch := make(chan slippy.Tile, len(ts))
	for _, t := range ts {
		ch <- t
	}
	close(ch)

	return cacheops.TileChan(ch)
}

func TestPurgeRemovesEveryListedTile(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)
	m := atlas.NewWebMercatorMap("osm")
	a.AddMap(m)

	tier.Seed(key(t, grid, "osm", 1, 0, 0), []byte("a"))
	tier.Seed(key(t, grid, "osm", 1, 1, 1), []byte("b"))
	tier.Seed(key(t, grid, "osm", 1, 0, 1), []byte("kept"))

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:          cacheops.Purge,
		Atlas:       a,
		Targets:     []cacheops.Target{{Map: m}},
		Grid:        grid,
		Concurrency: 2,
	}, tiles(slippy.Tile{Z: 1, X: 0, Y: 0}, slippy.Tile{Z: 1, X: 1, Y: 1}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Done != 2 {
		t.Errorf("Done: got %d, expected 2", res.Done)
	}
	for _, k := range []*cache.Key{key(t, grid, "osm", 1, 0, 0), key(t, grid, "osm", 1, 1, 1)} {
		if _, ok := tier.Value(k); ok {
			t.Errorf("%v survived the purge", k)
		}
	}
	if _, ok := tier.Value(key(t, grid, "osm", 1, 0, 1)); !ok {
		t.Error("a tile outside the list was purged")
	}
}

func renderable(name string) atlas.Map {
	m := atlas.NewWebMercatorMap(name)
	m.SetMVTProvider("test", &test.TileProvider{MVTTile: []byte("fresh")})

	return m
}

func TestSeedWithoutOverwriteSkipsCachedTiles(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)
	m := renderable("osm")

	cached := key(t, grid, "osm", 2, 1, 1)
	tier.Seed(cached, []byte("old"))

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Seed,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: m}},
		Grid:    grid,
	}, tiles(slippy.Tile{Z: 2, X: 1, Y: 1}, slippy.Tile{Z: 2, X: 2, Y: 2}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Done != 1 || res.Skipped != 1 {
		t.Errorf("result: got %+v, expected 1 done and 1 skipped", res)
	}
	if v, _ := tier.Value(cached); string(v) != "old" {
		t.Errorf("cached tile: got %q, expected it left as old", v)
	}
	if _, ok := tier.Value(key(t, grid, "osm", 2, 2, 2)); !ok {
		t.Error("the missing tile was not seeded")
	}
}

func twoTiers(t *testing.T) (hot, durable *faketier.Tier, rec *faketier.Recorder, chain cache.Interface) {
	t.Helper()

	rec = faketier.NewRecorder()
	hot = faketier.NewWithRecorder("hot", rec)
	durable = faketier.NewWithRecorder("durable", rec)

	chain, err := multi.NewChain([]cache.NamedTier{
		{Name: "hot", Cache: hot},
		{Name: "durable", Cache: durable},
	}, true, nil)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	return hot, durable, rec, chain
}

func TestSeedOverwriteWritesNamedTiersThenPurgesTheRest(t *testing.T) {
	grid := webMercator(t)
	hot, durable, rec, chain := twoTiers(t)

	a := &atlas.Atlas{}
	a.SetCache(chain)

	k := key(t, grid, "osm", 3, 1, 1)
	hot.Seed(k, []byte("stale"))
	rec.Reset()

	_, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:         cacheops.Seed,
		Atlas:      a,
		Targets:    []cacheops.Target{{Map: renderable("osm")}},
		Grid:       grid,
		Overwrite:  true,
		WriteTiers: []string{"durable"},
	}, tiles(slippy.Tile{Z: 3, X: 1, Y: 1}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, ok := durable.Value(k); !ok {
		t.Error("the durable tier was not written")
	}
	if _, ok := hot.Value(k); ok {
		t.Error("the stale hot-tier tile survived an overwrite")
	}

	// write before purge: the reverse leaves a window for a read to promote the old tile back
	expected := []string{"durable:set", "hot:purge"}
	if trace := rec.Trace(); !reflect.DeepEqual(trace, expected) {
		t.Errorf("trace: got %v, expected %v", trace, expected)
	}
}

func TestSeedReadsDoNotPromoteIntoTheHotTier(t *testing.T) {
	grid := webMercator(t)
	hot, durable, _, chain := twoTiers(t)

	a := &atlas.Atlas{}
	a.SetCache(chain)

	k := key(t, grid, "osm", 3, 2, 2)
	durable.Seed(k, []byte("cached"))

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Seed,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: renderable("osm")}},
		Grid:    grid,
	}, tiles(slippy.Tile{Z: 3, X: 2, Y: 2}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Skipped != 1 {
		t.Errorf("result: got %+v, expected the cached tile skipped", res)
	}
	if _, ok := hot.Value(k); ok {
		t.Error("the seed's read promoted into the hot tier")
	}
}

func TestCancelledRunStopsAndReportsWhatItDid(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan slippy.Tile)
	go func() {
		defer close(ch)
		for x := uint(0); x < 3; x++ {
			ch <- slippy.Tile{Z: 4, X: x, Y: 0}
		}
		cancel()
		for x := uint(3); x < 16; x++ {
			select {
			case ch <- slippy.Tile{Z: 4, X: x, Y: 0}:
			case <-ctx.Done():
				return
			}
		}
	}()

	res, err := cacheops.Run(ctx, cacheops.Options{
		Op:      cacheops.Purge,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: atlas.NewWebMercatorMap("osm")}},
		Grid:    grid,
	}, cacheops.TileChan(ch))
	if err != nil {
		t.Fatalf("Run: got %v, expected a cancelled run to return nil", err)
	}

	if res.Done < 1 || res.Done > 3 {
		t.Errorf("Done: got %d, expected the tiles before the cancel and none after", res.Done)
	}
	if n := tier.Count(faketier.OpPurge); int64(n) != res.Done {
		t.Errorf("purges: got %d, expected Done (%d)", n, res.Done)
	}
}

func TestFailingTileStopsTheRun(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")
	boom := errors.New("boom")
	tier.FailOn(faketier.OpPurge, boom)

	a := &atlas.Atlas{}
	a.SetCache(tier)

	_, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Purge,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: atlas.NewWebMercatorMap("osm")}},
		Grid:    grid,
	}, tiles(slippy.Tile{Z: 1, X: 0, Y: 0}, slippy.Tile{Z: 1, X: 1, Y: 0}))

	var te cacheops.ErrTile
	if !errors.As(err, &te) {
		t.Fatalf("err: got %v, expected an ErrTile", err)
	}
	if te.MapName != "osm" || !errors.Is(err, boom) {
		t.Errorf("ErrTile: got %+v, expected map osm wrapping the tier's error", te)
	}
	if n := tier.Count(faketier.OpPurge); n != 1 {
		t.Errorf("purges: got %d, expected the run to stop after the first failure", n)
	}
}

type stoppable struct {
	ch      chan slippy.Tile
	stopped chan struct{}
}

func (s *stoppable) Channel() <-chan slippy.Tile { return s.ch }
func (s *stoppable) Err() error                  { return nil }
func (s *stoppable) Stop()                       { close(s.stopped) }

func TestARunEndingEarlyStopsItsSource(t *testing.T) {
	tier := faketier.New("solo")
	tier.FailOn(faketier.OpPurge, errors.New("boom"))
	a := &atlas.Atlas{}
	a.SetCache(tier)

	src := &stoppable{ch: make(chan slippy.Tile), stopped: make(chan struct{})}
	go func() {
		defer close(src.ch)
		for x := uint(0); ; x++ {
			select {
			case src.ch <- slippy.Tile{Z: 20, X: x}:
			case <-src.stopped:
				return
			}
		}
	}()

	if _, err := cacheops.Run(context.Background(), cacheops.Options{
		Op: cacheops.Purge, Atlas: a, Targets: []cacheops.Target{{Map: atlas.NewWebMercatorMap("osm")}}, Grid: webMercator(t),
	}, src); err == nil {
		t.Fatal("Run: got nil, expected the tier's error")
	}

	select {
	case <-src.stopped:
	default:
		t.Error("the run failed but never stopped its source, so it would list every remaining tile")
	}
}

func TestConcurrentRunsDoNotShareSettings(t *testing.T) {
	grid := webMercator(t)

	run := func(op cacheops.Op, tier *faketier.Tier, overwrite bool) func() (cacheops.Result, error) {
		a := &atlas.Atlas{}
		a.SetCache(tier)

		return func() (cacheops.Result, error) {
			return cacheops.Run(context.Background(), cacheops.Options{
				Op: op, Atlas: a, Targets: []cacheops.Target{{Map: renderable("osm")}}, Grid: grid,
				Overwrite: overwrite, Concurrency: 4,
			}, tiles(slippy.Tile{Z: 5, X: 1, Y: 1}, slippy.Tile{Z: 5, X: 2, Y: 2}))
		}
	}

	seedTier, purgeTier := faketier.New("a"), faketier.New("b")
	k := key(t, grid, "osm", 5, 1, 1)
	seedTier.Seed(k, []byte("old"))
	purgeTier.Seed(k, []byte("old"))

	seed, purge := run(cacheops.Seed, seedTier, true), run(cacheops.Purge, purgeTier, false)

	var wg sync.WaitGroup
	var seedRes, purgeRes cacheops.Result
	var seedErr, purgeErr error
	wg.Add(2)
	go func() { defer wg.Done(); seedRes, seedErr = seed() }()
	go func() { defer wg.Done(); purgeRes, purgeErr = purge() }()
	wg.Wait()

	if seedErr != nil || purgeErr != nil {
		t.Fatalf("errors: seed %v, purge %v", seedErr, purgeErr)
	}
	if seedRes.Done != 2 || purgeRes.Done != 2 {
		t.Errorf("results: seed %+v, purge %+v, expected 2 done each", seedRes, purgeRes)
	}
	if v, _ := seedTier.Value(k); string(v) == "old" {
		t.Errorf("seed tier: got %q, expected the overwrite to replace it", v)
	}
	if _, ok := purgeTier.Value(k); ok {
		t.Error("purge tier: the tile survived")
	}
	if purgeTier.Count(faketier.OpSet) != 0 || seedTier.Count(faketier.OpPurge) != 0 {
		t.Error("one run's operation leaked into the other's cache")
	}
}

func layeredMap(name string) atlas.Map {
	m := atlas.NewWebMercatorMap(name)
	m.Layers = []atlas.Layer{
		{Name: "roads", MinZoom: 0, MaxZoom: 20},
		{Name: "buildings", MinZoom: 0, MaxZoom: 20},
		{Name: "pois", MinZoom: 14, MaxZoom: 20},
	}

	return m
}

func layerKey(t *testing.T, grid *tms.TileMatrixSet, mapName, layer string, z, x, y uint) *cache.Key {
	t.Helper()

	k, err := cache.NewKey(grid, mapName, layer, z, x, y)
	if err != nil {
		t.Fatalf("cache.NewKey: %v", err)
	}

	return &k
}

func TestPurgeOfAWholeMapClearsEveryLayerKey(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)

	for _, layer := range []string{"", "roads", "buildings"} {
		tier.Seed(layerKey(t, grid, "osm", layer, 6, 1, 1), []byte("old"))
	}
	neighbour := layerKey(t, grid, "osm", "roads", 6, 2, 2)
	tier.Seed(neighbour, []byte("kept"))

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Purge,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: layeredMap("osm")}},
		Grid:    grid,
	}, tiles(slippy.Tile{Z: 6, X: 1, Y: 1}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Done != 1 {
		t.Errorf("Done: got %d, expected 1 map tile", res.Done)
	}
	for _, layer := range []string{"", "roads", "buildings"} {
		if _, ok := tier.Value(layerKey(t, grid, "osm", layer, 6, 1, 1)); ok {
			t.Errorf("layer %q survived the purge", layer)
		}
	}
	if _, ok := tier.Value(neighbour); !ok {
		t.Error("a tile outside the list was purged")
	}
	// pois starts at z14, so the server never caches it at z6
	if n := tier.Count(faketier.OpPurge); n != 3 {
		t.Errorf("purges: got %d, expected the map key and the two layers active at z6", n)
	}
}

func TestPurgeOfOneLayerAlsoClearsTheWholeMapTile(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)

	for _, layer := range []string{"", "roads", "buildings"} {
		tier.Seed(layerKey(t, grid, "osm", layer, 6, 1, 1), []byte("old"))
	}

	_, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Purge,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: layeredMap("osm"), Layers: []string{"roads"}}},
		Grid:    grid,
	}, tiles(slippy.Tile{Z: 6, X: 1, Y: 1}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, layer := range []string{"", "roads"} {
		if _, ok := tier.Value(layerKey(t, grid, "osm", layer, 6, 1, 1)); ok {
			t.Errorf("layer %q survived the purge", layer)
		}
	}
	if _, ok := tier.Value(layerKey(t, grid, "osm", "buildings", 6, 1, 1)); !ok {
		t.Error("the sibling layer was purged")
	}
}

// layerEcho encodes a tile as the names of the layers it was asked for, so a
// test can tell which layers went into a cached tile.
type layerEcho struct{ test.TileProvider }

func (layerEcho) MVTForLayers(_ context.Context, _ provider.Tile, _ provider.Params, layers []provider.Layer) ([]byte, error) {
	names := make([]string, len(layers))
	for i, l := range layers {
		names[i] = l.MVTName
	}

	return []byte(strings.Join(names, ",")), nil
}

func echoingMap(name string) atlas.Map {
	m := layeredMap(name)
	m.SetMVTProvider("echo", &layerEcho{})

	return m
}

func noLayerCollections(m atlas.Map) atlas.Map {
	off := false
	m.ServeLayerCollections = &off

	return m
}

// layersIn is the layer names a cached tile was encoded with.
func layersIn(t *testing.T, tier *faketier.Tier, k *cache.Key) (string, bool) {
	t.Helper()

	v, ok := tier.Value(k)
	if !ok {
		return "", false
	}
	r, err := gzip.NewReader(bytes.NewReader(v))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}

	return string(b), true
}

func TestSeedOfOneLayerWritesItAndTheWholeMapTile(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Seed,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: echoingMap("osm"), Layers: []string{"roads", "pois"}}},
		Grid:    grid,
	}, tiles(slippy.Tile{Z: 6, X: 1, Y: 1}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Done != 1 {
		t.Errorf("Done: got %d, expected 1 map tile", res.Done)
	}
	if got, _ := layersIn(t, tier, layerKey(t, grid, "osm", "", 6, 1, 1)); got != "roads,buildings" {
		t.Errorf("whole-map tile: got layers %q, expected every layer active at z6", got)
	}
	if got, _ := layersIn(t, tier, layerKey(t, grid, "osm", "roads", 6, 1, 1)); got != "roads" {
		t.Errorf("roads tile: got layers %q, expected roads alone", got)
	}
	if _, ok := tier.Value(layerKey(t, grid, "osm", "buildings", 6, 1, 1)); ok {
		t.Error("a layer that was not named was seeded")
	}
	// pois starts at z14, so the server never caches it at z6
	if n := tier.Count(faketier.OpSet); n != 2 {
		t.Errorf("writes: got %d, expected the map key and roads", n)
	}
}

func TestSeedOfALayerWithoutOverwriteFillsOnlyItsMissingKeys(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)

	whole := layerKey(t, grid, "osm", "", 6, 1, 1)
	tier.Seed(whole, []byte("old"))
	for _, layer := range []string{"", "roads"} {
		tier.Seed(layerKey(t, grid, "osm", layer, 6, 2, 2), []byte("old"))
	}

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Seed,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: echoingMap("osm"), Layers: []string{"roads"}}},
		Grid:    grid,
	}, tiles(slippy.Tile{Z: 6, X: 1, Y: 1}, slippy.Tile{Z: 6, X: 2, Y: 2}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Done != 1 || res.Skipped != 1 {
		t.Errorf("result: got %+v, expected the fully cached tile skipped and the other done", res)
	}
	if v, _ := tier.Value(whole); string(v) != "old" {
		t.Errorf("whole-map tile: got %q, expected it left as old", v)
	}
	if got, _ := layersIn(t, tier, layerKey(t, grid, "osm", "roads", 6, 1, 1)); got != "roads" {
		t.Errorf("roads tile: got layers %q, expected it seeded", got)
	}
}

func TestRunRejectsLayersItCannotHonour(t *testing.T) {
	type tcase struct {
		opts cacheops.Options
	}

	grid := webMercator(t)

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			tier := faketier.New("solo")
			a := &atlas.Atlas{}
			a.SetCache(tier)
			tc.opts.Atlas, tc.opts.Grid = a, grid

			_, err := cacheops.Run(context.Background(), tc.opts, tiles(slippy.Tile{Z: 6, X: 1, Y: 1}))

			var invalid cacheops.ErrInvalidTarget
			if !errors.As(err, &invalid) {
				t.Fatalf("err: got %v, expected an ErrInvalidTarget", err)
			}
			if len(tier.Calls()) != 0 {
				t.Errorf("the cache was touched: %v", tier.Calls())
			}
		}
	}

	tests := map[string]tcase{
		"purge naming an unknown layer": {opts: cacheops.Options{
			Op: cacheops.Purge, Targets: []cacheops.Target{{Map: layeredMap("osm"), Layers: []string{"rivers"}}},
		}},
		"seed naming an unknown layer": {opts: cacheops.Options{
			Op: cacheops.Seed, Targets: []cacheops.Target{{Map: layeredMap("osm"), Layers: []string{"rivers"}}},
		}},
		"seed naming a layer of a map serving no layer collections": {opts: cacheops.Options{
			Op: cacheops.Seed, Targets: []cacheops.Target{{Map: noLayerCollections(layeredMap("osm")), Layers: []string{"roads"}}},
		}},
		"purge naming a layer of a map serving no layer collections": {opts: cacheops.Options{
			Op: cacheops.Purge, Targets: []cacheops.Target{{Map: noLayerCollections(layeredMap("osm")), Layers: []string{"roads"}}},
		}},
		"seed naming a layer of a map with query parameters": {opts: cacheops.Options{
			Op: cacheops.Seed, Targets: []cacheops.Target{{Map: withParams(layeredMap("osm")), Layers: []string{"roads"}}},
		}},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestContinueOnErrorAttemptsEveryTileAndCountsFailures(t *testing.T) {
	tier := faketier.New("solo")
	tier.FailOn(faketier.OpPurge, errors.New("boom"))
	a := &atlas.Atlas{}
	a.SetCache(tier)

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:              cacheops.Purge,
		Atlas:           a,
		Targets:         []cacheops.Target{{Map: atlas.NewWebMercatorMap("osm")}},
		Grid:            webMercator(t),
		ContinueOnError: true,
	}, tiles(slippy.Tile{Z: 1, X: 0, Y: 0}, slippy.Tile{Z: 1, X: 1, Y: 0}, slippy.Tile{Z: 1, X: 0, Y: 1}))

	var te cacheops.ErrTile
	if !errors.As(err, &te) {
		t.Fatalf("err: got %v, expected the first ErrTile", err)
	}
	if res.Failed != 3 || res.Done != 0 {
		t.Errorf("result: got %+v, expected 3 failed", res)
	}
	if n := tier.Count(faketier.OpPurge); n != 3 {
		t.Errorf("purges: got %d, expected every tile attempted", n)
	}
}

func withParams(m atlas.Map) atlas.Map {
	m.Params = []provider.QueryParameter{{Name: "lang", Token: "!LANG!", Type: "string"}}

	return m
}

func TestValidateTargetsReportsAnUnknownLayerFirst(t *testing.T) {
	err := cacheops.ValidateTargets(cacheops.Seed, []cacheops.Target{
		{Map: noLayerCollections(layeredMap("flat")), Layers: []string{"rivers"}},
	})

	if errors.Is(err, cacheops.ErrNoLayerCollections) || !strings.Contains(fmt.Sprint(err), `no layer named "rivers"`) {
		t.Errorf("err: got %v, expected the unknown layer reported", err)
	}
}

func TestValidateZooms(t *testing.T) {
	type tcase struct {
		op    cacheops.Op
		zooms []uint
		ok    bool
	}

	targets := []cacheops.Target{{Map: layeredMap("osm"), Layers: []string{"pois"}}}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			err := cacheops.ValidateZooms(tc.op, targets, tc.zooms)
			if tc.ok && err != nil {
				t.Errorf("err: got %v, expected none", err)
			}
			if !tc.ok && !errors.Is(err, cacheops.ErrLayerZooms) {
				t.Errorf("err: got %v, expected ErrLayerZooms", err)
			}
		}
	}

	tests := map[string]tcase{
		"seed below the layer's zooms":   {op: cacheops.Seed, zooms: []uint{1, 2, 3}},
		"seed reaching the layer's zoom": {op: cacheops.Seed, zooms: []uint{13, 14}, ok: true},
		"purge is not checked":           {op: cacheops.Purge, zooms: []uint{1}, ok: true},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestKeysNameALayerSharedByZoomBandsOnce(t *testing.T) {
	m := layeredMap("osm")
	m.Layers = append(m.Layers, atlas.Layer{Name: "roads", MinZoom: 0, MaxZoom: 20})

	for name, tgt := range map[string]cacheops.Target{
		"named":     {Map: m, Layers: []string{"roads"}},
		"every one": {Map: m},
	} {
		keys := tgt.Keys(cacheops.Purge, 6)
		if n := strings.Count(strings.Join(keys, ","), "roads"); n != 1 {
			t.Errorf("%v: got keys %q, expected roads once", name, keys)
		}
	}
}

// failingLayer echoes like layerEcho, but fails a tile of the named layer alone.
type failingLayer struct {
	layerEcho
	layer string
}

func (p failingLayer) MVTForLayers(ctx context.Context, tile provider.Tile, params provider.Params, layers []provider.Layer) ([]byte, error) {
	if len(layers) == 1 && layers[0].MVTName == p.layer {
		return nil, errors.New("boom")
	}

	return p.layerEcho.MVTForLayers(ctx, tile, params, layers)
}

func TestSeedOfLayersTriesEveryKeyAndNamesWhatItWrote(t *testing.T) {
	grid := webMercator(t)
	tier := faketier.New("solo")

	a := &atlas.Atlas{}
	a.SetCache(tier)

	m := layeredMap("osm")
	m.SetMVTProvider("echo", &failingLayer{layer: "roads"})

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:              cacheops.Seed,
		Atlas:           a,
		Targets:         []cacheops.Target{{Map: m, Layers: []string{"roads", "buildings"}}},
		Grid:            grid,
		ContinueOnError: true,
	}, tiles(slippy.Tile{Z: 6, X: 1, Y: 1}))

	var te cacheops.ErrTile
	if !errors.As(err, &te) {
		t.Fatalf("err: got %v, expected an ErrTile", err)
	}
	for _, want := range []string{"the whole-map tile", "layer (buildings)", "layer (roads): "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err: got %q, expected it to mention %q", err, want)
		}
	}
	if res.Failed != 1 || res.Done != 0 {
		t.Errorf("result: got %+v, expected the tile failed", res)
	}
	if got, _ := layersIn(t, tier, layerKey(t, grid, "osm", "buildings", 6, 1, 1)); got != "buildings" {
		t.Errorf("buildings tile: got layers %q, expected it seeded after roads failed", got)
	}
}

func TestSeedOfAMapWithQueryParametersSkipsIt(t *testing.T) {
	tier := faketier.New("solo")
	a := &atlas.Atlas{}
	a.SetCache(tier)

	res, err := cacheops.Run(context.Background(), cacheops.Options{
		Op:      cacheops.Seed,
		Atlas:   a,
		Targets: []cacheops.Target{{Map: withParams(echoingMap("osm"))}},
		Grid:    webMercator(t),
	}, tiles(slippy.Tile{Z: 6, X: 1, Y: 1}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Skipped != 1 || res.Done != 0 {
		t.Errorf("result: got %+v, expected the tile skipped", res)
	}
	if len(tier.Calls()) != 0 {
		t.Errorf("the cache was touched: %v", tier.Calls())
	}
}

func TestUnprocessableTargetsMatchOneSentinel(t *testing.T) {
	errs := map[string]error{
		"no layer collections": cacheops.ValidateTargets(cacheops.Purge, []cacheops.Target{{Map: noLayerCollections(layeredMap("osm")), Layers: []string{"roads"}}}),
		"query parameters":     cacheops.ValidateTargets(cacheops.Seed, []cacheops.Target{{Map: withParams(layeredMap("osm")), Layers: []string{"roads"}}}),
		"zooms":                cacheops.ValidateZooms(cacheops.Seed, []cacheops.Target{{Map: layeredMap("osm"), Layers: []string{"pois"}}}, []uint{1}),
	}
	for name, err := range errs {
		if !errors.Is(err, cacheops.ErrUnprocessable) {
			t.Errorf("%v: got %v, expected it to match ErrUnprocessable", name, err)
		}
	}

	unknown := cacheops.ValidateTargets(cacheops.Seed, []cacheops.Target{{Map: layeredMap("osm"), Layers: []string{"rivers"}}})
	if unknown == nil || errors.Is(unknown, cacheops.ErrUnprocessable) {
		t.Errorf("unknown layer: got %v, expected a plain invalid target", unknown)
	}
}
