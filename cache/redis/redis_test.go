package redis_test

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"reflect"
	"syscall"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/cache/redis"
	"github.com/go-spatial/tegola/dict"
	"github.com/go-spatial/tegola/internal/ttools"
	"github.com/go-spatial/tegola/tms"
)

// TESTENV is the environment variable that must be set to "yes" to run the redis tests.
const TESTENV = "RUN_REDIS_TESTS"

// ADDRENV overrides the address the connecting tests dial. It exists so the
// suite can reach a redis that is not on the loopback interface — a compose
// service name from inside a devcontainer, say. The default is the same address
// cache/redis itself defaults to, so an unset environment behaves as before.
const ADDRENV = "REDIS_TEST_ADDRESS"

var testAddress = ttools.GetEnvDefault(ADDRENV, "127.0.0.1:6379")

// withTestAddress fills in the test redis address, unless the case pins an
// address or a uri of its own — the cases that assert on a bad address must
// keep the address they were written with.
func withTestAddress(config dict.Dict) dict.Dict {
	if _, ok := config["address"]; ok {
		return config
	}
	if _, ok := config["uri"]; ok {
		return config
	}

	out := make(dict.Dict, len(config)+1)
	for k, v := range config {
		out[k] = v
	}
	out["address"] = testAddress

	return out
}

