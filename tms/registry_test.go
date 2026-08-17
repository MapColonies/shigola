package tms

// Registry behaviour. Partly ported from morecantile/tests/test_models.py
// (test_invalid_tms) and test_morecantile.py (test_default_grids, test_register),
// and partly specific to this port's activation policy (ADR-0009).

import (
	"errors"
	"slices"
	"testing"
)

// TestDefaultRegistryKnowsEveryBundledGrid is morecantile's test_default_grids,
// adjusted for this port: every bundled grid is registered, whether or not this
// build can serve it, so an operator asking for a real OGC grid gets told why it
// is unavailable rather than that it does not exist.
func TestDefaultRegistryKnowsEveryBundledGrid(t *testing.T) {
	registered := Registered()
	if len(registered) != bundledGridCount {
		t.Errorf("Registered() has %d grids, want %d: %v", len(registered), bundledGridCount, registered)
	}

	for _, id := range BundledIDs() {
		if !containsString(registered, id) {
			t.Errorf("bundled grid %q is not registered", id)
		}
	}
}

// TestActiveGrids pins the activation set design.md Phase 0 specifies: exactly
// the three grids whose coordinate conversions are closed-form over WGS 84, so
// the fork needs no PROJ and stays cgo-free.
func TestActiveGrids(t *testing.T) {
	want := []string{"WGS1984Quad", "WebMercatorQuad", "WorldCRS84Quad"}

	var got []string
	for _, grid := range List() {
		got = append(got, grid.ID())
	}

	if len(got) != len(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestGatedGridsReportWhy checks that each gated grid explains itself. The two
// reasons are genuinely different, and conflating them would misdescribe the
// variable-width grids as lacking a transform they in fact have.
func TestGatedGridsReportWhy(t *testing.T) {
	tests := map[string]error{
		"EuropeanETRS89_LAEAQuad":  ErrNoTransformBackend,
		"CanadianNAD83_LCC":        ErrNoTransformBackend,
		"UTM31WGS84Quad":           ErrNoTransformBackend,
		"NZTM2000Quad":             ErrNoTransformBackend,
		"UPSArcticWGS84Quad":       ErrNoTransformBackend,
		"UPSAntarcticWGS84Quad":    ErrNoTransformBackend,
		"LINZAntarticaMapTilegrid": ErrNoTransformBackend,
		"WorldMercatorWGS84Quad":   ErrNoTransformBackend,
		"GNOSISGlobalGrid":         ErrVariableWidthUnsupported,
		"CDB1GlobalGrid":           ErrVariableWidthUnsupported,
	}

	if len(tests)+len(activeGrids) != bundledGridCount {
		t.Fatalf("%d gated plus %d active grids does not account for all %d bundled grids",
			len(tests), len(activeGrids), bundledGridCount)
	}

	for id, wantReason := range tests {
		t.Run(id, func(t *testing.T) {
			_, err := Get(id)
			if err == nil {
				t.Fatalf("Get(%q) succeeded, want it gated", id)
			}

			var unavailable GridUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("Get(%q) error = %v, want GridUnavailableError", id, err)
			}

			if !errors.Is(err, wantReason) {
				t.Errorf("Get(%q) reason = %v, want %v", id, err, wantReason)
			}

			if Available(id) {
				t.Errorf("Available(%q) = true, want false", id)
			}
		})
	}
}

// TestGridNotActivatedReason covers the gating branch no bundled grid currently
// reaches: a grid this build could serve but has not activated.
//
// It is worth pinning because the branch is a trap. Wiring a Transformer for, say,
// EPSG:3395 would move WorldMercatorWGS84Quad into it, and if it borrowed
// ErrNoTransformBackend the grid would report having no transform while holding
// one.
func TestGridNotActivatedReason(t *testing.T) {
	def, raw, err := LoadDefinition("WorldCRS84Quad")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	// Geographic CRS, so a transform exists; fixed-width, so not variable; and an
	// id outside the activation set.
	def.ID = "SomeUnactivatedGrid"

	grid, err := New(def, raw)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !grid.TransformAvailable() {
		t.Fatal("the grid should have a transform, or this test proves nothing")
	}

	if grid.IsVariable() {
		t.Fatal("the grid should be fixed-width, or this test proves nothing")
	}

	reason := gatingReason(grid)
	if !errors.Is(reason, ErrGridNotActivated) {
		t.Errorf("gatingReason = %v, want ErrGridNotActivated", reason)
	}

	if errors.Is(reason, ErrNoTransformBackend) {
		t.Error("a grid that has a transform must not report ErrNoTransformBackend")
	}
}

// TestInvalidIdentifier is morecantile's test_invalid_tms. An unknown id must be
// distinguishable from a known-but-gated one.
func TestInvalidIdentifier(t *testing.T) {
	var invalid InvalidIdentifierError

	if _, err := Get("ANotValidName"); !errors.As(err, &invalid) {
		t.Errorf("Get(\"ANotValidName\") error = %v, want InvalidIdentifierError", err)
	}

	var unavailable GridUnavailableError
	if _, err := Get("ANotValidName"); errors.As(err, &unavailable) {
		t.Error("an unknown id must not be reported as an unavailable grid")
	}
}

// TestRegisterFactory checks the property ADR-0008 calls load-bearing: a grid
// arrives by registering a factory, with no change to any call site.
func TestRegisterFactory(t *testing.T) {
	registry := NewRegistry()

	def, raw, err := LoadDefinition("WebMercatorQuad")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	calls := 0

	if err := registry.Register("MyGrid", func() (*TileMatrixSet, error) {
		calls++

		return New(def, raw)
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Registration must be lazy: nothing is built until the grid is asked for.
	if calls != 0 {
		t.Errorf("factory called %d times before any Get, want 0", calls)
	}

	grid, err := registry.Get("MyGrid")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if grid.ID() != "WebMercatorQuad" {
		t.Errorf("grid id = %q, want WebMercatorQuad", grid.ID())
	}

	// The outcome is cached, so a repeated Get does not rebuild.
	if _, err := registry.Get("MyGrid"); err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if calls != 1 {
		t.Errorf("factory called %d times, want 1", calls)
	}

	// A duplicate id is refused rather than silently shadowing.
	if err := registry.Register("MyGrid", func() (*TileMatrixSet, error) {
		return New(def, raw)
	}); err == nil {
		t.Error("expected an error registering a duplicate id, got nil")
	}

	if err := registry.Register("", nil); err == nil {
		t.Error("expected an error registering an empty id, got nil")
	}
}

// TestRegistryCachesFailures checks a failing factory is not retried, so an
// unavailable grid reports the same reason every time.
func TestRegistryCachesFailures(t *testing.T) {
	registry := NewRegistry()

	calls := 0
	sentinel := errors.New("boom")

	if err := registry.Register("Broken", func() (*TileMatrixSet, error) {
		calls++

		return nil, sentinel
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := registry.Get("Broken"); !errors.Is(err, sentinel) {
			t.Fatalf("Get error = %v, want the factory's error", err)
		}
	}

	if calls != 1 {
		t.Errorf("factory called %d times, want 1", calls)
	}

	// A broken grid is registered but must not be listed as servable.
	if len(registry.List()) != 0 {
		t.Errorf("List() = %v, want empty", registry.List())
	}

	if !containsString(registry.Registered(), "Broken") {
		t.Error("a broken grid should still appear in Registered()")
	}
}

// TestReplaceGrid checks the override path, which a deployment needs to swap in
// a locally-corrected definition.
func TestReplaceGrid(t *testing.T) {
	registry := NewRegistry()

	def, raw, err := LoadDefinition("WorldCRS84Quad")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	first := errors.New("first")

	if err := registry.Register("Grid", func() (*TileMatrixSet, error) {
		return nil, first
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := registry.Get("Grid"); !errors.Is(err, first) {
		t.Fatalf("Get error = %v, want %v", err, first)
	}

	if err := registry.Replace("Grid", func() (*TileMatrixSet, error) {
		return New(def, raw)
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	// Replace must invalidate the cached failure.
	grid, err := registry.Get("Grid")
	if err != nil {
		t.Fatalf("Get after Replace: %v", err)
	}

	if grid.ID() != "WorldCRS84Quad" {
		t.Errorf("grid id = %q, want WorldCRS84Quad", grid.ID())
	}

	if n := len(registry.Registered()); n != 1 {
		t.Errorf("Registered() has %d entries after Replace, want 1", n)
	}
}

// TestGridsAreIndependentInstances guards against callers mutating a shared
// grid's definition through a returned slice.
func TestGridsAreIndependentInstances(t *testing.T) {
	grid := mustGrid(t, "WebMercatorQuad")

	axes := grid.OrderedAxes()
	if len(axes) != 2 {
		t.Fatalf("OrderedAxes() = %v, want two entries", axes)
	}

	axes[0] = "mutated"

	if again := grid.OrderedAxes(); again[0] == "mutated" {
		t.Error("OrderedAxes() exposes the grid's own slice; callers can corrupt it")
	}

	json, err := grid.DefinitionJSON()
	if err != nil {
		t.Fatalf("DefinitionJSON: %v", err)
	}

	json[0] = 'X'

	again, err := grid.DefinitionJSON()
	if err != nil {
		t.Fatalf("DefinitionJSON: %v", err)
	}

	if again[0] == 'X' {
		t.Error("DefinitionJSON() exposes the grid's own buffer; callers can corrupt it")
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}

// TestAvailableIDsOrder pins the ordering contract: a caller that defaults to
// "every available grid" — a map's tiling schemes, for one — takes the first
// entry as the default, and tegola's default has always been WebMercatorQuad.
// Plain sorting would put WGS1984Quad first, since 'G' sorts before 'e'.
func TestAvailableIDsOrder(t *testing.T) {
	ids := AvailableIDs()

	if len(ids) == 0 {
		t.Fatal("AvailableIDs is empty")
	}

	if ids[0] != WebMercatorQuad {
		t.Errorf("AvailableIDs()[0] = %q, want %q", ids[0], WebMercatorQuad)
	}

	for _, id := range ids {
		if !Available(id) {
			t.Errorf("AvailableIDs contains %q, which is not available", id)
		}
	}

	// the gated grids stay out of the list a config defaults to
	for _, id := range []string{"NZTM2000Quad", "CDB1GlobalGrid"} {
		if slices.Contains(ids, id) {
			t.Errorf("AvailableIDs contains gated grid %q", id)
		}
	}

	if rest := ids[1:]; !slices.IsSorted(rest) {
		t.Errorf("AvailableIDs after the default is not sorted: %v", rest)
	}
}
