package admin_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
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

func TestTileMatrixSetsNarrowTheRun(t *testing.T) {
	type tcase struct {
		path, sets  string
		expectCount int64
		expectSets  []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			f := newFixture(t, "", renderable(t, "osm"))
			if tc.path == "/cache/purge" {
				for _, k := range []*cache.Key{
					f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0),
					f.key(tms.WorldCRS84Quad, "osm", "", 1, 0, 0),
				} {
					f.tier.Seed(k, []byte("old"))
				}
			}

			rec := f.do(tc.path, `{
				"bounds": [-100, 10, -80, 20],
				"zoomRange": {"min": 1, "max": 1},
				"targets": [{"map": "osm"}],
				"tileMatrixSets": `+tc.sets+`
			}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
			}

			var r struct {
				Seeded, Purged int64
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
				t.Fatalf("decoding %s: %v", rec.Body, err)
			}
			if n := r.Seeded + r.Purged; n != tc.expectCount {
				t.Errorf("tiles: got %d, expected %d", n, tc.expectCount)
			}

			touched := map[string]bool{}
			for _, c := range f.tier.Calls() {
				touched[strings.SplitN(c.Key, "/", 2)[0]] = true
			}
			for _, id := range []string{tms.WebMercatorQuad, tms.WorldCRS84Quad} {
				if touched[id] != slices.Contains(tc.expectSets, id) {
					t.Errorf("%s touched = %v", id, touched[id])
				}
			}
		}
	}

	tests := map[string]tcase{
		"seed one set": {
			path: "/cache/seed", sets: `["WorldCRS84Quad"]`,
			expectCount: 2, expectSets: []string{tms.WorldCRS84Quad},
		},
		"seed both sets": {
			path: "/cache/seed", sets: `["WorldCRS84Quad", "WebMercatorQuad"]`,
			expectCount: 3, expectSets: []string{tms.WebMercatorQuad, tms.WorldCRS84Quad},
		},
		"seed every set when omitted": {
			path: "/cache/seed", sets: `null`,
			expectCount: 3, expectSets: []string{tms.WebMercatorQuad, tms.WorldCRS84Quad},
		},
		"purge one set": {
			path: "/cache/purge", sets: `["WebMercatorQuad"]`,
			expectCount: 1, expectSets: []string{tms.WebMercatorQuad},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestSeedOverwriteDecidesWhetherCachedTilesAreReplaced(t *testing.T) {
	type tcase struct {
		overwrite string
		expectOld bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			f := newFixture(t, "", renderable(t, "osm"))
			cached := f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0)
			f.tier.Seed(cached, []byte("old"))

			rec := f.do("/cache/seed", `{
				"bounds": [-100, 10, -80, 20],
				"zoomRange": {"min": 1, "max": 1},
				"targets": [{"map": "osm"}],
				"overwrite": `+tc.overwrite+`
			}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d. body: %s", rec.Code, rec.Body)
			}

			if v, _ := f.tier.Value(cached); (string(v) == "old") != tc.expectOld {
				t.Errorf("cached tile is %q", v)
			}
		}
	}

	tests := map[string]tcase{
		"without overwrite": {overwrite: "false", expectOld: true},
		"with overwrite":    {overwrite: "true", expectOld: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestSeedWritesTheLastTierUnlessToldOtherwise(t *testing.T) {
	type tcase struct {
		tiers     string
		expectHot bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
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
				"tiers": `+tc.tiers+`
			}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d. body: %s", rec.Code, rec.Body)
			}

			k := f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0)
			if _, ok := durable.Value(k); !ok {
				t.Error("the durable tier was not written")
			}
			if _, ok := hot.Value(k); ok != tc.expectHot {
				t.Errorf("hot tier written = %v, expected %v", ok, tc.expectHot)
			}
		}
	}

	tests := map[string]tcase{
		"default":   {tiers: `""`, expectHot: false},
		"all tiers": {tiers: `"all"`, expectHot: true},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
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
			refusal(t, rec)
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
