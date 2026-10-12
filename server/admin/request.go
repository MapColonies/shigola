package admin

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/internal/cacheops"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom/slippy"
)

type zoomRange struct {
	Min *uint `json:"min"`
	Max *uint `json:"max"`
}

type target struct {
	Map    string   `json:"map"`
	Layers []string `json:"layers"`
}

type request struct {
	// Bounds is west, south, east, north in lng/lat.
	Bounds *[4]float64 `json:"bounds"`
	// Tiles are z/x/y names in SourceTileMatrixSet, an alternative to Bounds.
	Tiles               []string  `json:"tiles"`
	SourceTileMatrixSet string    `json:"sourceTileMatrixSet"`
	ZoomRange           zoomRange `json:"zoomRange"`
	Targets             []target  `json:"targets"`
	// Overwrite is seed only. Tiers mean what the CLI's --cache-tiers means.
	Overwrite bool    `json:"overwrite"`
	Tiers     *string `json:"tiers"`
}

// job is a request resolved against the atlas.
type job struct {
	runs       []run
	overwrite  bool
	writeTiers []string
}

// run is the part of a request one tile matrix set covers.
type run struct {
	grid    *tms.TileMatrixSet
	targets []cacheops.Target
	boxes   []tms.BoundingBox
	zooms   []uint
}

func plan(a *atlas.Atlas, op cacheops.Op, req request) (job, error) {
	j := job{overwrite: req.Overwrite}

	if op == cacheops.Purge && req.Overwrite {
		return j, errors.New("overwrite applies to seed only")
	}

	spec := ""
	if req.Tiers != nil {
		spec = *req.Tiers
	}
	// validated for a purge too, as the CLI does, though a purge clears every tier
	tiers, err := cacheops.ResolveWriteTiers(a.GetCache(), spec)
	if err != nil {
		return j, fmt.Errorf("tiers %q: %w", spec, err)
	}
	if op == cacheops.Seed {
		j.writeTiers = tiers
	}

	zooms, err := req.ZoomRange.zooms()
	if err != nil {
		return j, err
	}

	boxes, err := req.area()
	if err != nil {
		return j, err
	}

	targets, err := resolveTargets(a, req.Targets)
	if err != nil {
		return j, err
	}
	if err := cacheops.ValidateTargets(op, targets); err != nil {
		return j, err
	}

	var runs []run
	for _, t := range targets {
		for _, grid := range t.Map.TileGrids() {
			i := slices.IndexFunc(runs, func(r run) bool { return r.grid.ID() == grid.ID() })
			if i < 0 {
				runs = append(runs, run{grid: grid, boxes: boxes, zooms: zooms})
				i = len(runs) - 1
			}
			runs[i].targets = append(runs[i].targets, t)
		}
	}
	j.runs = runs

	return j, nil
}

func (zr zoomRange) zooms() ([]uint, error) {
	if zr.Min == nil || zr.Max == nil {
		return nil, errors.New("zoomRange.min and zoomRange.max are required")
	}
	if *zr.Min > *zr.Max {
		return nil, fmt.Errorf("zoomRange.min (%d) is greater than zoomRange.max (%d)", *zr.Min, *zr.Max)
	}
	if *zr.Max > atlas.MaxZoom {
		return nil, fmt.Errorf("zoomRange.max (%d) is beyond the maximum zoom (%d)", *zr.Max, atlas.MaxZoom)
	}

	var zooms []uint
	for z := *zr.Min; z <= *zr.Max; z++ {
		zooms = append(zooms, z)
	}

	return zooms, nil
}

// area is the request's ground as lng/lat boxes, whichever way it was given.
func (req request) area() ([]tms.BoundingBox, error) {
	switch {
	case req.Bounds != nil && len(req.Tiles) > 0:
		return nil, errors.New("give either bounds or tiles, not both")

	case req.Bounds != nil:
		b := *req.Bounds
		if b[0] < -180 || b[0] > 180 || b[2] < -180 || b[2] > 180 ||
			b[1] < -90 || b[1] > 90 || b[3] < -90 || b[3] > 90 || b[1] > b[3] {
			return nil, fmt.Errorf("bounds %v are not a west, south, east, north lng/lat box", b)
		}
		return []tms.BoundingBox{{Left: b[0], Bottom: b[1], Right: b[2], Top: b[3]}}, nil

	case len(req.Tiles) > 0:
		return req.tileBoxes()
	}

	return nil, errors.New("one of bounds or tiles is required")
}

