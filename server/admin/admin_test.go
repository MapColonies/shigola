package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/server/admin"
	"github.com/MapColonies/shigola/tms"
)

type fixture struct {
	t       *testing.T
	tier    *faketier.Tier
	handler http.Handler
}

func newFixture(t *testing.T, token string, maps ...atlas.Map) *fixture {
	t.Helper()

	tier := faketier.New("solo")
	a := &atlas.Atlas{}
	a.SetCache(tier)
	for _, m := range maps {
		a.AddMap(m)
	}

	return &fixture{t: t, tier: tier, handler: admin.New(admin.Config{Atlas: a, Token: token})}
}

func (f *fixture) key(gridID, mapName, layer string, z, x, y uint) *cache.Key {
	f.t.Helper()

	grid, err := tms.Get(gridID)
	if err != nil {
		f.t.Fatalf("tms.Get: %v", err)
	}
	k, err := cache.NewKey(grid, mapName, layer, z, x, y)
	if err != nil {
		f.t.Fatalf("cache.NewKey: %v", err)
	}

	return &k
}

func (f *fixture) do(path, body string, header ...string) *httptest.ResponseRecorder {
	f.t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

type result struct {
	Purged  int64  `json:"purged"`
	Skipped int64  `json:"skipped"`
	Failed  int64  `json:"failed"`
	Error   string `json:"error"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) result {
	t.Helper()

	var r result
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}

	return r
}

const purgeBBox = `{
	"bounds": [-100, 10, -80, 20],
	"zoomRange": {"min": 1, "max": 2},
	"targets": [{"map": "osm"}]
}`

func TestPurgeByBoundsRemovesTheCoveringTiles(t *testing.T) {
	f := newFixture(t, "", atlas.NewWebMercatorMap("osm"))

	covering := []*cache.Key{
		f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0),
		f.key(tms.WebMercatorQuad, "osm", "", 2, 0, 1),
		f.key(tms.WebMercatorQuad, "osm", "", 2, 1, 1),
	}
	outside := f.key(tms.WebMercatorQuad, "osm", "", 1, 1, 0)
	for _, k := range append(covering, outside) {
		f.tier.Seed(k, []byte("old"))
	}

	rec := f.do("/cache/purge", purgeBBox)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}

	if r := decode(t, rec); r.Purged != 3 || r.Failed != 0 {
		t.Errorf("result: got %+v, expected 3 purged", r)
	}
	for _, k := range covering {
		if _, ok := f.tier.Value(k); ok {
			t.Errorf("%v survived the purge", k)
		}
	}
	if _, ok := f.tier.Value(outside); !ok {
		t.Error("a tile outside the bounds was purged")
	}
}

func twoGridMap(t *testing.T, name string) atlas.Map {
	t.Helper()

	m := atlas.NewWebMercatorMap(name)
	crs84, err := tms.Get(tms.WorldCRS84Quad)
	if err != nil {
		t.Fatalf("tms.Get: %v", err)
	}
	m.TileMatrixSets = append(m.TileMatrixSets, crs84)

	return m
}

func TestPurgeCoversEveryTileMatrixSetTheMapServes(t *testing.T) {
	f := newFixture(t, "", twoGridMap(t, "osm"))

	covering := []*cache.Key{
		f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0),
		f.key(tms.WebMercatorQuad, "osm", "", 2, 0, 1),
		f.key(tms.WebMercatorQuad, "osm", "", 2, 1, 1),
		f.key(tms.WorldCRS84Quad, "osm", "", 1, 0, 0),
		f.key(tms.WorldCRS84Quad, "osm", "", 1, 1, 0),
		f.key(tms.WorldCRS84Quad, "osm", "", 2, 1, 1),
		f.key(tms.WorldCRS84Quad, "osm", "", 2, 2, 1),
	}
	for _, k := range covering {
		f.tier.Seed(k, []byte("old"))
	}

	rec := f.do("/cache/purge", purgeBBox)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}
	if r := decode(t, rec); r.Purged != 7 {
		t.Errorf("result: got %+v, expected 7 purged across both sets", r)
	}
	for _, k := range covering {
		if _, ok := f.tier.Value(k); ok {
			t.Errorf("%v survived the purge", k)
		}
	}
}

func TestPurgeOfOneLayerLeavesItsSiblings(t *testing.T) {
	m := atlas.NewWebMercatorMap("osm")
	m.Layers = []atlas.Layer{{Name: "roads", MaxZoom: 20}, {Name: "buildings", MaxZoom: 20}}
	f := newFixture(t, "", m)

	whole, roads := f.key(tms.WebMercatorQuad, "osm", "", 1, 0, 0), f.key(tms.WebMercatorQuad, "osm", "roads", 1, 0, 0)
	buildings := f.key(tms.WebMercatorQuad, "osm", "buildings", 1, 0, 0)
	for _, k := range []*cache.Key{whole, roads, buildings} {
		f.tier.Seed(k, []byte("old"))
	}

	rec := f.do("/cache/purge", `{
		"bounds": [-100, 10, -80, 20],
		"zoomRange": {"min": 1, "max": 1},
		"targets": [{"map": "osm", "layers": ["roads"]}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}

	for _, k := range []*cache.Key{whole, roads} {
		if _, ok := f.tier.Value(k); ok {
			t.Errorf("%v survived the purge", k)
		}
	}
	if _, ok := f.tier.Value(buildings); !ok {
		t.Error("the sibling layer was purged")
	}
}

func TestPurgeByTileListMatchesTheEquivalentBounds(t *testing.T) {
	f := newFixture(t, "", twoGridMap(t, "osm"))

	// WorldCRS84Quad 2/1/1 is lng -135..-90, lat 0..45
	rec := f.do("/cache/purge", `{
		"tiles": ["2/1/1"],
		"sourceTileMatrixSet": "WorldCRS84Quad",
		"zoomRange": {"min": 2, "max": 2},
		"targets": [{"map": "osm"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}
	fromTiles := f.tier.Calls()

	g := newFixture(t, "", twoGridMap(t, "osm"))
	g.do("/cache/purge", `{
		"bounds": [-135, 0, -90, 45],
		"zoomRange": {"min": 2, "max": 2},
		"targets": [{"map": "osm"}]
	}`)

	if got, want := purgedKeys(fromTiles), purgedKeys(g.tier.Calls()); !equalSets(got, want) || len(got) == 0 {
		t.Errorf("tile list purged %v, bounds purged %v", got, want)
	}
	if !purgedKeys(fromTiles)[f.key(tms.WorldCRS84Quad, "osm", "", 2, 1, 1).String()] {
		t.Error("the listed tile itself was not purged")
	}
}

func TestPurgeByTileListAtTheWestEdgeStaysOnItsSide(t *testing.T) {
	f := newFixture(t, "", atlas.NewWebMercatorMap("osm"))
	east := f.key(tms.WebMercatorQuad, "osm", "", 1, 1, 0)
	f.tier.Seed(east, []byte("kept"))

	rec := f.do("/cache/purge", `{
		"tiles": ["2/0/0"],
		"sourceTileMatrixSet": "WebMercatorQuad",
		"zoomRange": {"min": 1, "max": 1},
		"targets": [{"map": "osm"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}
	if r := decode(t, rec); r.Purged != 1 {
		t.Errorf("purged: got %d, expected only 1/0/0", r.Purged)
	}
	if _, ok := f.tier.Value(east); !ok {
		t.Error("a tile on the far side of the antimeridian was purged")
	}
}

func TestPurgeByTheWholeWorldTileCoversEveryColumn(t *testing.T) {
	f := newFixture(t, "", atlas.NewWebMercatorMap("osm"))

	rec := f.do("/cache/purge", `{
		"tiles": ["0/0/0"],
		"sourceTileMatrixSet": "WebMercatorQuad",
		"zoomRange": {"min": 1, "max": 1},
		"targets": [{"map": "osm"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, expected 200. body: %s", rec.Code, rec.Body)
	}
	if r := decode(t, rec); r.Purged != 4 {
		t.Errorf("purged: got %d, expected all 4 tiles at zoom 1", r.Purged)
	}
}

func purgedKeys(calls []faketier.Call) map[string]bool {
	out := map[string]bool{}
	for _, c := range calls {
		if c.Op == faketier.OpPurge {
			out[c.Key] = true
		}
	}
	return out
}

func equalSets(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func TestAdminTokenGuardsTheCacheRoutes(t *testing.T) {
	type tcase struct {
		token, header string
		expected      int
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			f := newFixture(t, tc.token, atlas.NewWebMercatorMap("osm"))

			var header []string
			if tc.header != "" {
				header = []string{"Authorization", tc.header}
			}

			rec := f.do("/cache/purge", purgeBBox, header...)
			if rec.Code != tc.expected {
				t.Fatalf("status: got %d, expected %d. body: %s", rec.Code, tc.expected, rec.Body)
			}
			if rec.Code == http.StatusUnauthorized && len(f.tier.Calls()) != 0 {
				t.Error("an unauthorized request touched the cache")
			}
		}
	}

	tests := map[string]tcase{
		"no token configured, none sent":    {expected: http.StatusOK},
		"token configured, none sent":       {token: "s3cret", expected: http.StatusUnauthorized},
		"token configured, wrong one sent":  {token: "s3cret", header: "Bearer nope", expected: http.StatusUnauthorized},
		"token configured, not as a bearer": {token: "s3cret", header: "s3cret", expected: http.StatusUnauthorized},
		"token configured, right one sent":  {token: "s3cret", header: "Bearer s3cret", expected: http.StatusOK},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestPurgeRejectsRequestsItCannotHonour(t *testing.T) {
	type tcase struct {
		body string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			f := newFixture(t, "", atlas.NewWebMercatorMap("osm"))

			rec := f.do("/cache/purge", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, expected 400. body: %s", rec.Code, rec.Body)
			}
			if decode(t, rec).Error == "" {
				t.Error("the 400 does not say what was wrong")
			}
			if len(f.tier.Calls()) != 0 {
				t.Error("a rejected request touched the cache")
			}
		}
	}

	tests := map[string]tcase{
		"malformed json":                 {body: `{"bounds": [`},
		"unknown field":                  {body: `{"bound": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}}`},
		"no area":                        {body: `{"zoomRange": {"min": 1, "max": 1}}`},
		"bounds and tiles":               {body: `{"bounds": [-100, 10, -80, 20], "tiles": ["1/0/0"], "sourceTileMatrixSet": "WebMercatorQuad", "zoomRange": {"min": 1, "max": 1}}`},
		"bounds off the globe":           {body: `{"bounds": [-100, 10, -80, 95], "zoomRange": {"min": 1, "max": 1}}`},
		"south above north":              {body: `{"bounds": [-100, 20, -80, 10], "zoomRange": {"min": 1, "max": 1}}`},
		"no zoom range":                  {body: `{"bounds": [-100, 10, -80, 20]}`},
		"inverted zoom range":            {body: `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 3, "max": 1}}`},
		"tiles without a set":            {body: `{"tiles": ["1/0/0"], "zoomRange": {"min": 1, "max": 1}}`},
		"tile off its set":               {body: `{"tiles": ["1/2/0"], "sourceTileMatrixSet": "WebMercatorQuad", "zoomRange": {"min": 1, "max": 1}}`},
		"tile not z/x/y":                 {body: `{"tiles": ["1-0-0"], "sourceTileMatrixSet": "WebMercatorQuad", "zoomRange": {"min": 1, "max": 1}}`},
		"no targets":                     {body: `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}}`},
		"empty targets":                  {body: `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": []}`},
		"unknown map":                    {body: `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "nope"}]}`},
		"unknown layer":                  {body: `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm", "layers": ["nope"]}]}`},
		"unknown tile matrix set":        {body: `{"tiles": ["1/0/0"], "sourceTileMatrixSet": "Nope", "zoomRange": {"min": 1, "max": 1}}`},
		"a stray brace after the object": {body: `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm"}]}}`},
		"data after the object":          {body: `{"bounds": [-100, 10, -80, 20], "zoomRange": {"min": 1, "max": 1}, "targets": [{"map": "osm"}]} {"x": 1}`},
		"body over the size limit":       {body: `{"tiles": ["` + strings.Repeat(`1/0/0", "`, 200000) + `1/0/0"]}`},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestPurgeReportsAFailingTier(t *testing.T) {
	f := newFixture(t, "", atlas.NewWebMercatorMap("osm"))
	f.tier.FailOn(faketier.OpPurge, errors.New("tier unavailable"))

	rec := f.do("/cache/purge", purgeBBox)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, expected 500. body: %s", rec.Code, rec.Body)
	}

	r := decode(t, rec)
	if r.Failed != 3 || !strings.Contains(r.Error, "tier unavailable") {
		t.Errorf("result: got %+v, expected all 3 tiles attempted and failed, naming the tier's error", r)
	}
}

func TestPurgeAttemptsEveryTileMatrixSetAfterOneFails(t *testing.T) {
	f := newFixture(t, "", twoGridMap(t, "osm"))
	f.tier.FailOn(faketier.OpPurge, errors.New("tier unavailable"))

	rec := f.do("/cache/purge", purgeBBox)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, expected 500. body: %s", rec.Code, rec.Body)
	}
	if r := decode(t, rec); r.Failed != 7 {
		t.Errorf("result: got %+v, expected the 7 tiles of both sets attempted", r)
	}
}

func TestADisconnectedClientIsNotReportedAsAFailure(t *testing.T) {
	f := newFixture(t, "", atlas.NewWebMercatorMap("osm"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodPost, "/cache/purge", strings.NewReader(purgeBBox)).WithContext(ctx)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusInternalServerError || strings.Contains(rec.Body.String(), "error") {
		t.Errorf("got %d %s, expected a gone client not to be answered with a failure", rec.Code, rec.Body)
	}
}
