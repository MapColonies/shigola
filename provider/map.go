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
	// tileMatrixSetId. Omitted means every grid this build can serve. The first
	// entry is the map's default: the grid its /maps/... routes serve.
	TileMatrixSets []env.String `toml:"tile_matrix_sets"`
}
