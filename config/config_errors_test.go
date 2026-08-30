package config_test

import (
	"strings"
	"testing"

	"github.com/MapColonies/shigola/config"
)

// TestErrUnknownProviderTypeMessage pins what a config naming a provider type
// this build does not serve actually tells the operator.
//
// It is the only thing they see: an unknown type is rejected by Validate before
// anything is registered, so the message has to name the offending type and the
// provider entry it came from, the right way round.
func TestErrUnknownProviderTypeMessage(t *testing.T) {
	err := config.ErrUnknownProviderType{
		Name:           "provider1",
		Type:           "hana",
		KnownProviders: []string{"debug", "mvt_postgis"},
	}

	got := err.Error()

	for _, want := range []string{
		"invalid type (hana)",
		"for provider provider1",
		"debug,mvt_postgis",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, want it to contain %q", got, want)
		}
	}
}
