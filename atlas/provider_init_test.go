package atlas

import (
	"slices"
	"testing"

	"github.com/MapColonies/shigola/provider"
)

// removedProviderTypes are the provider types this fork used to serve and has
// deliberately stopped serving. Each entry is a ticket's worth of deletion.
var removedProviderTypes = []string{
	"hana",     // MAPCO-11487
	"mvt_hana", // MAPCO-11487
	"gpkg",     // MAPCO-11488
}

// TestRemovedProviderTypes is what makes a provider removal stick.
//
// A provider registers itself from an init function reached through a blank
// import in this package, so deleting one breaks nothing anywhere else in the
// tree and nothing would notice it coming back. config.Validate rejects a type
// it cannot find in provider.Drivers, which means "a config naming this type
// fails at startup" is true exactly while the type is absent from that list --
// this is the assertion behind that sentence.
func TestRemovedProviderTypes(t *testing.T) {
	registered := provider.Drivers()

	for _, typ := range removedProviderTypes {
		if slices.Contains(registered, typ) {
			t.Errorf("provider type %q is registered again; it was removed, and a config naming it must fail at startup", typ)
		}
	}
}
