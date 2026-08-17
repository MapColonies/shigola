package cache

import (
	"strconv"
	"strings"
	"time"

	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/log"
)

// The write path's three switches live in TEGOLA_OPTIONS rather than in the
// [cache] table, because they are process resourcing and lifecycle rather than
// cache configuration — and because each must be changeable during the incident
// that reveals the need for it, without a config deploy.
//
//	TEGOLA_OPTIONS=DetachedWriteSlots=1024        # pool capacity;     default 256
//	TEGOLA_OPTIONS=DetachedWriteTimeoutMs=10000   # bound on writes;   default 10000, 0 disables
//	TEGOLA_OPTIONS=DetachedWriteDrainMs=5000      # shutdown drain;    default 5000,  0 disables
//
// Integers throughout, and the milliseconds carry their unit in the key name
// for a second reason beyond clarity: this parser's delimiter set is ",.\t \n",
// so it splits on "." and a duration string like "1.5s" would silently truncate
// to 1.
//
// Parsed here rather than in atlas — which is where TEGOLA_OPTIONS is otherwise
// read — because this is the package that owns the pool. Having atlas parse
// them would mean atlas pushing values into cache at init time, which is both
// an ordering dependency and more knowledge of caches than atlas needs.
const (
	// defaultDetachedWriteSlots is a sensible middle. In-flight writes are
	// miss_rate × write_duration × tiers, which spans two orders of magnitude
	// across deployments, so this is 2–4× too small for the largest ones —
	// hence the knob.
	//
	// It is also the one knob that can exhaust process memory: worst-case live
	// write buffers are slots × tiers × avg_tile_size, so 256 is roughly 100 MB
	// with two tiers and 200 KB tiles, and 1024 is roughly 400 MB.
	defaultDetachedWriteSlots = 256

	// defaultDetachedWriteTimeoutMs bounds slot occupancy, not a request — the
	// user's request finished long before. On by default because it applies to
	// work nobody is waiting on, so it costs nothing against the requirement
	// that no response waits on a cache save.
	//
	// 10s is generous rather than tight: far above any healthy tile write to
	// any supported backend, so it fires only on genuinely stuck requests.
	// A tighter bound would start failing writes on a merely congested durable
	// tier, converting a latency problem into a data-loss problem.
	defaultDetachedWriteTimeoutMs = 10000

	// defaultDetachedWriteDrainMs fits inside the 30s default Kubernetes
	// terminationGracePeriodSeconds with room for the rest of shutdown.
	defaultDetachedWriteDrainMs = 5000
)

var (
	detachedWriteSlots   = defaultDetachedWriteSlots
	detachedWriteTimeout = time.Duration(defaultDetachedWriteTimeoutMs) * time.Millisecond
	detachedWriteDrain   = time.Duration(defaultDetachedWriteDrainMs) * time.Millisecond
)

func init() {
	parseOptions(env.Getenv("OPTIONS"))
}

func parseOptions(raw string) {
	options := strings.ToLower(raw)

	if v, ok := optionInt(options, "detachedwriteslots"); ok {
		if v > 0 {
			detachedWriteSlots = v
		} else {
			log.Errorf("invalid value for DetachedWriteSlots (%v). using default (%v).", v, defaultDetachedWriteSlots)
		}
	}

	// Zero is meaningful for both durations — it disables the bound — so only a
	// negative or unparseable value falls back. Falling back rather than
	// disabling matters: the failure mode of an unbounded write is a
	// permanently exhausted pool, and a typo must not reach it.
	if v, ok := optionInt(options, "detachedwritetimeoutms"); ok {
		if v >= 0 {
			detachedWriteTimeout = time.Duration(v) * time.Millisecond
		} else {
			log.Errorf("invalid value for DetachedWriteTimeoutMs (%v). using default (%v).", v, defaultDetachedWriteTimeoutMs)
		}
	}

	if v, ok := optionInt(options, "detachedwritedrainms"); ok {
		if v >= 0 {
			detachedWriteDrain = time.Duration(v) * time.Millisecond
		} else {
			log.Errorf("invalid value for DetachedWriteDrainMs (%v). using default (%v).", v, defaultDetachedWriteDrainMs)
		}
	}
}

// optionDelimiters separates TEGOLA_OPTIONS entries.
//
// Deliberately *not* the set atlas uses for SimplifyMaxZoom, which also splits
// on ".". Splitting on "." makes `DetachedWriteTimeoutMs=1.5s` read as the
// token "1" and parse cleanly to a 1 ms write bound — every write killed
// instantly, silently, from a plausible typo. Leaving "." in the token instead
// makes Atoi fail, and a failed parse falls back to the safe default.
const optionDelimiters = ",\t \n"

// optionInt reads `key=<int>` out of a lowercased TEGOLA_OPTIONS string.
func optionInt(options, key string) (int, bool) {
	idx := strings.Index(options, key+"=")
	if idx == -1 {
		return 0, false
	}
	idx += len(key) + 1

	eidx := strings.IndexAny(options[idx:], optionDelimiters)
	if eidx == -1 {
		eidx = len(options)
	} else {
		eidx += idx
	}

	v, err := strconv.Atoi(options[idx:eidx])
	if err != nil {
		log.Errorf("invalid value for %v (%v). using the default.", key, options[idx:eidx])
		return 0, false
	}

	return v, true
}

// DetachedWriteDrain is how long the pool drain waits for in-flight writes at
// shutdown. Zero disables the drain entirely, which is the behaviour before it
// existed: every process exit discards up to a poolful of writes.
func DetachedWriteDrain() time.Duration { return detachedWriteDrain }
