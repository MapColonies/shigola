package admin_test

import (
	"reflect"
	"testing"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/file"
	"github.com/MapColonies/shigola/cache/multi"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/server/admin"
)

func fileCache(t *testing.T, ttl int) cache.Interface {
	t.Helper()

	c, err := file.New(dict.Dict{"basepath": t.TempDir(), "ttl": ttl})
	if err != nil {
		t.Fatalf("file.New: %v", err)
	}

	return c
}

func TestUnexpiringPodTiersNamesFileTiersWithoutATTL(t *testing.T) {
	chain := func(tiers ...cache.NamedTier) cache.Interface {
		c, err := multi.NewChain(tiers, true, nil)
		if err != nil {
			t.Fatalf("NewChain: %v", err)
		}
		return c
	}

	type tcase struct {
		cache    cache.Interface
		expected []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := admin.UnexpiringPodTiers(tc.cache); !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("got %v, expected %v", got, tc.expected)
			}
		}
	}

	tests := map[string]tcase{
		"a file cache that never expires": {cache: fileCache(t, 0), expected: []string{"file"}},
		"a file cache with a ttl":         {cache: fileCache(t, 60)},
		"a shared tier only":              {cache: faketier.New("redis")},
		"a chain with an unexpiring file tier in front": {
			cache: chain(
				cache.NamedTier{Name: "hot", Cache: fileCache(t, 0)},
				cache.NamedTier{Name: "durable", Cache: faketier.New("s3")},
			),
			expected: []string{"hot"},
		},
		"a chain whose file tier expires": {
			cache: chain(
				cache.NamedTier{Name: "hot", Cache: fileCache(t, 60)},
				cache.NamedTier{Name: "durable", Cache: faketier.New("s3")},
			),
		},
		"a nested chain": {
			cache: chain(
				cache.NamedTier{Name: "local", Cache: chain(
					cache.NamedTier{Name: "disk", Cache: fileCache(t, 0)},
				)},
				cache.NamedTier{Name: "durable", Cache: faketier.New("s3")},
			),
			expected: []string{"local/disk"},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
