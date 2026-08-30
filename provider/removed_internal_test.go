package provider

import (
	"errors"
	"testing"
)

// TestRegisterRemoved covers the registry that turns "unknown provider type"
// into "that type moved, here is its successor".
//
// The For path matters even though config.Validate normally rejects a removed
// type first: Validate is a check a caller can skip, and register.Providers
// calls For regardless. The two should not disagree about what happened.
func TestRegisterRemoved(t *testing.T) {
	const (
		name        = "removed_test_provider"
		replacement = "mvt_removed_test_provider"
	)

	t.Cleanup(func() { delete(removedProviders, name) })

	if err := RegisterRemoved(name, replacement); err != nil {
		t.Fatalf("RegisterRemoved(%v, %v) = %v, want nil", name, replacement, err)
	}

	t.Run("a removed type is not a driver", func(t *testing.T) {
		for _, drv := range Drivers() {
			if drv == name {
				t.Errorf("Drivers() lists %v, which has been removed", name)
			}
		}
	})

	t.Run("Removed reports the successor", func(t *testing.T) {
		got, ok := Removed(name)
		if !ok {
			t.Fatalf("Removed(%v) = _, false, want true", name)
		}
		if got != replacement {
			t.Errorf("Removed(%v) = %v, want %v", name, got, replacement)
		}
	})

	t.Run("For names the successor rather than listing every driver", func(t *testing.T) {
		_, err := For(name, nil, nil)

		var removed ErrRemovedProvider
		if !errors.As(err, &removed) {
			t.Fatalf("For(%v) = %T (%v), want ErrRemovedProvider", name, err, err)
		}
		if removed.Replacement != replacement {
			t.Errorf("Replacement = %v, want %v", removed.Replacement, replacement)
		}
	})

	t.Run("a successor is required", func(t *testing.T) {
		if err := RegisterRemoved("another_removed_test_provider", ""); !errors.Is(err, ErrNilReplacement) {
			t.Errorf("RegisterRemoved with no replacement = %v, want ErrNilReplacement", err)
		}
	})
}
