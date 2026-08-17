package tms

// The OGC Two Dimensional Tile Matrix Set 2.0 document model, ported from
// morecantile/models.py (MIT, Development Seed), which in turn tracks
// https://github.com/opengeospatial/2D-Tile-Matrix-Set/tree/master/schemas/tms/2.0/json
//
// This file is the document: what a grid definition says. The arithmetic that
// interprets it lives in tilematrixset.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Corner-of-origin values permitted by OGC 17-083r4.
const (
	CornerTopLeft    = "topLeft"
	CornerBottomLeft = "bottomLeft"
)

// VariableMatrixWidth describes a band of rows whose tiles coalesce, i.e. where
// several columns of the nominal matrix are served as a single wider tile. It is
// what makes the GNOSIS and CDB1 grids "variable width".
type VariableMatrixWidth struct {
	// Coalesce is the number of nominal columns that merge into one tile for
	// these rows. OGC requires it to be at least 2.
	Coalesce int64 `json:"coalesce"`
	// MinTileRow and MaxTileRow bound the row band, inclusive.
	MinTileRow int64 `json:"minTileRow"`
	MaxTileRow int64 `json:"maxTileRow"`
}

// validate checks the invariants OGC states for a variableMatrixWidth.
func (v VariableMatrixWidth) validate() error {
	if v.Coalesce < 2 {
		return fmt.Errorf("tms: variableMatrixWidth coalesce must be >= 2, got %d", v.Coalesce)
	}

	if v.MinTileRow < 0 || v.MaxTileRow < 0 {
		return fmt.Errorf("tms: variableMatrixWidth rows must not be negative, got %d..%d",
			v.MinTileRow, v.MaxTileRow)
	}

	if v.MaxTileRow < v.MinTileRow {
		return fmt.Errorf("tms: variableMatrixWidth maxTileRow (%d) is below minTileRow (%d)",
			v.MaxTileRow, v.MinTileRow)
	}

	return nil
}

// TileMatrix is one zoom level of a TileMatrixSet: the size of the matrix, the
// size of its tiles, and where its origin sits in the grid's CRS.
type TileMatrix struct {
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`

	// ID identifies the zoom level. OGC types it as a string matching
	// ^-?[0-9]+$ rather than an integer; Zoom parses it.
	ID string `json:"id"`

	ScaleDenominator float64 `json:"scaleDenominator"`
	CellSize         float64 `json:"cellSize"`

	// CornerOfOrigin is the corner used as the origin for numbering rows and
	// columns, either CornerTopLeft (the default when absent) or
	// CornerBottomLeft.
	CornerOfOrigin string `json:"cornerOfOrigin,omitempty"`

	// PointOfOrigin is the position of the corner of origin in the grid's CRS,
	// in the axis order the TileMatrixSet declares — so it is (lat, lon) for a
	// grid whose orderedAxes are inverted. Use TileMatrixSet.matrixOrigin to
	// read it as (x, y).
	PointOfOrigin [2]float64 `json:"pointOfOrigin"`

	TileWidth    int64 `json:"tileWidth"`
	TileHeight   int64 `json:"tileHeight"`
	MatrixWidth  int64 `json:"matrixWidth"`
	MatrixHeight int64 `json:"matrixHeight"`

	VariableMatrixWidths []VariableMatrixWidth `json:"variableMatrixWidths,omitempty"`
}

// tileMatrixAlias breaks the recursion in TileMatrix.UnmarshalJSON.
type tileMatrixAlias TileMatrix

// UnmarshalJSON decodes a tileMatrix, rejecting unknown members. OGC declares
// the tileMatrix object closed, and morecantile enforces that with
// extra="forbid"; a document with a stray member is more likely to be a typo
// than an extension.
func (m *TileMatrix) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()

	var alias tileMatrixAlias
	if err := dec.Decode(&alias); err != nil {
		return fmt.Errorf("tms: cannot decode tileMatrix: %w", err)
	}

	*m = TileMatrix(alias)

	if m.CornerOfOrigin == "" {
		m.CornerOfOrigin = CornerTopLeft
	}

	return nil
}

// validate checks the invariants OGC states for a tileMatrix.
func (m TileMatrix) validate() error {
	if _, err := m.Zoom(); err != nil {
		return err
	}

	switch m.CornerOfOrigin {
	case CornerTopLeft, CornerBottomLeft:
	default:
		return fmt.Errorf("tms: tileMatrix %q has invalid cornerOfOrigin %q, want %q or %q",
			m.ID, m.CornerOfOrigin, CornerTopLeft, CornerBottomLeft)
	}

	for _, dim := range []struct {
		name  string
		value int64
	}{
		{"tileWidth", m.TileWidth},
		{"tileHeight", m.TileHeight},
		{"matrixWidth", m.MatrixWidth},
		{"matrixHeight", m.MatrixHeight},
	} {
		if dim.value < 1 {
			return fmt.Errorf("tms: tileMatrix %q has %s %d, want >= 1", m.ID, dim.name, dim.value)
		}
	}

	for _, vmw := range m.VariableMatrixWidths {
		if err := vmw.validate(); err != nil {
			return fmt.Errorf("tileMatrix %q: %w", m.ID, err)
		}

		if vmw.MaxTileRow > m.MatrixHeight-1 {
			return fmt.Errorf(
				"tms: tileMatrix %q variableMatrixWidth maxTileRow (%d) exceeds matrixHeight-1 (%d)",
				m.ID, vmw.MaxTileRow, m.MatrixHeight-1)
		}
	}

	return nil
}

// Zoom parses the matrix identifier as an integer zoom level.
func (m TileMatrix) Zoom() (int, error) {
	z, err := strconv.Atoi(m.ID)
	if err != nil {
		return 0, fmt.Errorf("tms: tileMatrix id %q is not an integer: %w", m.ID, err)
	}

	return z, nil
}

// CoalesceFactor returns the number of nominal columns that coalesce into one
// tile on the given row, which is 1 for every row of a non-variable matrix.
//
// Ported from morecantile.models.TileMatrix.get_coalesce_factor.
func (m TileMatrix) CoalesceFactor(row int64) (int64, error) {
	if len(m.VariableMatrixWidths) == 0 {
		return 1, nil
	}

	if row < 0 {
		return 0, fmt.Errorf("tms: cannot find coalesce factor for negative row (%d)", row)
	}

	if row > m.MatrixHeight-1 {
		return 0, fmt.Errorf("tms: row %d is greater than the tileMatrix height (%d)",
			row, m.MatrixHeight)
	}

	for _, vmw := range m.VariableMatrixWidths {
		if vmw.MaxTileRow >= row && row >= vmw.MinTileRow {
			return vmw.Coalesce, nil
		}
	}

	return 1, nil
}

// TMSBoundingBox is the minimum bounding rectangle surrounding a
// TileMatrixSet, expressed in that set's CRS.
type TMSBoundingBox struct {
	LowerLeft   [2]float64 `json:"lowerLeft"`
	UpperRight  [2]float64 `json:"upperRight"`
	CRS         *CRS       `json:"crs,omitempty"`
	OrderedAxes []string   `json:"orderedAxes,omitempty"`
}

// Definition is a TileMatrixSet document: the OGC 17-083r4 JSON model served at
// /tileMatrixSets/{tileMatrixSetId}.
//
// Field order matches the bundled grid definitions so that a re-marshalled
// document reads the same as the original.
type Definition struct {
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`

	ID          string   `json:"id,omitempty"`
	URI         string   `json:"uri,omitempty"`
	OrderedAxes []string `json:"orderedAxes,omitempty"`

	CRS CRS `json:"crs"`

	WellKnownScaleSet string          `json:"wellKnownScaleSet,omitempty"`
	BoundingBox       *TMSBoundingBox `json:"boundingBox,omitempty"`

	TileMatrices []TileMatrix `json:"tileMatrices"`
}

