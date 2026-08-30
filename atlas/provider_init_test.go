package atlas

import (
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/MapColonies/shigola/provider"
)

// removedProviderTypes are the provider types this fork used to serve and has
// deliberately stopped serving. Each entry is a ticket's worth of deletion.
//
// This is a longer list than provider.removedProviders, and deliberately so:
// that one records only the types with a named successor to redirect a config
// to, while this one is every type that must not come back, successor or not.
var removedProviderTypes = []string{
	"hana",     // MAPCO-11487
	"mvt_hana", // MAPCO-11487
	"gpkg",     // MAPCO-11488
	"postgis",  // MAPCO-11490, replaced by mvt_postgis
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

// TestCheckProviderTypes pins the provider types a shigola binary accepts.
//
// It is the companion to TestCheckCacheTypes, and it is the other half of
// TestRemovedProviderTypes: that one catches a type coming back, this one
// catches a type arriving. Both std and mvt names are listed, so a type
// registered under either shows up here.
//
// test, mvt_test and emptycollection are not part of what ships: they come from
// provider/test, which only this test binary imports. They are listed because
// Drivers reports what is registered, not what a release contains.
func TestCheckProviderTypes(t *testing.T) {
	got := provider.Drivers()
	sort.Strings(got)

	exp := []string{"debug", "emptycollection", "mvt_postgis", "mvt_test", "test"}
	sort.Strings(exp)

	if !reflect.DeepEqual(got, exp) {
		t.Errorf("registered providers, expected %v got %v", exp, got)
	}
}
