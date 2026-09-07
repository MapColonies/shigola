package test

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/provider"
	"github.com/go-spatial/geom"

	"github.com/MapColonies/shigola/dict"
)

const (
	Name = "test"
	// MVTProviderType is the name this provider registers under, spelled out
	// the way provider/postgis spells its own.
	MVTProviderType = "mvt_test"
)

var (
	lock     sync.Mutex
	MVTCount int
)

func init() {
	provider.MVTRegister(MVTProviderType, NewMVTTileProvider, Cleanup)
}

// NewMVTTileProvider sets up a test provider for mvt tile providers. The only
// supported parameter is "test_file", which should point to an mvt tile file to
// return for MVTForLayers.
//
// It is optional. Omitting it yields a provider that serves no bytes, which is
// what a test wanting a registrable provider rather than a particular tile
// needs -- and since MAPCO-11491 that is every test that used to reach for the
// debug or standard test provider.
func NewMVTTileProvider(config dict.Dicter, maps []provider.Map) (provider.MVTTiler, error) {
	lock.Lock()
	MVTCount++
	lock.Unlock()

	var mvtTile []byte
	if config != nil {
		none := ""
		path, err := config.String("test_file", &none)
		if err != nil {
			return nil, fmt.Errorf("failed to get test_file key: %w", err)
		}
		if path != "" {
			mvtTile, err = os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("failed to read test_file: %w", err)
			}
		}
	}

	return &TileProvider{
		MVTTile: mvtTile,
	}, nil
}

// Cleanup cleans up all the test providers.
func Cleanup() {
	lock.Lock()
	MVTCount = 0
	lock.Unlock()
}

// TileProvider mocks out an MVT tile provider.
//
// MVTTile is the tile it serves for every request. Left nil it serves no bytes,
// which is what an empty tile looks like on the wire -- enough for the tests
// that care about status, headers and framing rather than about content. Tests
// that need a tile's content to depend on the ground it covers use the PostGIS
// fixture instead (see server/tilecontent).
type TileProvider struct {
	MVTTile []byte
}

// Layers returns the configured layers, there is always only one "test-layer"
func (tp *TileProvider) Layers() ([]provider.LayerInfo, error) {
	return []provider.LayerInfo{
		layer{
			name:     "test-layer",
			geomType: geom.Polygon{},
			srid:     shigola.WebMercator,
		},
	}, nil
}

// MVTForLayers mocks out MVTForLayers by just returning the MVTTile bytes, this will never error
func (tp *TileProvider) MVTForLayers(ctx context.Context, _ provider.Tile, _ provider.Params, _ []provider.Layer) ([]byte, error) {
	if tp == nil {
		return nil, nil
	}
	return tp.MVTTile, nil
}
