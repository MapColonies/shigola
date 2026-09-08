package atlas

import (
	"bytes"
	"compress/gzip"
	"context"
	"strings"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/observability"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
)

// NewWebMercatorMap creates a new map with the necessary default values
func NewWebMercatorMap(name string) Map {
	return Map{
		Name: name,
		// default bounds
		Bounds:         shigola.WGS84Bounds,
		Layers:         []Layer{},
		SRID:           shigola.WebMercator,
		TileMatrixSets: []*tms.TileMatrixSet{DefaultTileGrid()},
		TileExtent:     4096,
		TileBuffer:     uint64(shigola.DefaultTileBuffer),
	}
}

// DefaultTileGrid is the TileMatrixSet a Map carries when nothing else names
// one: WebMercatorQuad, the grid tegola has always served.
//
// WebMercatorQuad is bundled and active in every build, so a failure to resolve
// it means the tms package's embedded definitions are broken — the same
// condition its own init panics on.
//
// It panics, so it must stay off every request path. Nothing reached from a
// handler may call it: a served request names its grid, and every seam it
// travels through — Encode, SeedMapTile, PurgeMapTile — takes that grid as an
// argument rather than resolving a default of its own (MAPCO-11486). Its one
// caller is NewWebMercatorMap, and keeping it to construction is what makes the
// panic a startup concern rather than a request-time one.
func DefaultTileGrid() *tms.TileMatrixSet {
	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		panic("atlas: default tile grid " + tms.WebMercatorQuad + " is unavailable: " + err.Error())
	}

	return grid
}

// Map defines a Web Mercator map
type Map struct {
	Name string
	// Contains an attribution to be displayed when the map is shown to a user.
	// 	This string is sanitized so it can't be abused as a vector for XSS or beacon tracking.
	Attribution string
	// The maximum extent of available map tiles in WGS:84
	// latitude and longitude values, in the order left, bottom, right, top.
	// Default: [-180, -85, 180, 85]
	Bounds *geom.Extent
	// The first value is the longitude, the second is latitude (both in
	// WGS:84 values), the third value is the zoom level.
	Center [3]float64
	Layers []Layer
	// Params holds configured query parameters
	Params []provider.QueryParameter

	SRID uint64
	// TileMatrixSets are the grids this map may be requested in.
	//
	// ADR-0008 gave the first entry a second job — naming the map's default —
	// because the native /maps/... routes served a grid without being told
	// which. Those routes are gone (MAPCO-11484), and every OGC request names
	// its scheme, so serving reads no default from this list: it reads
	// membership, through SupportsTileGrid, and nothing else. The order that
	// remains is presentation — the order a collection's tilesets are listed
	// in — plus one fallback that `cache seed` states in its own terms rather
	// than inheriting from here.
	//
	// Read it through TileGrids rather than directly.
	TileMatrixSets []*tms.TileMatrixSet
	// ServeLayerCollections decides whether this map's layers are independently
	// addressable: nil or true publishes a collection per layer alongside the
	// map's own, false publishes the map's own only (MAPCO-11493).
	//
	// A pointer rather than a plain bool, unlike TileBuffer above, because the
	// two zero values differ in kind. A zero TileBuffer is a degenerate value of
	// a knob; a zero bool here would read as "hide every layer collection" on
	// every Map built as a literal rather than through NewWebMercatorMap — two
	// dozen of them in this tree — and silently invert the feature's default.
	// nil is the default, and it means the same thing as an omitted config key.
	//
	// Read it through ServesLayerCollections rather than directly.
	ServeLayerCollections *bool
	// MVT output values
	TileExtent uint64
	TileBuffer uint64

	mvtProviderName string
	mvtProvider     provider.MVTTiler

	observer observability.Interface
}

// TileGrids returns every TileMatrixSet this map may be requested in.
//
// A map that names none offers none, rather than falling back to
// DefaultTileGrid: this is read from handlers, and the fallback would put a
// panicking resolver on the request path to cover a case registration cannot
// produce — register fills the list with every available grid when the config
// omits the key, so a live map always has at least one.
func (m Map) TileGrids() []*tms.TileMatrixSet {
	grids := make([]*tms.TileMatrixSet, 0, len(m.TileMatrixSets))
	for _, grid := range m.TileMatrixSets {
		if grid != nil {
			grids = append(grids, grid)
		}
	}

	return grids
}

// ServesLayerCollections reports whether this map's layers are independently
// addressable.
//
// The default is yes, so a Map that says nothing about the flag serves what it
// always did.
func (m Map) ServesLayerCollections() bool {
	return m.ServeLayerCollections == nil || *m.ServeLayerCollections
}

