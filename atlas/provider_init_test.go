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

	// The in-process providers that fed the Go-side encode path (MAPCO-11491).
	// They were never a data source: debug drew each tile's own outline, and the
	// other two existed for tests. They are listed here for the same reason as
	// the rest -- so that re-registering one fails loudly rather than quietly
	// restoring a second tile-production path.
	"debug",
	"test",
	"emptycollection",
	// Note "test", not "mvt_test": the standard registration is what was
	// removed from provider/test. Its MVT half is still registered, and is in
	// the expected list below.
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
// mvt_test is not part of what ships: it comes from provider/test, which only
// this test binary imports. It is listed because Drivers reports what is
// registered, not what a release contains.
//
// Every name here now carries the mvt_ prefix, and that is the shape of the
// change MAPCO-11491 made: registration has one kind of entry again, because
// the standard provider interface is gone.
func TestCheckProviderTypes(t *testing.T) {
	got := provider.Drivers()
	sort.Strings(got)

	exp := []string{"mvt_postgis", "mvt_test"}
	sort.Strings(exp)

	if !reflect.DeepEqual(got, exp) {
		t.Errorf("registered providers, expected %v got %v", exp, got)
	}
}
