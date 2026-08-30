package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/internal/env"
	_ "github.com/MapColonies/shigola/provider/postgis"
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

// TestRemovedProviderTypeIsRejected covers the standard PostGIS type, which was
// removed in favour of mvt_postgis (MAPCO-11490).
//
// A config naming it is not a config naming a typo: it is a working
// configuration written against a build that served it, and the operator's next
// move is to change one word. An "unknown type, here are the known ones" error
// makes them guess which of the known ones took over; this one says.
func TestRemovedProviderTypeIsRejected(t *testing.T) {
	cfg := config.Config{
		Providers: []env.Dict{
			{
				"name": "provider1",
				"type": "postgis",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want a config naming the removed postgis type to be rejected")
	}

	var removed config.ErrRemovedProviderType
	if !errors.As(err, &removed) {
		t.Fatalf("Validate() = %T (%v), want config.ErrRemovedProviderType", err, err)
	}

	if removed.Type != "postgis" {
		t.Errorf("Type = %q, want %q", removed.Type, "postgis")
	}
	if removed.Replacement != "mvt_postgis" {
		t.Errorf("Replacement = %q, want %q", removed.Replacement, "mvt_postgis")
	}

	for _, want := range []string{"postgis", "mvt_postgis", "provider1"} {
		if !strings.Contains(removed.Error(), want) {
			t.Errorf("Error() = %q, want it to contain %q", removed.Error(), want)
		}
	}
}
