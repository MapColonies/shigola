package cache_test

import (
	"testing"
	"time"

	"github.com/MapColonies/shigola/cache"
)

// TestWriteOptionDefaults pins the compiled-in defaults, including the one the
// whole exhaustion argument rests on: the write bound is on by default.
func TestWriteOptionDefaults(t *testing.T) {
	cache.ResetWriteOptions()

	slots, timeout, drain := cache.WriteOptions()
	if slots != 256 {
		t.Errorf("slots: got %d, expected 256", slots)
	}
	if timeout != 10*time.Second {
		t.Errorf("write bound: got %v, expected 10s — the default must not be unbounded", timeout)
	}
	if drain != 5*time.Second {
		t.Errorf("drain: got %v, expected 5s", drain)
	}
}

func TestParseWriteOptions(t *testing.T) {
	type tcase struct {
		options string
		slots   int
		timeout time.Duration
		drain   time.Duration
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			cache.ResetWriteOptions()
			t.Cleanup(cache.ResetWriteOptions)

			cache.ParseOptions(tc.options)

			slots, timeout, drain := cache.WriteOptions()
			if slots != tc.slots {
				t.Errorf("slots: got %d, expected %d", slots, tc.slots)
			}
			if timeout != tc.timeout {
				t.Errorf("write bound: got %v, expected %v", timeout, tc.timeout)
			}
			if drain != tc.drain {
				t.Errorf("drain: got %v, expected %v", drain, tc.drain)
			}
		}
	}

	const (
		defSlots   = 256
		defTimeout = 10 * time.Second
		defDrain   = 5 * time.Second
	)

	tests := map[string]tcase{
		"empty": {
			options: "",
			slots:   defSlots, timeout: defTimeout, drain: defDrain,
		},
		"all three": {
			options: "DetachedWriteSlots=1024,DetachedWriteTimeoutMs=20000,DetachedWriteDrainMs=15000",
			slots:   1024, timeout: 20 * time.Second, drain: 15 * time.Second,
		},
		"alongside the options that were already there": {
			options: "DontSimplifyGeo,SimplifyMaxZoom=14,DetachedWriteSlots=512",
			slots:   512, timeout: defTimeout, drain: defDrain,
		},
		// Zero is meaningful for the durations: it disables the bound.
		"the write bound disabled": {
			options: "DetachedWriteTimeoutMs=0",
			slots:   defSlots, timeout: 0, drain: defDrain,
		},
		"the drain disabled": {
			options: "DetachedWriteDrainMs=0",
			slots:   defSlots, timeout: defTimeout, drain: 0,
		},
		// It is not meaningful for slots — a zero-slot pool drops everything.
		"zero slots falls back": {
			options: "DetachedWriteSlots=0",
			slots:   defSlots, timeout: defTimeout, drain: defDrain,
		},
		"negative slots falls back": {
			options: "DetachedWriteSlots=-1",
			slots:   defSlots, timeout: defTimeout, drain: defDrain,
		},
		// Falling back rather than disabling matters here: the failure mode of
		// an unbounded write is a permanently exhausted pool, and a typo must
		// not reach it.
		"a malformed write bound falls back, it does not disable": {
			options: "DetachedWriteTimeoutMs=soon",
			slots:   defSlots, timeout: defTimeout, drain: defDrain,
		},
		"a negative write bound falls back": {
			options: "DetachedWriteTimeoutMs=-1",
			slots:   defSlots, timeout: defTimeout, drain: defDrain,
		},
		// The parser splits on ".", which is why the keys carry their unit
		// rather than taking a duration string.
		"a duration string does not parse": {
			options: "DetachedWriteTimeoutMs=1.5s",
			slots:   defSlots, timeout: defTimeout, drain: defDrain,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
