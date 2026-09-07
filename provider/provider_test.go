package provider_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/provider/test"
)

func TestProviderInterface(t *testing.T) {
	if _, err := provider.For(test.MVTProviderType, nil, nil); err != nil {
		t.Errorf("retrieve provider err , expected nil got %v", err)
		return
	}
	if test.MVTCount != 1 {
		t.Errorf(" expected count , expected 1 got %v", test.MVTCount)
	}
	provider.Cleanup()
	if test.MVTCount != 0 {
		t.Errorf(" expected count , expected 0 got %v", test.MVTCount)
	}
}

// TestForUnknownProvider covers the other half of For: a name nothing
// registered is an error naming what is registered, not a nil provider the
// caller has to notice.
func TestForUnknownProvider(t *testing.T) {
	got, err := provider.For("nope", nil, nil)
	if err == nil {
		t.Fatalf("For(nope), expected an error got provider %v", got)
	}
	if got != nil {
		t.Errorf("For(nope) provider, expected nil got %v", got)
	}

	var unknown provider.ErrUnknownProvider
	if !errors.As(err, &unknown) {
		t.Fatalf("For(nope) err, expected ErrUnknownProvider got %T", err)
	}
	if !slices.Contains(unknown.KnownProviders, test.MVTProviderType) {
		t.Errorf("known providers %v, expected it to name %v", unknown.KnownProviders, test.MVTProviderType)
	}
}

func TestNewTileWorldCRS84QuadExtent(t *testing.T) {
	tile := provider.NewTile(0, 1, 0, 64, shigola.WGS84)
	ext, srid := tile.Extent()
	if srid != shigola.WGS84 {
		t.Fatalf("srid, expected %d got %d", shigola.WGS84, srid)
	}
	if got, expected := ext.Extent(), [4]float64{0, -90, 180, 90}; got != expected {
		t.Fatalf("extent, expected %v got %v", expected, got)
	}
}
