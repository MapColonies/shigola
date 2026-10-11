package admin_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/server/admin"
)

func newLimitedFixture(t *testing.T, cfg admin.Config, maps ...atlas.Map) *fixture {
	t.Helper()

	f := newFixture(t, "", maps...)
	a := &atlas.Atlas{}
	a.SetCache(f.tier)
	for _, m := range maps {
		a.AddMap(m)
	}
	cfg.Atlas = a
	f.handler = admin.New(cfg)

	return f
}

func TestOversizedRequestIsRejectedBeforeAnyTileIsTouched(t *testing.T) {
	f := newLimitedFixture(t, admin.Config{MaxTilesPerRequest: 2}, atlas.NewWebMercatorMap("osm"))

	rec := f.do("/cache/purge", purgeBBox) // 3 tiles
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, expected 422. body: %s", rec.Code, rec.Body)
	}
	if r := decode(t, rec); !strings.Contains(r.Error, "3") || !strings.Contains(r.Error, "shigola cache") {
		t.Errorf("error: got %q, expected the tile count and a pointer to the CLI", r.Error)
	}
	if len(f.tier.Calls()) != 0 {
		t.Error("an oversized request touched the cache")
	}
}

func TestTileLimitCountsEveryTileMatrixSetAndLayerKey(t *testing.T) {
	m := twoGridMap(t, "osm")
	m.Layers = []atlas.Layer{{Name: "roads", MaxZoom: 20}}

	// 3 WebMercatorQuad tiles plus 4 WorldCRS84Quad tiles, each a whole-map and a roads key
	for limit, expected := range map[int]int{13: http.StatusUnprocessableEntity, 14: http.StatusOK} {
		f := newLimitedFixture(t, admin.Config{MaxTilesPerRequest: limit}, m)

		if rec := f.do("/cache/purge", purgeBBox); rec.Code != expected {
			t.Errorf("limit %d: got %d, expected %d. body: %s", limit, rec.Code, expected, rec.Body)
		}
	}
}

func TestTileLimitCountsATileListOnceAfterDeduplication(t *testing.T) {
	f := newLimitedFixture(t, admin.Config{MaxTilesPerRequest: 1}, atlas.NewWebMercatorMap("osm"))

	// both z2 tiles lie inside z1 tile 1/0/0, so at zoom 1 they touch one tile
	rec := f.do("/cache/purge", `{
		"tiles": ["2/0/0", "2/1/0"],
		"sourceTileMatrixSet": "WebMercatorQuad",
		"zoomRange": {"min": 1, "max": 1},
		"targets": [{"map": "osm"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200 for a single unique tile. body: %s", rec.Code, rec.Body)
	}
}

func TestDefaultTileLimitApplies(t *testing.T) {
	f := newLimitedFixture(t, admin.Config{}, atlas.NewWebMercatorMap("osm"))

	// the whole world at z7 is 16384 WebMercatorQuad tiles, over the default of 10000
	rec := f.do("/cache/purge", `{"bounds": [-180, -85, 180, 85], "zoomRange": {"min": 7, "max": 7}, "targets": [{"map": "osm"}]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, expected 422. body: %s", rec.Code, rec.Body)
	}
}

func TestRequestsOverTheConcurrencyLimitAreTurnedAway(t *testing.T) {
	f := newLimitedFixture(t, admin.Config{MaxConcurrentRequests: 1}, atlas.NewWebMercatorMap("osm"))
	gate := faketier.NewGate()
	f.tier.GateOn(faketier.OpPurge, gate)

	var first int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); first = f.do("/cache/purge", purgeBBox).Code }()
	gate.WaitEntered(1)

	rec := f.do("/cache/purge", purgeBBox)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("second request: got %d with Retry-After %q, expected 429 with one", rec.Code, rec.Header().Get("Retry-After"))
	}

	gate.Release()
	wg.Wait()
	if first != http.StatusOK {
		t.Errorf("first request: got %d, expected 200", first)
	}
	if rec := f.do("/cache/purge", purgeBBox); rec.Code != http.StatusOK {
		t.Errorf("after the slot freed: got %d, expected 200", rec.Code)
	}
}

func TestTimedOutRequestStopsAndReportsWhatItDid(t *testing.T) {
	f := newLimitedFixture(t, admin.Config{RequestTimeout: 50 * time.Millisecond}, atlas.NewWebMercatorMap("osm"))
	f.tier.GateOn(faketier.OpPurge, faketier.NewGate())

	start := time.Now()
	rec := f.do("/cache/purge", purgeBBox)

	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the request took %v, expected it to stop near its 50ms timeout", took)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, expected 503. body: %s", rec.Code, rec.Body)
	}
	if r := decode(t, rec); r.Purged != 0 || !strings.Contains(r.Error, "timed out") {
		t.Errorf("result: got %+v, expected nothing purged and a timeout error", r)
	}
}
