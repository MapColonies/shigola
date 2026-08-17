package tms

// Ported from morecantile/tests/test_models.py (MIT, Development Seed). The
// per-grid expectations below are morecantile's own golden values, which is what
// makes them a check on this port rather than a restatement of it.

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
)

// bundledGridCount is the number of grid definitions morecantile 7.0.3 ships,
// all of which this package embeds.
const bundledGridCount = 13

func TestBundledGridsParse(t *testing.T) {
	ids := BundledIDs()
	if len(ids) != bundledGridCount {
		t.Fatalf("embedded %d grids, want %d: %v", len(ids), bundledGridCount, ids)
	}

	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			grid, err := LoadGrid(id)
			if err != nil {
				t.Fatalf("LoadGrid(%q): %v", id, err)
			}

			if grid.ID() != id {
				t.Errorf("grid id = %q, want %q (filename and document id must agree)", grid.ID(), id)
			}

			if len(grid.Definition().TileMatrices) == 0 {
				t.Error("grid has no tile matrices")
			}

			// Matrix order must follow zoom order after parsing.
			for i, m := range grid.Definition().TileMatrices {
				z, err := m.Zoom()
				if err != nil {
					t.Fatalf("tileMatrix %d has a non-integer id: %v", i, err)
				}

				if i > 0 {
					prev, _ := grid.Definition().TileMatrices[i-1].Zoom()
					if z <= prev {
						t.Errorf("tileMatrices are not sorted: index %d has zoom %d after %d", i, z, prev)
					}
				}
			}
		})
	}
}

// TestQuadkeySupport is morecantile's test_quadkey_support.
func TestQuadkeySupport(t *testing.T) {
	expected := map[string]bool{
		"LINZAntarticaMapTilegrid": false,
		"EuropeanETRS89_LAEAQuad":  true,
		"CanadianNAD83_LCC":        false,
		"UPSArcticWGS84Quad":       true,
		"NZTM2000Quad":             true,
		"UTM31WGS84Quad":           false,
		"UPSAntarcticWGS84Quad":    true,
		"WorldMercatorWGS84Quad":   true,
		"WGS1984Quad":              false,
		"WorldCRS84Quad":           false,
		"WebMercatorQuad":          true,
		"CDB1GlobalGrid":           false,
		"GNOSISGlobalGrid":         false,
	}

	assertGridFlags(t, expected, func(grid *TileMatrixSet) bool { return grid.IsQuadtree() })
}

// TestVariableTMS is morecantile's test_variable_tms.
func TestVariableTMS(t *testing.T) {
	expected := map[string]bool{
		"LINZAntarticaMapTilegrid": false,
		"EuropeanETRS89_LAEAQuad":  false,
		"CanadianNAD83_LCC":        false,
		"UPSArcticWGS84Quad":       false,
		"NZTM2000Quad":             false,
		"UTM31WGS84Quad":           false,
		"UPSAntarcticWGS84Quad":    false,
		"WorldMercatorWGS84Quad":   false,
		"WorldCRS84Quad":           false,
		"WGS1984Quad":              false,
		"WebMercatorQuad":          false,
		"CDB1GlobalGrid":           true,
		"GNOSISGlobalGrid":         true,
	}

	assertGridFlags(t, expected, func(grid *TileMatrixSet) bool { return grid.IsVariable() })
}

// TestInvertedTMS is morecantile's test_inverted_tms. Axis inversion is the
// subtlest thing the port has to get right: a grid with inverted axes states its
// pointOfOrigin as (lat, lon), so every coordinate would be transposed if this
// were wrong.
func TestInvertedTMS(t *testing.T) {
	expected := map[string]bool{
		"LINZAntarticaMapTilegrid": true,
		"EuropeanETRS89_LAEAQuad":  true,
		"CanadianNAD83_LCC":        false,
		"UPSArcticWGS84Quad":       false,
		"NZTM2000Quad":             true,
		"UTM31WGS84Quad":           false,
		"UPSAntarcticWGS84Quad":    false,
		"WorldMercatorWGS84Quad":   false,
		"WorldCRS84Quad":           false,
		"WGS1984Quad":              true,
		"WebMercatorQuad":          false,
		"CDB1GlobalGrid":           true,
		"GNOSISGlobalGrid":         true,
	}

	assertGridFlags(t, expected, func(grid *TileMatrixSet) bool { return grid.invertAxis })
}