func (req request) tileBoxes() ([]tms.BoundingBox, error) {
	if req.SourceTileMatrixSet == "" {
		return nil, errors.New("sourceTileMatrixSet is required with tiles")
	}

	grid, err := tms.Get(req.SourceTileMatrixSet)
	if err != nil {
		return nil, fmt.Errorf("sourceTileMatrixSet: %w", err)
	}

	boxes := make([]tms.BoundingBox, 0, len(req.Tiles))
	for _, name := range req.Tiles {
		tile, err := parseTile(name)
		if err != nil {
			return nil, err
		}
		if err := grid.ValidateTile(tile.Z, tile.X, tile.Y); err != nil {
			return nil, fmt.Errorf("tile %q: %w", name, err)
		}

		b, err := grid.Bounds(tile)
		if err != nil {
			return nil, fmt.Errorf("tile %q: %w", name, err)
		}
		// a tile never crosses the antimeridian, but its edge on it can come
		// back as +180 for -180 or the reverse
		if b.Left > b.Right {
			if b.Left == 180 {
				b.Left = -180
			} else {
				b.Right = 180
			}
		}
		boxes = append(boxes, b)
	}

	return boxes, nil
}

func parseTile(name string) (tms.Tile, error) {
	parts := strings.Split(name, "/")
	if len(parts) != 3 {
		return tms.Tile{}, fmt.Errorf("tile %q is not z/x/y", name)
	}

	var n [3]int64
	for i, p := range parts {
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil || v < 0 {
			return tms.Tile{}, fmt.Errorf("tile %q is not z/x/y", name)
		}
		n[i] = v
	}

	if n[0] > atlas.MaxZoom {
		return tms.Tile{}, fmt.Errorf("tile %q is beyond the maximum zoom (%d)", name, atlas.MaxZoom)
	}

	return tms.Tile{Z: int(n[0]), X: n[1], Y: n[2]}, nil
}

func resolveTargets(a *atlas.Atlas, ts []target) ([]cacheops.Target, error) {
	if len(ts) == 0 {
		return nil, errors.New("targets is required: name the maps to work on")
	}

	out := make([]cacheops.Target, len(ts))
	for i, t := range ts {
		m, err := a.Map(t.Map)
		if err != nil {
			return nil, fmt.Errorf("targets[%d]: no map named %q", i, t.Map)
		}
		out[i] = cacheops.Target{Map: m, Layers: t.Layers}
	}

	return out, nil
}

// count is how many cache keys the job would touch, stopping once it passes
// limit. A single box is counted without listing its tiles; several boxes, as
// a tile list gives, are listed so tiles they share count once.
func count(op cacheops.Op, j job, limit int64) (int64, error) {
	var n int64

	for _, r := range j.runs {
		for _, z := range r.zooms {
			var keys int64
			for _, t := range r.targets {
				keys += int64(len(t.Keys(op, slippy.Zoom(z))))
			}

			tiles, err := uniqueTiles(r.grid, r.boxes, int(z), (limit-n)/max(keys, 1)+1)
			if err != nil {
				return 0, err
			}

			n += tiles * keys
			if n > limit {
				return n, nil
			}
		}
	}

	return n, nil
}

func uniqueTiles(grid *tms.TileMatrixSet, boxes []tms.BoundingBox, z int, stopAfter int64) (int64, error) {
	if len(boxes) == 1 {
		b := boxes[0]
		return grid.TileCount(b.Left, b.Bottom, b.Right, b.Top, []int{z}, true)
	}

	seen := map[tms.Tile]bool{}
	for _, b := range boxes {
		// counted first so a huge box is never listed: it alone passes the limit
		n, err := grid.TileCount(b.Left, b.Bottom, b.Right, b.Top, []int{z}, true)
		if err != nil {
			return 0, err
		}
		if n >= stopAfter {
			return n, nil
		}

		tiles, err := grid.Tiles(b.Left, b.Bottom, b.Right, b.Top, []int{z}, true)
		if err != nil {
			return 0, err
		}
		for _, t := range tiles {
			seen[t] = true
		}
		if int64(len(seen)) >= stopAfter {
			break
		}
	}

	return int64(len(seen)), nil
}
