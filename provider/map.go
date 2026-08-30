package provider

import "github.com/MapColonies/shigola/internal/env"

// A Map represents a map in the Tegola Config file.
type Map struct {
	Name        env.String       `toml:"name"`
	Attribution env.String       `toml:"attribution"`
	Bounds      []env.Float      `toml:"bounds"`
	Center      [3]env.Float     `toml:"center"`
	Layers      []MapLayer       `toml:"layers"`
	Parameters  []QueryParameter `toml:"params"`
	TileBuffer  *env.Int         `toml:"tile_buffer"`
	// TileMatrixSets names the tiling schemes this map may be requested in, by
	// tileMatrixSetId. Omitted means every grid this build can serve.
	//
	// Order is not a default. It was one while the /maps/... routes served a
	// grid without naming it; those are gone, and every request names its
	// scheme. What order still decides is the order tilesets are listed in, and
	// which scheme `cache seed --map` picks when no --tile-matrix-set is given.
	TileMatrixSets []env.String `toml:"tile_matrix_sets"`
}