// assertGridFlags checks a boolean property across every bundled grid.
func assertGridFlags(t *testing.T, expected map[string]bool, probe func(*TileMatrixSet) bool) {
	t.Helper()

	if len(expected) != bundledGridCount {
		t.Fatalf("expectation table covers %d grids, want all %d", len(expected), bundledGridCount)
	}

	for id, want := range expected {
		t.Run(id, func(t *testing.T) {
			grid, err := LoadGrid(id)
			if err != nil {
				t.Fatalf("LoadGrid(%q): %v", id, err)
			}

			if got := probe(grid); got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// TestCoalesceFactor is morecantile's test_coalesce.
func TestCoalesceFactor(t *testing.T) {
	matrix := TileMatrix{
		ID:               "2",
		ScaleDenominator: 34942641.501794859767,
		CellSize:         0.087890625,
		CornerOfOrigin:   CornerTopLeft,
		PointOfOrigin:    [2]float64{90, -180},
		MatrixWidth:      16,
		MatrixHeight:     8,
		TileWidth:        256,
		TileHeight:       256,
		VariableMatrixWidths: []VariableMatrixWidth{
			{Coalesce: 4, MinTileRow: 0, MaxTileRow: 0},
			{Coalesce: 2, MinTileRow: 1, MaxTileRow: 1},
			{Coalesce: 2, MinTileRow: 6, MaxTileRow: 6},
			{Coalesce: 4, MinTileRow: 7, MaxTileRow: 7},
		},
	}

	for row, want := range map[int64]int64{0: 4, 1: 2, 3: 1, 6: 2, 7: 4} {
		got, err := matrix.CoalesceFactor(row)
		if err != nil {
			t.Fatalf("CoalesceFactor(%d): %v", row, err)
		}

		if got != want {
			t.Errorf("CoalesceFactor(%d) = %d, want %d", row, got, want)
		}
	}

	if _, err := matrix.CoalesceFactor(8); err == nil {
		t.Error("expected an error for a row past matrixHeight, got nil")
	}

	if _, err := matrix.CoalesceFactor(-1); err == nil {
		t.Error("expected an error for a negative row, got nil")
	}

	// A matrix without variableMatrixWidths coalesces nothing. morecantile
	// raises here; returning 1 is equivalent for every caller in this package
	// and spares them a special case, so that difference is deliberate.
	plain := matrix
	plain.VariableMatrixWidths = nil

	got, err := plain.CoalesceFactor(0)
	if err != nil {
		t.Fatalf("CoalesceFactor on a fixed-width matrix: %v", err)
	}

	if got != 1 {
		t.Errorf("CoalesceFactor on a fixed-width matrix = %d, want 1", got)
	}
}

// TestRejectVersion1 is morecantile's check_for_old_specification validator: a
// TMS 1.0 document must be refused rather than half-understood.
//
// Like morecantile's model validator, the check inspects the document's top
// level, which is where a 1.0 document carries supportedCRS and topLeftCorner.
func TestRejectVersion1(t *testing.T) {
	for _, doc := range []string{
		`{"identifier":"Test","supportedCRS":"http://www.opengis.net/def/crs/EPSG/0/3857","tileMatrix":[]}`,
		`{"identifier":"Test","topLeftCorner":[0,0],"tileMatrix":[]}`,
	} {
		if _, err := ParseDefinition([]byte(doc)); !errors.Is(err, ErrTMSVersion1) {
			t.Errorf("ParseDefinition(%s) error = %v, want ErrTMSVersion1", doc, err)
		}
	}

	// A 1.0 keyword nested inside a tileMatrix is caught by the matrix's closed
	// member set instead, exactly as pydantic's extra="forbid" does upstream.
	nested := `{"id":"Test","crs":"http://www.opengis.net/def/crs/EPSG/0/3857",` +
		`"tileMatrices":[{"topLeftCorner":[0,0]}]}`

	if _, err := ParseDefinition([]byte(nested)); err == nil {
		t.Error("expected an error for a tileMatrix carrying topLeftCorner, got nil")
	}
}

func TestParseDefinitionRejectsUnknownMatrixMembers(t *testing.T) {
	doc := `{
	  "id": "Test",
	  "crs": "http://www.opengis.net/def/crs/EPSG/0/3857",
	  "tileMatrices": [{
	    "id": "0", "scaleDenominator": 1, "cellSize": 1,
	    "pointOfOrigin": [0, 0], "tileWidth": 256, "tileHeight": 256,
	    "matrixWidth": 1, "matrixHeight": 1, "surpriseMember": true
	  }]
	}`

	if _, err := ParseDefinition([]byte(doc)); err == nil {
		t.Error("expected an error for an unknown tileMatrix member, got nil")
	}
}

func TestParseDefinitionDefaultsCornerOfOrigin(t *testing.T) {
	// WorldCRS84Quad omits cornerOfOrigin, which must default to topLeft — get
	// this wrong and every row index flips.
	def, _, err := LoadDefinition("WorldCRS84Quad")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	for _, m := range def.TileMatrices {
		if m.CornerOfOrigin != CornerTopLeft {
			t.Fatalf("tileMatrix %q cornerOfOrigin = %q, want %q",
				m.ID, m.CornerOfOrigin, CornerTopLeft)
		}
	}
}

func TestDefinitionJSONIsVerbatim(t *testing.T) {
	// /tileMatrixSets/{id} must serve the canonical OGC document, not a
	// re-encoding of our model, so the bytes have to survive the round trip.
	for _, id := range BundledIDs() {
		t.Run(id, func(t *testing.T) {
			grid, err := LoadGrid(id)
			if err != nil {
				t.Fatalf("LoadGrid: %v", err)
			}

			got, err := grid.DefinitionJSON()
			if err != nil {
				t.Fatalf("DefinitionJSON: %v", err)
			}

			want, err := gridData.ReadFile("data/" + id + ".json")
			if err != nil {
				t.Fatalf("reading embedded definition: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Error("DefinitionJSON did not return the embedded document unchanged")
			}
		})
	}
}

func TestDefinitionRoundTripsThroughModel(t *testing.T) {
	// Marshalling the parsed model must produce a document that parses back to
	// the same model, so a grid built in memory can still be served.
	for _, id := range BundledIDs() {
		t.Run(id, func(t *testing.T) {
			def, _, err := LoadDefinition(id)
			if err != nil {
				t.Fatalf("LoadDefinition: %v", err)
			}

			encoded, err := json.Marshal(def)
			if err != nil {
				t.Fatalf("marshalling definition: %v", err)
			}

			reparsed, err := ParseDefinition(encoded)
			if err != nil {
				t.Fatalf("re-parsing marshalled definition: %v", err)
			}

			if !reflect.DeepEqual(def, reparsed) {
				t.Error("definition did not survive a marshal/parse round trip")
			}
		})
	}
}

// TestMatrixZoomIDs pins that each grid's matrix ids are the contiguous zoom
// range its min and max zoom advertise, which the tile handlers rely on.
func TestMatrixZoomIDs(t *testing.T) {
	for _, id := range BundledIDs() {
		t.Run(id, func(t *testing.T) {
			grid, err := LoadGrid(id)
			if err != nil {
				t.Fatalf("LoadGrid: %v", err)
			}

			matrices := grid.Definition().TileMatrices

			for i, m := range matrices {
				want := grid.MinZoom() + i
				if m.ID != strconv.Itoa(want) {
					t.Fatalf("tileMatrix at index %d has id %q, want %q", i, m.ID, strconv.Itoa(want))
				}
			}

			if got := grid.MaxZoom(); got != grid.MinZoom()+len(matrices)-1 {
				t.Errorf("MaxZoom = %d, want %d", got, grid.MinZoom()+len(matrices)-1)
			}
		})
	}
}
