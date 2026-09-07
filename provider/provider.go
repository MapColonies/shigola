package provider

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/slippy"
)

// defaultGridForSRID maps a bare tile SRID onto the TileMatrixSet tegola has
// historically meant by it.
//
// It exists only for callers that still describe a tile by its SRID — a
// description that is genuinely ambiguous, since WorldCRS84Quad and WGS1984Quad
// are both EPSG:4326 grids with different matrix shapes. Callers that know which
// grid they want say so with NewTileForGrid; this table is what NewTile falls
// back on.
var defaultGridForSRID = map[uint]string{
	shigola.WebMercator: tms.WebMercatorQuad,
	shigola.WGS84:       tms.WorldCRS84Quad,
}

// tile_t is an implementation of the Tile interface, it is
// named as such as to not confuse from the 4 other possible meanings
// of the symbol "tile" in this code base. It should be removed after
// the geom port is mostly done as part of issue #499 (removing the
// Tile interface in this package)
// TODO(@ear7h) remove this atrocity from the code base
// NOTE(@jchamberlain) leaving said atrocity for now because several places
// in the code require extent+srid, (which said atrocity supplies),
// not just extent.
type tile_t struct {
	slippy.Tile
	buffer uint
	// grid is the TileMatrixSet this tile is indexed in. It is nil only when a
	// caller named an SRID no grid is registered for, which Extent reports.
	grid *tms.TileMatrixSet
	// srid is the tile CRS's EPSG code, carried separately so that an
	// unresolvable grid can still report the SRID the caller asked for.
	srid uint64
}

// NewTile creates a new slippy tile with a Buffer, in the grid tegola
// historically associates with srid (see defaultGridForSRID). Prefer
// NewTileForGrid, which names the grid outright.
func NewTile(z slippy.Zoom, x uint, y uint, buf, srid uint) Tile {
	if srid == 0 {
		srid = shigola.WebMercator
	}

	gridID, ok := defaultGridForSRID[srid]
	if !ok {
		// Preserve the historical shape of this failure: the tile exists, and
		// reports the SRID it was asked for, but has no extent.
		return &tile_t{
			Tile:   slippy.Tile{Z: z, X: x, Y: y},
			buffer: buf,
			srid:   uint64(srid),
		}
	}

	grid, err := tms.Get(gridID)
	if err != nil {
		log.Errorf("tile grid %v is not available: %v", gridID, err)
		return &tile_t{
			Tile:   slippy.Tile{Z: z, X: x, Y: y},
			buffer: buf,
			srid:   uint64(srid),
		}
	}

	return NewTileForGrid(z, x, y, buf, grid)
}

// NewTileForGrid creates a new slippy tile with a Buffer, indexed in an explicit
// TileMatrixSet. This is the constructor the tile pipeline uses: the grid, not
// the SRID, is what determines the tile's extent and matrix dimensions.
func NewTileForGrid(z slippy.Zoom, x uint, y uint, buf uint, grid *tms.TileMatrixSet) Tile {
	t := &tile_t{
		Tile:   slippy.Tile{Z: z, X: x, Y: y},
		buffer: buf,
		grid:   grid,
	}

	if grid != nil {
		// A bundled grid always names a CRS we can resolve; a grid that does not
		// still yields tiles, it just cannot say which SRID they are in.
		if srid, err := grid.NativeSRID(); err == nil {
			t.srid = srid
		} else {
			log.Errorf("tile grid %v has no EPSG code: %v", grid.ID(), err)
		}
	}

	return t
}

// index converts the slippy tile index into the tms package's equivalent.
func (tile *tile_t) index() tms.Tile {
	return tms.Tile{Z: int(tile.Z), X: int64(tile.X), Y: int64(tile.Y)}
}

// Extent returns the extent of the tile
func (tile *tile_t) Extent() (ext *geom.Extent, srid uint64) {
	if tile.grid == nil {
		log.Error("Unsupported tile SRID.", tile.srid)
		return &geom.Extent{}, tile.srid
	}

	e, err := tile.grid.TileExtent(tile.index())
	if err != nil {
		log.Error("Could not generate valid extent for tile.", tile, err)
		return &geom.Extent{}, tile.srid
	}

	return &e, tile.srid
}

// BufferedExtent returns an extent of the tile, with the define buffer
func (tile *tile_t) BufferedExtent() (ext *geom.Extent, srid uint64) {
	ext, srid = tile.Extent()

	// The buffer is a pixel count, so it converts to projected units through
	// this tile's own span. Every matrix in a TileMatrixSet has uniform tiles,
	// so this is the same ratio slippy derived from the (z,0,0) tile — without
	// assuming the grid is WebMercator.
	ratio := ext.XSpan() / slippy.MvtTileDim

	return ext.ExpandBy(ratio * float64(tile.buffer)), srid
}

