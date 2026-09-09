package tracing_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/internal/faketier"
	"github.com/MapColonies/shigola/internal/faketracer"
	"github.com/MapColonies/shigola/tms"
	"github.com/MapColonies/shigola/tracing"
)

func testKey(t *testing.T) *cache.Key {
	t.Helper()

	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		t.Fatalf("grid: %v", err)
	}

	key, err := cache.NewKey(grid, "osm", "", 6, 5, 4)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	return &key
}

func TestTracedCacheSpanPerOperation(t *testing.T) {
	type tcase struct {
		tier string
		want []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			backend, exporter := faketracer.New(t)
			key := testKey(t)

			var traced cache.Interface
			if tc.tier == "" {
				traced = backend.InstrumentedCache(faketier.New("inner"))
			} else {
				traced = backend.InstrumentedTierCache(tc.tier, faketier.New("inner"))
			}

			ctx := context.Background()
			if _, _, err := traced.Get(ctx, key); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if err := traced.Set(ctx, key, []byte("tile")); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if err := traced.Purge(ctx, key); err != nil {
				t.Fatalf("Purge: %v", err)
			}

			for _, name := range tc.want {
				span := faketracer.SpanNamed(t, exporter, name)

				if got := faketracer.StringAttr(span, tracing.AttrCacheKey); got != key.String() {
					t.Errorf("%v key attribute = %q, want %q", name, got, key.String())
				}

				if tc.tier == "" {
					continue
				}
				if got := faketracer.StringAttr(span, tracing.AttrCacheTier); got != tc.tier {
					t.Errorf("%v tier attribute = %q, want %q", name, got, tc.tier)
				}
			}
		}
	}

	tests := map[string]tcase{
		// Different span names, not one name with a tier attribute: a
		// whole-cache read is one tile fetched from somewhere in the chain and
		// a tier read is one lookup, and a query that summed them would be
		// counting two different things.
		"whole cache": {
			want: []string{tracing.SpanCacheGet, tracing.SpanCacheSet, tracing.SpanCachePurge},
		},
		"one tier": {
			tier: "hot",
			want: []string{tracing.SpanTierGet, tracing.SpanTierSet, tracing.SpanTierPurge},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestTracedCacheGetIsTransparent is the invariant the decorator must not
// break: a layered cache's read failures are misses, never errors, and a
// tracing wrapper that changed any part of the returned tuple would break the
// property that keeps tiles serving when a tier dies.
func TestTracedCacheGetIsTransparent(t *testing.T) {
	readFailed := errors.New("tier: unreachable")

	type tcase struct {
		seed    bool
		failOn  error
		wantHit bool
		wantErr error
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			backend, _ := faketracer.New(t)
			key := testKey(t)

			inner := faketier.New("inner")
			if tc.seed {
				inner.Seed(key, []byte("tile"))
			}
			if tc.failOn != nil {
				inner.FailOn(faketier.OpGet, tc.failOn)
			}

			traced := backend.InstrumentedTierCache("hot", inner)

			body, hit, err := traced.Get(context.Background(), key)

			if hit != tc.wantHit {
				t.Errorf("hit = %v, want %v", hit, tc.wantHit)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantHit && string(body) != "tile" {
				t.Errorf("body = %q, want %q", body, "tile")
			}
		}
	}

	tests := map[string]tcase{
		"hit":                    {seed: true, wantHit: true},
		"miss":                   {},
		"read failure is a miss": {failOn: readFailed, wantErr: readFailed},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestTracedCacheRecordsTheCachesOwnFailures draws the same line the prometheus
// observer's countReadError draws.
//
// A tier that failed on its own is a failed span. A read that failed because
// the client walked away is recorded but not marked failed — otherwise every
// disconnect paints the service red, and the trace contradicts
// shigola_cache_tier_errors_total, which does not count those either.
func TestTracedCacheRecordsTheCachesOwnFailures(t *testing.T) {
	type tcase struct {
		cancelCaller bool
		wantCode     codes.Code
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			backend, exporter := faketracer.New(t)
			key := testKey(t)

			inner := faketier.New("inner")
			inner.FailOn(faketier.OpGet, errors.New("tier: unreachable"))

			traced := backend.InstrumentedTierCache("hot", inner)

			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancelCaller {
				cancel()
			} else {
				defer cancel()
			}

			//nolint:errcheck // the error is the point; the span is what is asserted
			traced.Get(ctx, key)

			span := faketracer.SpanNamed(t, exporter, tracing.SpanTierGet)

			if span.Status.Code != tc.wantCode {
				t.Errorf("span status = %v, want %v", span.Status.Code, tc.wantCode)
			}
			if len(span.Events) == 0 {
				t.Error("the error was not recorded on the span at all")
			}
		}
	}

	tests := map[string]tcase{
		"the tier failed":        {wantCode: codes.Error},
		"the caller walked away": {cancelCaller: true, wantCode: codes.Unset},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// fakeTiered is a cache that reports tiers, so the reach-through below has
// something to find.
type fakeTiered struct {
	cache.Interface
	tiers []cache.NamedTier
}

func (f *fakeTiered) Tiers() []cache.NamedTier { return f.tiers }

func (f *fakeTiered) WithTiers([]cache.Interface) cache.Interface { return f }

// TestTracedCacheCanBeReachedThrough is why the wrapper implements
// cache.Wrapped.
//
// cache.TieredOf, cache.WritePoolOf and cache.ChainStatsOf all walk down from
// whatever cache is installed, and `shigola cache seed --cache-tiers` resolves
// tier names through the first of them. A wrapper that could not be unwrapped
// would make the chain invisible to all three the moment tracing was switched
// on.
func TestTracedCacheCanBeReachedThrough(t *testing.T) {
	backend, _ := faketracer.New(t)

	inner := &fakeTiered{
		Interface: faketier.New("inner"),
		tiers:     []cache.NamedTier{{Name: "hot", Cache: faketier.New("hot")}},
	}

	traced := backend.InstrumentedCache(inner)

	if _, ok := traced.(*fakeTiered); ok {
		t.Fatal("the cache was not wrapped at all")
	}

	wrapped, ok := traced.(tracing.Cache)
	if !ok {
		t.Fatal("a traced cache does not satisfy tracing.Cache, so atlas cannot strip it")
	}
	if !wrapped.IsTracer() {
		t.Error("IsTracer() = false")
	}
	if wrapped.Original() != cache.Interface(inner) {
		t.Error("Original() does not return the cache that was wrapped")
	}

	tiered, ok := cache.TieredOf(traced)
	if !ok {
		t.Fatal("cache.TieredOf cannot see the chain through the tracing wrapper")
	}
	if got := tiered.Tiers(); len(got) != 1 || got[0].Name != "hot" {
		t.Errorf("Tiers() = %v, want one tier named hot", got)
	}
}

// TestNullTracerInstallsNothing is the "off by default" half of the feature:
// with tracing disabled the caches and handlers are the ones that were passed
// in, so there is no decorator to cost anything.
func TestNullTracerInstallsNothing(t *testing.T) {
	inner := faketier.New("inner")

	if got := tracing.NullTracer.InstrumentedCache(inner); got != cache.Interface(inner) {
		t.Error("InstrumentedCache wrapped the cache")
	}
	if got := tracing.NullTracer.InstrumentedTierCache("hot", inner); got != cache.Interface(inner) {
		t.Error("InstrumentedTierCache wrapped the cache")
	}
	if tracing.NullTracer.Enabled() {
		t.Error("Enabled() = true")
	}
}
