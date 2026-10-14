package cache

import (
	"errors"
	"reflect"
	"testing"

	shigolaCache "github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/multi"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"

	_ "github.com/MapColonies/shigola/atlas"
)

var tiersKey = &shigolaCache.Key{MapName: "osm", Z: 7, X: 6, Y: 5}

func init() {
	for cacheType, name := range map[string]string{
		"clihot": "hot", "clidurable": "durable",
		"clinested1": "nested1", "clinested2": "nested2",
		"clisolo": "solo",
	} {
		tier := faketier.New(name)
		if err := shigolaCache.Register(cacheType, func(dict.Dicter) (shigolaCache.Interface, error) {
			return tier, nil
		}); err != nil {
			panic(err)
		}
	}
}

func twoTierCache(t *testing.T) shigolaCache.Interface {
	t.Helper()

	c, err := shigolaCache.For("multi", dict.Dict{
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
// config changes what `shigola cache seed` writes.
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
	c, err := shigolaCache.For("clisolo", dict.Dict{})
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
	c, err := shigolaCache.For("multi", dict.Dict{
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