// Tile is an interface used by Tiler, it is an unnecessary abstraction and is
// due to be removed. The tiler interface will, instead take a *geom.Extent.
type Tile interface {
	// ZXY returns the z, x and y values of the tile
	ZXY() (slippy.Zoom, uint, uint)
	// Extent returns the extent of the tile excluding any buffer
	Extent() (extent *geom.Extent, srid uint64)
	// BufferedExtent returns the extent of the tile including any buffer
	BufferedExtent() (extent *geom.Extent, srid uint64)
}

// ParameterTokenRegexp to validate QueryParameters
var ParameterTokenRegexp = regexp.MustCompile("![a-zA-Z0-9_-]+!")

// CleanupFunc is called to when the system is shutting down, this allows the provider to clean up.
type CleanupFunc func()

type pfns struct {
	mvtInit MVTInitFunc
	cleanup CleanupFunc
}

var providers map[string]pfns

// MVTRegister the provider with the system. This call is generally made in the init functions of the provider.
//
//	the cleanup function will be called during shutdown of the provider to allow the provider to do any cleanup.
//
// The init function can not be nil, the cleanup function may be nil
func MVTRegister(name string, init MVTInitFunc, cleanup CleanupFunc) error {
	if init == nil {
		return ErrNilInitFunc
	}
	if providers == nil {
		providers = make(map[string]pfns)
	}

	if _, ok := providers[name]; ok {
		return fmt.Errorf("provider %v already exists", name)
	}

	providers[name] = pfns{
		mvtInit: init,
		cleanup: cleanup,
	}

	return nil
}

// removedProviders maps a provider type this build deliberately no longer
// serves onto the type that took over from it.
//
// A removed type is not a misspelled one. It is a name that worked, in a
// config someone is still running, and the useful thing to say about it is
// which type to write instead -- not a list of every type this binary happens
// to know, out of which the operator has to guess.
//
// Only types with a successor belong here. A backend that was dropped outright
// has nothing to redirect to and is better served by the unknown-provider
// error, which at least lists what is left.
var removedProviders map[string]string

// RegisterRemoved records that name is no longer served and that replacement
// took over from it. Like Register, this is called from a provider package's
// init function -- the package that used to serve the name is the one that
// knows what replaced it.
func RegisterRemoved(name, replacement string) error {
	if replacement == "" {
		return ErrMissingReplacement
	}
	if removedProviders == nil {
		removedProviders = make(map[string]string)
	}

	// Registered and removed are contradictory claims about one name, and the
	// registration is the one with a working init function behind it.
	if _, ok := providers[name]; ok {
		return ErrProviderAlreadyExists{Name: name}
	}

	removedProviders[name] = replacement

	return nil
}

// Removed reports whether name was a provider type this build has stopped
// serving, and if so what replaced it.
func Removed(name string) (replacement string, ok bool) {
	replacement, ok = removedProviders[name]
	return replacement, ok
}

// Drivers returns a list of registered drivers.
//
// It took a type filter until MAPCO-11491 retired the standard provider
// interface. With one kind of provider left there is nothing to filter on, and
// the callers that passed a type were all asking the same question this answers
// without one: what may a config name?
func Drivers() (l []string) {
	for k := range providers {
		l = append(l, k)
	}
	// Sorted because this list is read back to the operator in
	// ErrUnknownProviderType, and a message whose contents shuffle between runs
	// is one nobody can pin in a test or diff against a previous run.
	sort.Strings(l)

	return l
}

// For returns a configured provider of the given name.
func For(name string, config dict.Dicter, maps []Map) (MVTTiler, error) {
	p, ok := providers[name]
	if !ok {
		// A type with a named successor is reported as removed rather than as
		// unknown, so the operator is told what to write instead of being
		// handed the list to choose from.
		if replacement, removed := Removed(name); removed {
			return nil, ErrRemovedProvider{Name: name, Replacement: replacement}
		}
		return nil, ErrUnknownProvider{KnownProviders: Drivers(), Name: name}
	}
	if p.mvtInit == nil {
		return nil, ErrInvalidRegisteredProvider{Name: name}
	}

	return p.mvtInit(config, maps)
}

// Cleanup is called at the end of the run to allow providers to clean up
func Cleanup() {
	log.Info("cleaning up providers")
	for _, p := range providers {
		if p.cleanup != nil {
			p.cleanup()
		}
	}
}
