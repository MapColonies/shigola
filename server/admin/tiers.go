package admin

import (
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/file"
)

// UnexpiringPodTiers names the file tiers of c with no ttl. A file tier lives
// on one pod, so a purge handled by another pod never reaches it, and without
// a ttl it serves a purged tile indefinitely.
func UnexpiringPodTiers(c cache.Interface) []string {
	return unexpiring(c, "")
}

func unexpiring(c cache.Interface, path string) []string {
	if tiered, ok := cache.TieredOf(c); ok {
		var out []string
		for _, tier := range tiered.Tiers() {
			name := tier.Name
			if path != "" {
				name = path + "/" + name
			}
			out = append(out, unexpiring(tier.Cache, name)...)
		}
		return out
	}

	if fc, ok := cache.Innermost(c).(*file.Cache); ok && fc.Expiration == 0 {
		if path == "" {
			path = "file"
		}
		return []string{path}
	}

	return nil
}
