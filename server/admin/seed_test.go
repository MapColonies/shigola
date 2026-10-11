package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/multi"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/provider/test"
	"github.com/MapColonies/shigola/server/admin"
	"github.com/MapColonies/shigola/tms"
)

func renderable(t *testing.T, name string) atlas.Map {
	t.Helper()

	m := twoGridMap(t, name)
	m.SetMVTProvider("test", &test.TileProvider{MVTTile: []byte("fresh")})

	return m
}

func seeded(t *testing.T, body []byte) int64 {
	t.Helper()

	var r struct {
		Seeded *int64 `json:"seeded"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Seeded == nil {
		t.Fatalf("decoding %q: no seeded count (%v)", body, err)
	}

	return *r.Seeded
}

func TestSeedByBoundsWritesWholeMapTilesInEverySet(t *testing.T) {
	f := newFixture(t, "", renderable(t, "osm"))

	rec := f.do("/cache/seed", purgeBBox)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}
	if n := seeded(t, rec.Body.Bytes()); n != 7 {
		t.Errorf("seeded: got %d, expected 3 WebMercatorQuad and 4 WorldCRS84Quad tiles", n)
	}

	for _, k := range []*cache.Key{
		f.key(tms.WebMercatorQuad, "osm", "", 2, 1, 1),
		f.key(tms.WorldCRS84Quad, "osm", "", 2, 2, 1),
	} {
		if _, ok := f.tier.Value(k); !ok {
			t.Errorf("%v was not seeded", k)
		}
	}
}

func TestSeedOverwriteDecidesWhetherCachedTilesAreReplaced(t *testing.T) {
	for overwrite, expectOld := range map[string]bool{"false": true, "true": false} {
		f := newFixture(t, "", renderable(t, "osm"))
		cached := f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0)
		f.tier.Seed(cached, []byte("old"))

		rec := f.do("/cache/seed", `{
			"bounds": [-100, 10, -80, 20],
			"zoomRange": {"min": 1, "max": 1},
			"targets": [{"map": "osm"}],
			"overwrite": `+overwrite+`
		}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("overwrite %v: got %d. body: %s", overwrite, rec.Code, rec.Body)
		}

		if v, _ := f.tier.Value(cached); (string(v) == "old") != expectOld {
			t.Errorf("overwrite %v: cached tile is %q", overwrite, v)
		}
	}
}

func TestSeedWritesTheLastTierUnlessToldOtherwise(t *testing.T) {
	for tiers, expectHot := range map[string]bool{`""`: false, `"all"`: true} {
		hot, durable := faketier.New("hot"), faketier.New("durable")
		chain, err := multi.NewChain([]cache.NamedTier{{Name: "hot", Cache: hot}, {Name: "durable", Cache: durable}}, true, nil)
		if err != nil {
			t.Fatalf("NewChain: %v", err)
		}

		a := &atlas.Atlas{}
		a.SetCache(chain)
		a.AddMap(renderable(t, "osm"))
		f := &fixture{t: t, tier: durable, handler: admin.New(admin.Config{Atlas: a})}

		rec := f.do("/cache/seed", `{
			"bounds": [-100, 10, -80, 20],
			"zoomRange": {"min": 1, "max": 1},
			"targets": [{"map": "osm"}],
			"tiers": `+tiers+`
		}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("tiers %v: got %d. body: %s", tiers, rec.Code, rec.Body)
		}

		k := f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0)
		if _, ok := durable.Value(k); !ok {
			t.Errorf("tiers %v: the durable tier was not written", tiers)
		}
		if _, ok := hot.Value(k); ok != expectHot {
			t.Errorf("tiers %v: hot tier written = %v, expected %v", tiers, ok, expectHot)
		}
	}
}

func TestRefusesWhatTheOperationCannotDo(t *testing.T) {
	type tcase struct {
		path, body string
		cfg        admin.Config
		expected   int
	}

	m := renderable(t, "osm")
	m.Layers = []atlas.Layer{{Name: "roads", MaxZoom: 20}}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			f := newLimitedFixture(t, tc.cfg, m)

			rec := f.do(tc.path, tc.body)
			if rec.Code != tc.expected {
				t.Fatalf("status: got %d, expected %d. body: %s", rec.Code, tc.expected, rec.Body)
			}
			if len(f.tier.Calls()) != 0 {
				t.Error("a refused request touched the cache")
			}
		}
	}

	tests := map[string]tcase{
		"seed naming a layer": {
			path:     "/cache/seed",
			body:     `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm", "layers": ["roads"]}]}`,
			expected: http.StatusUnprocessableEntity,
		},
		"seed over the tile limit": {
			path:     "/cache/seed",
			body:     purgeBBox,
			cfg:      admin.Config{MaxTilesPerRequest: 2},
			expected: http.StatusUnprocessableEntity,
		},
		"seed naming an unknown tier": {
			path:     "/cache/seed",
			body:     `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm"}], "tiers": "nope"}`,
			expected: http.StatusBadRequest,
		},
		"purge naming an unknown tier": {
			path:     "/cache/purge",
			body:     `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm"}], "tiers": "nope"}`,
			expected: http.StatusBadRequest,
		},
		"purge given overwrite": {
			path:     "/cache/purge",
			body:     `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm"}], "overwrite": true}`,
			expected: http.StatusBadRequest,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestPurgeAcceptsTiersLikeTheCLI(t *testing.T) {
	f := newFixture(t, "", atlas.NewWebMercatorMap("osm"))

	rec := f.do("/cache/purge", `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm"}], "tiers": "all"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}
}