// SupportsTileGrid reports whether this map may be requested in the named grid.
func (m Map) SupportsTileGrid(id string) bool {
	for _, grid := range m.TileGrids() {
		if grid.ID() == id {
			return true
		}
	}

	return false
}

// HasMVTProvider indicates if map is a mvt provider based map
func (m Map) HasMVTProvider() bool { return m.mvtProvider != nil }

// MVTProvider returns the mvt provider if this map is a mvt provider based map, otherwise nil
func (m Map) MVTProvider() provider.MVTTiler { return m.mvtProvider }

// MVTProviderName returns the mvt provider name if this map is a mvt provider based map, otherwise ""
func (m Map) MVTProviderName() string { return m.mvtProviderName }

// SetMVTProvider sets the map to be based on the passed in mvt provider, and returning the provider
func (m *Map) SetMVTProvider(name string, p provider.MVTTiler) provider.MVTTiler {
	m.mvtProviderName = name
	m.mvtProvider = p
	return p
}

// Collectors returns the map's provider-level metrics collectors.
//
// It asked each layer for its own until MAPCO-11491: layers carried a provider
// then, and a map without an MVT provider was assembled from several. A map has
// exactly one provider now, so there is one place to ask.
func (m Map) Collectors(prefix string, config func(configKey string) map[string]interface{}) ([]observability.Collector, error) {
	collect, ok := m.mvtProvider.(observability.Observer)
	if !ok {
		return nil, nil
	}

	return collect.Collectors(prefix, config)
}

// FilterLayersByZoom returns a copy of a Map with a subset of layers that match the given zoom
func (m Map) FilterLayersByZoom(zoom slippy.Zoom) Map {
	var layers []Layer

	for i := range m.Layers {
		if slippy.Zoom(m.Layers[i].MinZoom) <= zoom && slippy.Zoom(m.Layers[i].MaxZoom) >= zoom {
			layers = append(layers, m.Layers[i])
			continue
		}
	}

	// overwrite the Map's layers with our subset
	m.Layers = layers

	return m
}

// FilterLayersByName returns a copy of a Map with a subset of layers that match the supplied list of layer names
func (m Map) FilterLayersByName(names ...string) Map {
	var layers []Layer

	nameStr := strings.Join(names, ",")
	for i := range m.Layers {
		// if we have a name set, use it for the lookup
		if m.Layers[i].Name != "" && nameStr == m.Layers[i].Name {
			layers = append(layers, m.Layers[i])
			continue
		} else if m.Layers[i].ProviderLayerName != "" && strings.Contains(nameStr, m.Layers[i].ProviderLayerName) { // default to using the ProviderLayerName for the lookup
			layers = append(layers, m.Layers[i])
			continue
		}
	}

	// overwrite the Map's layers with our subset
	m.Layers = layers

	return m
}

func (m Map) encodeMVTProviderTile(ctx context.Context, grid *tms.TileMatrixSet, tile slippy.Tile, params provider.Params) ([]byte, error) {
	// get the list of our layers
	ptile := provider.NewTileForGrid(tile.Z, tile.X, tile.Y, uint(m.TileBuffer), grid)

	layers := make([]provider.Layer, len(m.Layers))
	for i := range m.Layers {
		layers[i] = provider.Layer{
			Name:    m.Layers[i].ProviderLayerName,
			MVTName: m.Layers[i].MVTName(),
		}
	}
	return m.mvtProvider.MVTForLayers(ctx, ptile, params, layers)

}

// Encode will encode the given tile, cut in grid, into mvt format.
//
// grid is required. It decides the tile's ground extent and the CRS features
// are reprojected into, and it is the same grid the caller must file the result
// under in the cache — so a caller that has not resolved one has not decided
// what it is asking for. Defaulting it here would let the encode and the cache
// key disagree silently, which is the failure this parameter exists to make
// impossible (MAPCO-11486).
func (m Map) Encode(ctx context.Context, grid *tms.TileMatrixSet, tile slippy.Tile, params provider.Params) ([]byte, error) {
	if grid == nil {
		return nil, ErrNilGrid
	}

	if !m.HasMVTProvider() {
		return nil, ErrNoMVTProvider{Map: m.Name}
	}

	tileBytes, err := m.encodeMVTProviderTile(ctx, grid, tile, params)
	if err != nil {
		return nil, err
	}

	// buffer to store our compressed bytes
	var gzipBuf bytes.Buffer

	// compress the encoded bytes
	w := gzip.NewWriter(&gzipBuf)
	_, err = w.Write(tileBytes)
	if err != nil {
		return nil, err
	}

	// flush and close the writer
	if err = w.Close(); err != nil {
		return nil, err
	}

	// return encoded, gzipped tile
	return gzipBuf.Bytes(), nil
}