// ParseDefinition decodes and validates an OGC TMS 2.0 TileMatrixSet document.
//
// It performs the checks morecantile does at model-validation time: documents
// using TMS 1.0 keywords are rejected with ErrTMSVersion1, and tileMatrices are
// sorted by their integer identifier so that index order matches zoom order.
func ParseDefinition(b []byte) (Definition, error) {
	if err := rejectVersion1(b); err != nil {
		return Definition{}, err
	}

	var def Definition
	if err := json.Unmarshal(b, &def); err != nil {
		return Definition{}, fmt.Errorf("tms: cannot decode TileMatrixSet: %w", err)
	}

	if len(def.TileMatrices) == 0 {
		return Definition{}, fmt.Errorf("tms: TileMatrixSet %q has no tileMatrices", def.ID)
	}

	if n := len(def.OrderedAxes); n != 0 && n != 2 {
		return Definition{}, fmt.Errorf(
			"tms: TileMatrixSet %q declares %d orderedAxes, want exactly 2", def.ID, n)
	}

	for _, m := range def.TileMatrices {
		if err := m.validate(); err != nil {
			return Definition{}, err
		}
	}

	def.sortTileMatrices()

	return def, nil
}

// rejectVersion1 reports ErrTMSVersion1 for documents carrying TMS 1.0
// keywords, mirroring morecantile's check_for_old_specification validator.
func rejectVersion1(b []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("tms: cannot decode TileMatrixSet: %w", err)
	}

	for _, key := range []string{"supportedCRS", "topLeftCorner"} {
		if _, ok := probe[key]; ok {
			return ErrTMSVersion1
		}
	}

	return nil
}

// sortTileMatrices orders matrices by their integer identifier.
//
// Ported from morecantile's sort_tile_matrices validator. Identifiers have
// already been validated as integers by TileMatrix.validate, so a matrix whose
// id somehow fails to parse sorts to the front rather than panicking.
func (d *Definition) sortTileMatrices() {
	sort.SliceStable(d.TileMatrices, func(i, j int) bool {
		zi, err := d.TileMatrices[i].Zoom()
		if err != nil {
			return true
		}

		zj, err := d.TileMatrices[j].Zoom()
		if err != nil {
			return false
		}

		return zi < zj
	})
}
