package cacheops

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/multi"
)

// TiersAll lifts the write-tier restriction entirely.
const TiersAll = "all"

// ResolveWriteTiers turns a tiers spec into Options.WriteTiers, or nil for "no
// restriction".
//
//	empty      the last tier in read order — the durable one by construction
//	all        every tier
//	a,b,c      exactly those, validated against the whole tree
//
// The default is the last tier rather than every tier because the alternative
// floods the hot tier with cold tiles in seed order, evicting the live working
// set — the exact harm the layered cache exists to avoid. "Durable" resisted
// definition (a file tier can carry a ttl; an s3 bucket under a lifecycle policy
// has none yet is not permanent), and position needs no inference: read order
// runs hot to durable, so the last tier *is* the durable one.
//
// It rests on that ordering, and nothing enforces it. A chain of s3 then redis
// is legal, and makes this write the hot tier and skip the durable one.
//
// A single-backend cache is unaffected: one cache is also the last cache, so
// there is nothing to restrict.
func ResolveWriteTiers(c cache.Interface, flag string) ([]string, error) {
	known := multi.TierNames(c)

	flag = strings.TrimSpace(flag)

	switch {
	case len(known) == 0:
		// not a chain; there is nothing to target
		if flag != "" && !strings.EqualFold(flag, TiersAll) {
			return nil, fmt.Errorf("tiers %q: the configured cache has no tiers", flag)
		}
		return nil, nil

	case strings.EqualFold(flag, TiersAll):
		return nil, nil

	case flag == "":
		last, ok := multi.LastTierName(c)
		if !ok {
			return nil, nil
		}
		return []string{last}, nil
	}

	var names []string
	for _, name := range strings.Split(flag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Validated at startup rather than silently no-oping at run time: a
		// typo in a cron job would otherwise look like a successful seed that
		// wrote nothing.
		if !slices.Contains(known, name) {
			return nil, multi.ErrUnknownTier{Name: name, Known: known}
		}
		names = append(names, name)
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("tiers %q names no tier. known tiers: %v", flag, known)
	}

	return names, nil
}
