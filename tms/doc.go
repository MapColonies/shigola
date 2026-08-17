// Package tms implements OGC Two Dimensional Tile Matrix Set (OGC 17-083r4)
// tiling schemes, and is the single source of truth for which grids tegola can
// produce and describe.
//
// The package is a faithful Go port of developmentseed/morecantile 7.0.3
// (https://github.com/developmentseed/morecantile), MIT licensed — see
// LICENSE-morecantile in this directory. The OGC TMS 2.0 document model, the
// tile algorithms, the bundled grid definitions under data/, and the test suite
// are all translated from it so that morecantile's proven golden values act as
// this package's correctness oracle.
//
// # Registry
//
// Grids are obtained through the package Registry, which maps a
// tileMatrixSetId to a TileMatrixSet. Grids enter the registry as factories
// (see Register), so adding a grid later never changes a call site.
//
// # PROJ-free grids
//
// morecantile delegates coordinate reference system handling to pyproj. This
// port has no PROJ dependency and stays cgo-free, so CRS knowledge is limited
// to the metadata table in crs.go and to the arithmetic Transformer
// implementations in transform.go.
//
// Every grid's tile-index arithmetic — MatrixSize, TileExtent, Tile, UpperLeft,
// XYBounds and friends — is closed-form over the grid's own matrix definitions
// and therefore works for all 13 bundled grids without any CRS transform. Only
// conversions to and from geographic coordinates (GeoExtent, LngLat, XY,
// Bounds, TileFromLngLat) need a Transformer.
//
// Grids whose CRS this package cannot transform arithmetically are registered
// but return ErrNoTransformBackend from their factory, so they are visible as
// "known but unavailable" rather than silently missing. Activating them is a
// matter of supplying a Transformer backend; see ADR-0009.
package tms
