package cache

import (
	"context"
	"errors"
	"reflect"
	"testing"

	tegolaCache "github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/multi"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"

	_ "github.com/MapColonies/shigola/atlas"
)

var tiersKey = &tegolaCache.Key{MapName: "osm", Z: 7, X: 6, Y: 5}

func init() {
	for cacheType, name := range map[string]string{
		"clihot": "hot", "clidurable": "durable",
		"clinested1": "nested1", "clinested2": "nested2",
		"clisolo": "solo",
	} {
		tier := faketier.New(name)
		if err := tegolaCache.Register(cacheType, func(dict.Dicter) (tegolaCache.Interface, error) {
			return tier, nil
		}); err != nil {
			panic(err)
		}
	}
}

func twoTierCache(t *testing.T) tegolaCache.Interface {
	t.Helper()

	c, err := tegolaCache.For("multi", dict.Dict{
		"layers": []map[string]interface{}{
			{"type": "clihot", "name": "hot"},
			{"type": "clidurable", "name": "durable"},
		},
	})
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}

	return c
}

// TestResolveCacheTiers covers what a seed run is allowed to write, which is
// the behaviour most likely to surprise: adding a tier to an existing chain
// config changes what `tegola cache seed` writes.
func TestResolveCacheTiers(t *testing.T) {
	type tcase struct {
		flag        string
		expected    []string
		expectedErr error
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got, err := resolveCacheTiers(twoTierCache(t), tc.flag)

			if tc.expectedErr != nil {
				if err == nil {
					t.Fatalf("expected an error, got nil (resolved %v)", got)
				}
				if reflect.TypeOf(err) != reflect.TypeOf(tc.expectedErr) {
					t.Fatalf("expected %T, got %T: %v", tc.expectedErr, err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("got %v, expected %v", got, tc.expected)
			}
		}
	}

	tests := map[string]tcase{
		// The default, and the one that changes behaviour for an existing
		// config the moment a second tier is added.
		"unset targets the last tier": {flag: "", expected: []string{"durable"}},
		"all lifts the restriction":   {flag: "all", expected: nil},
		"ALL is accepted too":         {flag: "ALL", expected: nil},
		"an explicit tier":            {flag: "durable", expected: []string{"durable"}},
		"an explicit list":            {flag: "hot,durable", expected: []string{"hot", "durable"}},
		"whitespace is tolerated":     {flag: " hot , durable ", expected: []string{"hot", "durable"}},
		// An unknown name is an error at startup, not a silent no-op: a typo in
		// a cron job would otherwise look like a seed that wrote nothing.
		"an unknown tier": {flag: "hto", expectedErr: multi.ErrUnknownTier{}},
		"a partly unknown list": {
			flag: "hot,s3", expectedErr: multi.ErrUnknownTier{},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestResolveCacheTiersSingleBackend — one cache is also the last cache, so
// there is nothing to restrict and the seed default is unchanged for every
// deployment that is not using a chain.
func TestResolveCacheTiersSingleBackend(t *testing.T) {
	c, err := tegolaCache.For("clisolo", dict.Dict{})
	if err != nil {
		t.Fatalf("building the cache: %v", err)
	}

	for _, flag := range []string{"", "all"} {
		got, err := resolveCacheTiers(c, flag)
		if err != nil {
			t.Fatalf("--cache-tiers=%q: unexpected error: %v", flag, err)
		}
		if got != nil {
			t.Errorf("--cache-tiers=%q: got %v, expected no restriction", flag, got)
		}
	}

	// Naming a tier on a cache that has none is a mistake worth reporting.
	if _, err := resolveCacheTiers(c, "durable"); err == nil {
		t.Error("naming a tier on a single-backend cache was accepted")
	}
}

// TestResolveCacheTiersNested — resolution runs against the whole tree, and the
// default recurses: the last tier of the last tier.
func TestResolveCacheTiersNested(t *testing.T) {
	c, err := tegolaCache.For("multi", dict.Dict{
		"layers": []map[string]interface{}{
			{"type": "clihot", "name": "hot"},
			{"type": "multi", "name": "nested", "layers": []map[string]interface{}{
				{"type": "clinested1", "name": "inner1"},
				{"type": "clinested2", "name": "inner2"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}

	got, err := resolveCacheTiers(c, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"nested/inner2"}) {
		t.Errorf("got %v, expected [nested/inner2]", got)
	}

	if _, err := resolveCacheTiers(c, "nested/inner1"); err != nil {
		t.Errorf("a path-qualified name was rejected: %v", err)
	}
	if _, err := resolveCacheTiers(c, "inner1"); err == nil {
		t.Error("an unqualified nested name was accepted; names are path-qualified")
	}
}

// TestWorkerContextCarriesEveryIntent pins what the seed worker actually hands
// across the seam. A missing flag here is silent: the seed still runs, and
// quietly does the wrong thing.
func TestWorkerContextCarriesEveryIntent(t *testing.T) {
	type tcase struct {
		writeTiers []string
		overwrite  bool

		expectedTiers      []string
		expectedRestricted bool
		expectedInvalidate bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			savedTiers, savedOverwrite := seedWriteTiers, cacheOverwrite
			t.Cleanup(func() { seedWriteTiers, cacheOverwrite = savedTiers, savedOverwrite })

			seedWriteTiers, cacheOverwrite = tc.writeTiers, tc.overwrite

			var got context.Context
			worker := withCacheIntent(func(ctx context.Context, _ MapTile) error {
				got = ctx
				return nil
			})

			if err := worker(context.Background(), MapTile{}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// These two are unconditional. Without synchronous writes the seed
			// loses writes at process exit; without promotion suppression its
			// own reads flood the hot tier.
			if !tegolaCache.SynchronousWrites(got) {
				t.Error("the seed context does not carry WithSynchronousWrites")
			}
			if !tegolaCache.PromotionDisabled(got) {
				t.Error("the seed context does not carry WithoutPromotion")
			}

			names, restricted := tegolaCache.WriteTiers(got)
			if restricted != tc.expectedRestricted {
				t.Errorf("write tiers restricted: got %v, expected %v", restricted, tc.expectedRestricted)
			}
			if restricted && !reflect.DeepEqual(names, tc.expectedTiers) {
				t.Errorf("write tiers: got %v, expected %v", names, tc.expectedTiers)
			}

			if tegolaCache.InvalidateUnwritten(got) != tc.expectedInvalidate {
				t.Errorf("invalidate unwritten: got %v, expected %v",
					tegolaCache.InvalidateUnwritten(got), tc.expectedInvalidate)
			}
		}
	}

	tests := map[string]tcase{
		"durable only, no overwrite": {
			writeTiers:    []string{"durable"},
			expectedTiers: []string{"durable"}, expectedRestricted: true,
		},
		"durable only, overwrite": {
			writeTiers: []string{"durable"}, overwrite: true,
			expectedTiers: []string{"durable"}, expectedRestricted: true,
			expectedInvalidate: true,
		},
		// --cache-tiers=all resolves to no restriction, so there is nothing
		// left over to purge even with --overwrite.
		"all tiers, overwrite": {
			writeTiers: nil, overwrite: true,
			expectedRestricted: false, expectedInvalidate: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestSeedSemanticsEndToEnd drives the resolved context through a real chain,
// which is where the flags become behaviour.
func TestSeedSemanticsEndToEnd(t *testing.T) {
	rec := faketier.NewRecorder()
	hot := faketier.NewWithRecorder("hot", rec)
	durable := faketier.NewWithRecorder("durable", rec)

	chain, err := multi.NewChain([]tegolaCache.NamedTier{
		{Name: "hot", Cache: hot},
		{Name: "durable", Cache: durable},
	}, true, nil)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	savedTiers, savedOverwrite := seedWriteTiers, cacheOverwrite
	t.Cleanup(func() { seedWriteTiers, cacheOverwrite = savedTiers, savedOverwrite })

	seedWriteTiers, cacheOverwrite = []string{"durable"}, true
	hot.Seed(tiersKey, []byte("stale"))

	var ctx context.Context
	capture := withCacheIntent(func(c context.Context, _ MapTile) error { ctx = c; return nil })
	if err := capture(context.Background(), MapTile{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := chain.Set(ctx, tiersKey, []byte("fresh")); err != nil {
		t.Fatalf("set: unexpected error: %v", err)
	}

	if val, ok := durable.Value(tiersKey); !ok || string(val) != "fresh" {
		t.Errorf("durable tier: got (%q, %v), expected (fresh, true)", val, ok)
	}
	if _, ok := hot.Value(tiersKey); ok {
		t.Error("the stale hot-tier tile survived --overwrite")
	}

	// Write before purge, never the reverse: purging first leaves a window for
	// a concurrent read to promote the old tile back.
	expected := []string{"durable:set", "hot:purge"}
	if trace := rec.Trace(); !reflect.DeepEqual(trace, expected) {
		t.Errorf("trace: got %v, expected %v", trace, expected)
	}

	// And the seed's own read does not warm the hot tier.
	rec.Reset()
	durable.Seed(tiersKey, []byte("fresh"))
	if _, hit, err := chain.Get(ctx, tiersKey); !hit || err != nil {
		t.Fatalf("get: got (%v, %v), expected (true, nil)", hit, err)
	}
	if _, ok := hot.Value(tiersKey); ok {
		t.Error("the seed's read promoted into the hot tier")
	}
}

// TestUnknownTierErrorNamesTheAlternatives — the error has to be actionable,
// because the operator is usually looking at a cron job they cannot see the
// config for.
func TestUnknownTierErrorNamesTheAlternatives(t *testing.T) {
	_, err := resolveCacheTiers(twoTierCache(t), "durabel")

	var unknown multi.ErrUnknownTier
	if !errors.As(err, &unknown) {
		t.Fatalf("got %T, expected multi.ErrUnknownTier", err)
	}
	if unknown.Name != "durabel" {
		t.Errorf("name: got %q, expected %q", unknown.Name, "durabel")
	}
	if !reflect.DeepEqual(unknown.Known, []string{"hot", "durable"}) {
		t.Errorf("known: got %v, expected [hot durable]", unknown.Known)
	}
}
