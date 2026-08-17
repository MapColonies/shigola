package ogc

import (
	"fmt"
	"strings"

	"github.com/MapColonies/shigola/atlas"
)

// LayerSeparator divides a Layer-collection's id into its map and layer parts.
//
// ':' rather than '/', '.' or '_': a slash would make the id look like two path
// segments, and a dot or underscore can occur in a tegola map or layer name,
// which would make the split ambiguous (ADR-0002).
const LayerSeparator = ":"

// Collection is one tileset's worth of geodata: a tegola map, or a single layer
// of one (ADR-0002).
//
// The two tiers exist because tegola serves both — a map's tiles carry every
// layer, and a layer's tiles carry one — and OGC clients that can only consume a
// single-layer tileset would otherwise be unable to use a multi-layer map.
type Collection struct {
	// ID is the collection id: "{map}" or "{map}:{layer}".
	ID string
	// Map is the atlas map this collection reads, already filtered to the
	// collection's layers.
	Map atlas.Map
	// LayerName is the layer a Layer-collection is restricted to, empty for a
	// Map-collection.
	LayerName string
}

// IsLayer reports whether this is a Layer-collection.
func (c Collection) IsLayer() bool { return c.LayerName != "" }

// Title is the collection's human-readable name.
func (c Collection) Title() string {
	if c.IsLayer() {
		return fmt.Sprintf("%s — %s", c.Map.Name, c.LayerName)
	}

	return c.Map.Name
}

// ErrCollectionNotFound reports an id that names no collection.
type ErrCollectionNotFound struct {
	ID string
}

func (e ErrCollectionNotFound) Error() string {
	return fmt.Sprintf("collection %q not found", e.ID)
}

// ErrTileSetNotFound reports a tiling scheme a collection is not served in.
//
// It is distinct from an unknown scheme: this one exists, the collection's map
// simply does not offer it. Both are a 404 to a client, which cannot act on the
// difference, but a log reader can.
type ErrTileSetNotFound struct {
	CollectionID    string
	TileMatrixSetID string
}

func (e ErrTileSetNotFound) Error() string {
	return fmt.Sprintf("collection %q is not served in tile matrix set %q", e.CollectionID, e.TileMatrixSetID)
}

// collections enumerates every collection this service publishes: one
// Map-collection per map, plus one Layer-collection per layer of it.
//
// The map-collection is always emitted, even for a single-layer map, so that a
// map's id is always a valid collection id.
func (s *Service) collections() []Collection {
	maps := s.cfg.Atlas.AllMaps()

	out := make([]Collection, 0, len(maps))
	for _, m := range maps {
		out = append(out, Collection{ID: m.Name, Map: m})

		for i := range m.Layers {
			name := m.Layers[i].MVTName()
			out = append(out, Collection{
				ID:        m.Name + LayerSeparator + name,
				Map:       m.FilterLayersByName(name),
				LayerName: name,
			})
		}
	}

	return out
}

// collection resolves a collection id to its map and layers.
//
// The id is split on the first separator only: a layer name may itself contain
// one, and the map name comes first.
func (s *Service) collection(id string) (Collection, error) {
	mapName, layerName, hasLayer := strings.Cut(id, LayerSeparator)

	m, err := s.cfg.Atlas.Map(mapName)
	if err != nil {
		return Collection{}, ErrCollectionNotFound{ID: id}
	}

	if !hasLayer {
		return Collection{ID: id, Map: m}, nil
	}

	filtered := m.FilterLayersByName(layerName)
	if len(filtered.Layers) == 0 {
		return Collection{}, ErrCollectionNotFound{ID: id}
	}

	return Collection{ID: id, Map: filtered, LayerName: layerName}, nil
}