func TestCreateOptions(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	type tcase struct {
		name        string
		config      dict.Dict
		expected    *goredis.Options
		expectedErr error
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			actual, err := redis.CreateOptions(tc.config)
			if tc.expectedErr == nil && err != nil {
				t.Fatalf("unexpected error: %q", err)
				return
			}
			// Without this the case falls through to compareOptions with a nil
			// tc.expected — a segfault that takes the whole binary down and every
			// other subtest's result with it, rather than one clean failure.
			// TestNew guards the same way.
			if tc.expectedErr != nil && err == nil {
				t.Fatalf("expected err %v, got nil", tc.expectedErr)
				return
			}
			if tc.expectedErr != nil && err != nil {
				if reflect.TypeOf(err) != reflect.TypeOf(tc.expectedErr) {
					t.Errorf("invalid error type. expected %T, got %T", tc.expectedErr, err)
					return
				}
				return
			}
			compareOptions(t, actual, tc.expected)
		}
	}

	tests := map[string]tcase{
		"test complete config": {
			config: map[string]any{
				"network":  "tcp",
				"address":  "127.0.0.1:6379",
				"password": "test",
				"db":       0,
				"max_zoom": uint(10),
				"ssl":      false,
			},
			expected: &goredis.Options{
				Network:  "tcp",
				DB:       0,
				Addr:     "127.0.0.1:6379",
				Password: "test",
			},
		},
		"test with uri no ssl": {
			config: map[string]any{
				"uri": "redis://user:test@127.0.0.1:6379/0",
			},
			expected: &goredis.Options{
				Network:  "tcp",
				DB:       0,
				Addr:     "127.0.0.1:6379",
				Password: "test",
			},
		},
		"test with uri with ssl": {
			config: map[string]any{
				"uri": "rediss://user:test@127.0.0.1:6379/0",
			},
			expected: &goredis.Options{
				Network:   "tcp",
				DB:        0,
				Addr:      "127.0.0.1:6379",
				Password:  "test",
				TLSConfig: &tls.Config{ /* no deep comparison */ },
			},
		},
		"test empty config": {
			config: map[string]any{},
			expected: &goredis.Options{
				Network:  "tcp",
				DB:       0,
				Addr:     "127.0.0.1:6379",
				Password: "",
			},
		},
		// A password with characters a uri cannot carry literally. Every one of
		// these breaks or is silently mangled inside a uri unless percent-encoded;
		// the point of the key is that it needs no encoding at all.
		"test uri with password key": {
			config: map[string]any{
				"uri":      "redis://127.0.0.1:6379/0",
				"password": "Aa1^%$#!",
			},
			expected: &goredis.Options{
				Network:  "tcp",
				DB:       0,
				Addr:     "127.0.0.1:6379",
				Password: "Aa1^%$#!",
			},
		},
		"test password key overrides the uri password": {
			config: map[string]any{
				"uri":      "redis://user:fromuri@127.0.0.1:6379/0",
				"password": "fromkey",
			},
			expected: &goredis.Options{
				Network:  "tcp",
				DB:       0,
				Addr:     "127.0.0.1:6379",
				Password: "fromkey",
			},
		},
		// present but empty asks for no password, rather than falling back to
		// the one in the uri
		"test empty password key clears the uri password": {
			config: map[string]any{
				"uri":      "redis://user:fromuri@127.0.0.1:6379/0",
				"password": "",
			},
			expected: &goredis.Options{
				Network:  "tcp",
				DB:       0,
				Addr:     "127.0.0.1:6379",
				Password: "",
			},
		},
		// absent leaves the uri's password alone — the pre-existing behaviour
		"test uri password survives an absent password key": {
			config: map[string]any{
				"uri": "redis://user:fromuri@127.0.0.1:6379/0",
			},
			expected: &goredis.Options{
				Network:  "tcp",
				DB:       0,
				Addr:     "127.0.0.1:6379",
				Password: "fromuri",
			},
		},
		"test bad password with uri": {
			config: map[string]any{
				"uri":      "redis://127.0.0.1:6379/0",
				"password": 1,
			},
			expectedErr: dict.ErrKeyType{
				Key:   "password",
				Value: 1,
				T:     reflect.TypeOf(""),
			},
		},
		"test ssl config": {
			name: "test test ssl config",
			config: map[string]any{
				"network":  "tcp",
				"address":  "127.0.0.1:6379",
				"password": "test",
				"db":       0,
				"max_zoom": uint(10),
				"ssl":      true,
			},
			expected: &goredis.Options{
				Network:   "tcp",
				DB:        0,
				Addr:      "127.0.0.1:6379",
				Password:  "test",
				TLSConfig: &tls.Config{ /* no deep comparison */ },
			},
		},
		"test bad address": {
			name: "test test ssl config",
			config: map[string]any{
				"network":  "tcp",
				"address":  2,
				"password": "test",
				"db":       0,
			},
			expectedErr: dict.ErrKeyType{
				Key:   "addr",
				Value: 2,
				T:     reflect.TypeOf(""),
			},
		},
		"test bad host": {
			name: "test test ssl config",
			config: map[string]any{
				"network": "tcp",
				"address": "::8080",
				"db":      0,
			},
			expectedErr: &net.AddrError{ /* no deep comparison */ },
		},
		"test missing host": {
			name: "test test ssl config",
			config: map[string]any{
				"network": "tcp",
				"address": ":8080",
				"db":      0,
			},
			expectedErr: &redis.ErrHostMissing{},
		},
		"test missing port": {
			name: "test test ssl config",
			config: map[string]any{
				"network": "tcp",
				"address": "localhost",
				"db":      0,
			},
			expectedErr: &net.AddrError{ /* no deep comparison */ },
		},
		"test bad db": {
			name: "test test ssl config",
			config: map[string]any{
				"network": "tcp",
				"address": "127.0.0.1:6379",
				"db":      "fails",
			},
			expectedErr: dict.ErrKeyType{
				Key:   "db",
				Value: "fails",
				T:     reflect.TypeOf(1),
			},
		},
		"test bad password": {
			name: "test test ssl config",
			config: map[string]any{
				"network":  "tcp",
				"address":  "127.0.0.1:6379",
				"password": 0,
			},
			expectedErr: dict.ErrKeyType{
				Key:   "password",
				Value: 0,
				T:     reflect.TypeOf(""),
			},
		},
		"test bad network": {
			name: "test test ssl config",
			config: map[string]any{
				"network": 0,
				"address": "127.0.0.1:6379",
			},
			expectedErr: dict.ErrKeyType{
				Key:   "network",
				Value: 0,
				T:     reflect.TypeOf(1),
			},
		},
		"test bad ssl": {
			name: "test test ssl config",
			config: map[string]any{
				"network": "tcp",
				"address": "127.0.0.1:6379",
				"ssl":     0,
			},
			expectedErr: dict.ErrKeyType{
				Key:   "ssl",
				Value: 0,
				T:     reflect.TypeOf(true),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func compareOptions(t *testing.T, actual, expected *goredis.Options) {
	t.Helper()

	if actual.Addr != expected.Addr {
		t.Errorf("got %q, want %q", actual.Addr, expected.Addr)
	}
	if actual.DB != expected.DB {
		t.Errorf("DB: got %d, expected %d", actual.DB, expected.DB)
	}
	if actual.TLSConfig == nil && expected.TLSConfig != nil {
		t.Errorf("got nil TLSConfig, expected a TLSConfig")
	}
	if actual.TLSConfig != nil && expected.TLSConfig == nil {
		t.Errorf("got TLSConfig, expected no TLSConfig")
	}
	if actual.Password != expected.Password {
		t.Errorf("Password: got %q, expected %q", actual.Password, expected.Password)
	}
}

// TestRedisKey pins how key_prefix composes with cache.Key. It needs no redis, so
// unlike every other test in this file it is not gated behind RUN_REDIS_TESTS —
// key composition is worth checking on a bare `go test ./...`.
func TestRedisKey(t *testing.T) {
	type tcase struct {
		keyPrefix string
		key       cache.Key
		expected  string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			rc := &redis.RedisCache{KeyPrefix: tc.keyPrefix}

			if got := rc.RedisKeyForTest(&tc.key); got != tc.expected {
				t.Errorf("wrong key, expected %q got %q", tc.expected, got)
			}
		}
	}

	key := cache.Key{
		TileMatrixSetID: tms.WebMercatorQuad,
		MapName:         "osm",
		LayerName:       "water",
		Z:               10, X: 511, Y: 340,
	}

	tests := map[string]tcase{
		"no prefix": {
			keyPrefix: "",
			key:       key,
			expected:  "WebMercatorQuad/osm/water/10/511/340",
		},
		"colon separated prefix": {
			keyPrefix: "tegola:",
			key:       key,
			expected:  "tegola:WebMercatorQuad/osm/water/10/511/340",
		},
		// The documented sharp edge of concatenating rather than path-joining: a
		// prefix without a separator runs into the grid id. Pinned so it cannot
		// change silently.
		"prefix without a separator": {
			keyPrefix: "tegola",
			key:       key,
			expected:  "tegolaWebMercatorQuad/osm/water/10/511/340",
		},
		// filepath.Join would collapse the doubled slash here. Concatenation does
		// not touch the prefix, which is what lets ':' namespacing survive.
		"prefix is passed through verbatim": {
			keyPrefix: "tegola//",
			key:       key,
			expected:  "tegola//WebMercatorQuad/osm/water/10/511/340",
		},
		"prefix on a key with no map or layer": {
			keyPrefix: "tegola:",
			key:       cache.Key{Z: 0, X: 1, Y: 2},
			expected:  "tegola:WebMercatorQuad/0/1/2",
		},
		// The grid partitions the redis keyspace under the same prefix, so a
		// shared redis cannot serve one grid's tiles for another (ADR-0007).
		"another grid, same prefix and tile": {
			keyPrefix: "tegola:",
			key: cache.Key{
				TileMatrixSetID: tms.WorldCRS84Quad,
				MapName:         "osm",
				LayerName:       "water",
				Z:               10, X: 511, Y: 340,
			},
			expected: "tegola:WorldCRS84Quad/osm/water/10/511/340",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestNew will run tests against a live redis instance — 127.0.0.1:6379 unless
// REDIS_TEST_ADDRESS says otherwise.
func TestNew(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	type tcase struct {
		config      dict.Dict
		expectedErr error
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			_, err := redis.New(withTestAddress(tc.config))
			if tc.expectedErr != nil {
				if err == nil {
					t.Errorf("expected err %v, got nil", tc.expectedErr.Error())
					return
				}

				// check error types
				if reflect.TypeOf(err) != reflect.TypeOf(tc.expectedErr) {
					t.Errorf("invalid error type. expected %T, got %T", tc.expectedErr, err)
					return
				}

				switch e := err.(type) {
				case *net.OpError:
					expectedErr := tc.expectedErr.(*net.OpError)

					if reflect.TypeOf(e.Err) != reflect.TypeOf(expectedErr.Err) {
						t.Errorf("invalid error type. expected %T, got %T", expectedErr.Err, e.Err)
						return
					}
				default:
					// check error messages
					if err.Error() != tc.expectedErr.Error() {
						t.Errorf("invalid error. expected %v, got %v", tc.expectedErr, err.Error())
						return
					}
				}

				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
				return
			}
		}
	}

	tests := map[string]tcase{
		"explicit config": {
			config: map[string]any{
				"network":  "tcp",
				"address":  testAddress,
				"password": "",
				"db":       0,
				"max_zoom": uint(10),
				"ssl":      false,
			},
		},
		"explicit config with uri": {
			config: map[string]any{
				"uri": "redis://" + testAddress + "/0",
			},
		},
		"implicit config": {
			config: map[string]any{},
		},
		"explicit config with key_prefix": {
			config: map[string]any{
				"key_prefix": "tegola:",
			},
		},
		"bad config address": {
			config: map[string]any{"address": 0},
			expectedErr: dict.ErrKeyType{
				Key:   "address",
				Value: 0,
				T:     reflect.TypeOf(""),
			},
		},
		"bad config uri": {
			config: map[string]any{"uri": 1},
			expectedErr: dict.ErrKeyType{
				Key:   "uri",
				Value: 1,
				T:     reflect.TypeOf(""),
			},
		},
		"bad config ttl": {
			config: map[string]any{"ttl": "fails"},
			expectedErr: dict.ErrKeyType{
				Key:   "ttl",
				Value: "fails",
				T:     reflect.TypeOf(1),
			},
		},
		"bad config key_prefix": {
			config: map[string]any{"key_prefix": 1},
			expectedErr: dict.ErrKeyType{
				Key:   "key_prefix",
				Value: 1,
				T:     reflect.TypeOf(""),
			},
		},
		"bad address": {
			config: map[string]any{
				"address": "127.0.0.1:6000",
			},
			expectedErr: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Addr: &net.TCPAddr{
					IP:   net.ParseIP("127.0.0.1"),
					Port: 6000,
				},
				Err: &os.SyscallError{
					Err: syscall.ECONNREFUSED,
				},
			},
		},
		"bad max_zoom": {
			config: map[string]any{
				"max_zoom": "2",
			},
			expectedErr: dict.ErrKeyType{
				Key:   "max_zoom",
				Value: "2",
				T:     reflect.TypeOf(uint(0)),
			},
		},
		"bad max_zoom 2": {
			config: map[string]any{
				"max_zoom": -2,
			},
			expectedErr: dict.ErrKeyType{
				Key:   "max_zoom",
				Value: -2,
				T:     reflect.TypeOf(uint(0)),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestSetGetPurge(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	ctx := context.Background()
	type tcase struct {
		config       dict.Dict
		key          cache.Key
		expectedData []byte
		expectedHit  bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			rc, err := redis.New(withTestAddress(tc.config))
			if err != nil {
				t.Errorf("unexpected err, expected %v got %v", nil, err)
				return
			}

			// test write
			if tc.expectedHit {
				err = rc.Set(ctx, &tc.key, tc.expectedData)
				if err != nil {
					t.Errorf("unexpected err, expected %v got %v", nil, err)
				}
				return
			}

			// test read
			output, hit, err := rc.Get(ctx, &tc.key)
			if err != nil {
				t.Errorf("read failed with error, expected %v got %v", nil, err)
				return
			}
			if tc.expectedHit != hit {
				t.Errorf("read failed, wrong 'hit' value expected %t got %t", tc.expectedHit, hit)
				return
			}

			if !reflect.DeepEqual(output, tc.expectedData) {
				t.Errorf("read failed, expected %v got %v", output, tc.expectedData)
				return
			}

			// test purge
			if tc.expectedHit {
				err = rc.Purge(ctx, &tc.key)
				if err != nil {
					t.Errorf("purge failed with err, expected %v got %v", nil, err)
					return
				}
			}
		}
	}

	testcases := map[string]tcase{
		"redis cache hit": {
			config: map[string]any{},
			key: cache.Key{
				Z: 0,
				X: 1,
				Y: 2,
			},
			expectedData: []byte("\x53\x69\x6c\x61\x73"),
			expectedHit:  true,
		},
		"redis cache miss": {
			config: map[string]any{},
			key: cache.Key{
				Z: 0,
				X: 0,
				Y: 0,
			},
			expectedData: []byte(nil),
			expectedHit:  false,
		},
	}

	for name, tc := range testcases {
		t.Run(name, fn(tc))
	}

}

func TestSetOverwrite(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	ctx := context.Background()
	type tcase struct {
		config   dict.Dict
		key      cache.Key
		bytes1   []byte
		bytes2   []byte
		expected []byte
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			rc, err := redis.New(withTestAddress(tc.config))
			if err != nil {
				t.Errorf("unexpected err, expected %v got %v", nil, err)
				return
			}

			// test write1
			if err = rc.Set(ctx, &tc.key, tc.bytes1); err != nil {
				t.Errorf("write failed with err, expected %v got %v", nil, err)
				return
			}

			// test write2
			if err = rc.Set(ctx, &tc.key, tc.bytes2); err != nil {
				t.Errorf("write failed with err, expected %v got %v", nil, err)
				return
			}

			// fetch the cache entry
			output, hit, err := rc.Get(ctx, &tc.key)
			if err != nil {
				t.Errorf("read failed with err, expected %v got %v", nil, err)
				return
			}
			if !hit {
				t.Errorf("read failed, expected hit %t got %t", true, hit)
				return
			}

			if !reflect.DeepEqual(output, tc.expected) {
				t.Errorf("read failed, expected %v got %v)", output, tc.expected)
				return
			}

			// clean up
			if err = rc.Purge(ctx, &tc.key); err != nil {
				t.Errorf("purge failed with err, expected %v got %v", nil, err)
				return
			}
		}
	}

	testcases := map[string]tcase{
		"redis overwrite": {
			config: map[string]any{},
			key: cache.Key{
				Z: 0,
				X: 1,
				Y: 1,
			},
			bytes1:   []byte("\x66\x6f\x6f"),
			bytes2:   []byte("\x53\x69\x6c\x61\x73"),
			expected: []byte("\x53\x69\x6c\x61\x73"),
		},
	}

	for name, tc := range testcases {
		t.Run(name, fn(tc))
	}
}

func TestMaxZoom(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)
	ctx := context.Background()

	type tcase struct {
		config      dict.Dict
		key         cache.Key
		bytes       []byte
		expectedHit bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			rc, err := redis.New(withTestAddress(tc.config))
			if err != nil {
				t.Fatalf("unexpected err, expected %v got %v", nil, err)
			}

			// test write
			if tc.expectedHit {
				err = rc.Set(ctx, &tc.key, tc.bytes)
				if err != nil {
					t.Fatalf("unexpected err, expected %v got %v", nil, err)
				}
			}

			// test read
			_, hit, err := rc.Get(ctx, &tc.key)
			if err != nil {
				t.Fatalf("read failed with error, expected %v got %v", nil, err)
			}
			if tc.expectedHit != hit {
				t.Fatalf("read failed, wrong 'hit' value expected %t got %t", tc.expectedHit, hit)
			}

			// test purge
			if tc.expectedHit {
				err = rc.Purge(ctx, &tc.key)
				if err != nil {
					t.Fatalf("purge failed with err, expected %v got %v", nil, err)
				}
			}
		}
	}

	tests := map[string]tcase{
		"over max zoom": {
			config: map[string]any{
				"max_zoom": uint(10),
			},
			key: cache.Key{
				Z: 11,
				X: 1,
				Y: 1,
			},
			bytes:       []byte("\x41\x64\x61"),
			expectedHit: false,
		},
		"under max zoom": {
			config: map[string]any{
				"max_zoom": uint(10),
			},
			key: cache.Key{
				Z: 9,
				X: 1,
				Y: 1,
			},
			bytes:       []byte("\x41\x64\x61"),
			expectedHit: true,
		},
		"equals max zoom": {
			config: map[string]any{
				"max_zoom": uint(10),
			},
			key: cache.Key{
				Z: 10,
				X: 1,
				Y: 1,
			},
			bytes:       []byte("\x41\x64\x61"),
			expectedHit: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestKeyPrefix checks the property TestRedisKey cannot: that the prefix reaches
// the wire on all three operations. A prefix honoured by Set but not Purge would
// leave keys no tegola can delete, and one honoured by Set but not Get would miss
// everything it wrote — both pass a round-trip test that only talks to itself, so
// this one reads the raw key with a client of its own.
func TestKeyPrefix(t *testing.T) {
	ttools.ShouldSkip(t, TESTENV)

	ctx := context.Background()

	const keyPrefix = "tegola-prefix-test:"
	key := cache.Key{MapName: "prefixtest", LayerName: "water", Z: 4, X: 2, Y: 3}
	val := []byte("\x53\x69\x6c\x61\x73")

	prefixed, err := redis.New(withTestAddress(dict.Dict{"key_prefix": keyPrefix}))
	if err != nil {
		t.Fatalf("unexpected err, expected %v got %v", nil, err)
	}

	unprefixed, err := redis.New(withTestAddress(dict.Dict{}))
	if err != nil {
		t.Fatalf("unexpected err, expected %v got %v", nil, err)
	}

	// a client of our own, so the assertions do not depend on the code under test
	// to tell us what it wrote
	raw := goredis.NewClient(&goredis.Options{Addr: testAddress})
	defer raw.Close()

	if err := prefixed.Set(ctx, &key, val); err != nil {
		t.Fatalf("set failed with err, expected %v got %v", nil, err)
	}
	// belt and braces: whatever the assertions below do, do not leave the key behind
	defer raw.Del(ctx, keyPrefix+key.String())

	got, err := raw.Get(ctx, keyPrefix+key.String()).Bytes()
	if err != nil {
		t.Fatalf("expected %q to exist, got err %v", keyPrefix+key.String(), err)
	}
	if !reflect.DeepEqual(got, val) {
		t.Errorf("wrong value at %q, expected %v got %v", keyPrefix+key.String(), val, got)
	}

	// nothing may be written at the unprefixed key
	if err := raw.Get(ctx, key.String()).Err(); err != goredis.Nil {
		t.Errorf("expected %q to not exist, got err %v", key.String(), err)
	}

	// the isolation property the option exists for: a cache without the prefix
	// cannot see a prefixed cache's tiles
	if _, hit, err := unprefixed.Get(ctx, &key); err != nil {
		t.Errorf("read failed with error, expected %v got %v", nil, err)
	} else if hit {
		t.Error("unprefixed cache hit a prefixed key, expected a miss")
	}

	// and the prefixed cache reads back its own write
	if out, hit, err := prefixed.Get(ctx, &key); err != nil {
		t.Errorf("read failed with error, expected %v got %v", nil, err)
	} else if !hit {
		t.Error("prefixed cache missed its own write, expected a hit")
	} else if !reflect.DeepEqual(out, val) {
		t.Errorf("read failed, expected %v got %v", val, out)
	}

	// a purge through the prefixed cache must reach the prefixed key
	if err := prefixed.Purge(ctx, &key); err != nil {
		t.Fatalf("purge failed with err, expected %v got %v", nil, err)
	}
	if err := raw.Get(ctx, keyPrefix+key.String()).Err(); err != goredis.Nil {
		t.Errorf("expected %q to be purged, got err %v", keyPrefix+key.String(), err)
	}
}
