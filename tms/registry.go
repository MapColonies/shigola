package tms

// The grid registry, ported from morecantile/defaults.py (MIT, Development
// Seed), with lazy factory registration in place of morecantile's dict of
// pre-built or path-valued entries.

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// gridData holds the bundled OGC TileMatrixSet definitions, served verbatim at
// /tileMatrixSets/{tileMatrixSetId}.
//
//go:embed data/*.json
var gridData embed.FS

// activeGrids names the grids this build activates, per ADR-0009: the three
// whose tile-to-coordinate conversions are closed-form arithmetic over WGS 84,
// so the fork needs no PROJ backend and stays cgo-free.
//
// Every other bundled grid is still registered, and Get reports precisely why it
// is unavailable. Activating one is a matter of supplying its Transformer and
// adding it here — no call site changes.
// Identifiers of the grids this build activates. Every bundled grid is
// registered under its definition file's id; these three are the ones that need
// no transform backend and so can actually be served (ADR-0009).
const (
	WebMercatorQuad = "WebMercatorQuad"
	WorldCRS84Quad  = "WorldCRS84Quad"
	WGS1984Quad     = "WGS1984Quad"
)

var activeGrids = map[string]bool{
	WebMercatorQuad: true,
	WorldCRS84Quad:  true,
	WGS1984Quad:     true,
}

// Factory builds a TileMatrixSet on demand.
type Factory func() (*TileMatrixSet, error)

// Registry maps a tileMatrixSetId to a TileMatrixSet and is the single source of
// truth for which tiling schemes the server can produce and describe.
//
// Grids are held as factories and built on first use, then cached — including
// cached failures, so an unavailable grid reports the same reason every time.
// A Registry is safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	order     []string
	cache     map[string]cachedGrid
}

// cachedGrid memoises a factory's outcome.
type cachedGrid struct {
	tms *TileMatrixSet
	err error
}

// NewRegistry returns an empty Registry. Most callers want the package-level
// Default registry, which already holds the bundled grids.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		cache:     make(map[string]cachedGrid),
	}
}

// Register adds a grid factory under an id.
//
// It fails if the id is already registered; use Replace to override. The factory
// is not called until the grid is first requested, so registering a grid this
// build cannot serve is cheap and keeps it discoverable through Registered.
func (r *Registry) Register(id string, ctor Factory) error {
	if id == "" {
		return ErrInvalidIdentifier{Identifier: id}
	}

	if ctor == nil {
		return fmt.Errorf("tms: cannot register TileMatrixSet %q with a nil factory", id)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[id]; exists {
		return fmt.Errorf("tms: TileMatrixSet %q is already registered", id)
	}

	r.factories[id] = ctor
	r.order = append(r.order, id)

	return nil
}

// Replace registers a grid factory, overriding any existing registration and
// discarding anything already cached for that id.
//
// This is the Go spelling of morecantile's register(custom_tms, overwrite=True),
// and exists for the same reason: a deployment may need to serve a locally
// corrected definition of a bundled grid.
func (r *Registry) Replace(id string, ctor Factory) error {
	if id == "" {
		return ErrInvalidIdentifier{Identifier: id}
	}

	if ctor == nil {
		return fmt.Errorf("tms: cannot register TileMatrixSet %q with a nil factory", id)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[id]; !exists {
		r.order = append(r.order, id)
	}

	r.factories[id] = ctor
	delete(r.cache, id)

	return nil
}

// Get returns the grid registered under an id.
//
// An unregistered id yields ErrInvalidIdentifier. A registered grid this build
// cannot serve yields ErrGridUnavailable, which distinguishes "known but not
// available here" from "no such grid".
func (r *Registry) Get(id string) (*TileMatrixSet, error) {
	r.mu.RLock()
	cached, ok := r.cache[id]
	r.mu.RUnlock()

	if ok {
		return cached.tms, cached.err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Re-check: another goroutine may have resolved this id already.
	if cached, ok := r.cache[id]; ok {
		return cached.tms, cached.err
	}

	ctor, ok := r.factories[id]
	if !ok {
		return nil, ErrInvalidIdentifier{Identifier: id}
	}

	grid, err := ctor()
	r.cache[id] = cachedGrid{tms: grid, err: err}

	return grid, err
}

// List returns every grid this build can actually serve, ordered by id.
//
// This is what /tileMatrixSets should enumerate: listing a grid that cannot
// produce tiles would advertise a tiling scheme the server would then refuse.
func (r *Registry) List() []*TileMatrixSet {
	var out []*TileMatrixSet

	for _, id := range r.Registered() {
		grid, err := r.Get(id)
		if err != nil {
			continue
		}

		out = append(out, grid)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })

	return out
}

// Registered returns every registered id, including grids that are not
// available in this build, sorted for stable output.
func (r *Registry) Registered() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := append([]string(nil), r.order...)
	sort.Strings(out)

	return out
}

// Available reports whether a grid is registered and usable.
func (r *Registry) Available(id string) bool {
	_, err := r.Get(id)

	return err == nil
}

// AvailableIDs returns the ids of every grid this build can serve, with
// WebMercatorQuad first and the rest sorted.
//
// The order is part of the contract, not a detail: a caller that takes "every
// available grid" as a default — a map's tiling schemes, say — treats the first
// entry as the default one, and tegola's default grid has always been
// WebMercatorQuad. Sorting alone would put WGS1984Quad there, since 'G' sorts
// before 'e'.
func (r *Registry) AvailableIDs() []string {
	var out []string

	for _, id := range r.Registered() {
		if id == WebMercatorQuad || !r.Available(id) {
			continue
		}

		out = append(out, id)
	}

	if r.Available(WebMercatorQuad) {
		out = append([]string{WebMercatorQuad}, out...)
	}

	return out
}

// Default is the registry holding the bundled grid definitions. Server code
// should resolve tileMatrixSetIds through it rather than constructing grids
// directly.
var Default = NewRegistry()

// Get returns a grid from the Default registry.
func Get(id string) (*TileMatrixSet, error) { return Default.Get(id) }

// List returns the servable grids in the Default registry.
func List() []*TileMatrixSet { return Default.List() }

// Registered returns every id known to the Default registry, available or not.
func Registered() []string { return Default.Registered() }

// Register adds a grid factory to the Default registry.
func Register(id string, ctor Factory) error { return Default.Register(id, ctor) }

// Available reports whether a grid is registered and servable by this build.
func Available(id string) bool { return Default.Available(id) }

// AvailableIDs returns the ids of the Default registry's servable grids,
// WebMercatorQuad first.
func AvailableIDs() []string { return Default.AvailableIDs() }

func init() {
	if err := registerBundled(Default); err != nil {
		// The bundled definitions are compiled in, so a failure here means the
		// package itself is inconsistent rather than the input being bad.
		panic("tms: cannot register bundled TileMatrixSets: " + err.Error())
	}
}

// registerBundled registers a factory for every grid definition embedded under
// data/.
func registerBundled(r *Registry) error {
	entries, err := fs.Glob(gridData, "data/*.json")
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		return errors.New("tms: no embedded grid definitions found")
	}

	for _, entry := range entries {
		id := strings.TrimSuffix(path.Base(entry), ".json")

		if err := r.Register(id, bundledFactory(id, entry)); err != nil {
			return err
		}
	}

	return nil
}

// bundledFactory returns the Factory for an embedded grid definition. It parses
// the document, builds the grid, and then applies this build's activation
// policy, so a gated grid reports why it is gated rather than simply going
// missing.
func bundledFactory(id, entry string) Factory {
	return func() (*TileMatrixSet, error) {
		raw, err := gridData.ReadFile(entry)
		if err != nil {
			return nil, fmt.Errorf("tms: cannot read embedded definition %q: %w", entry, err)
		}

		def, err := ParseDefinition(raw)
		if err != nil {
			return nil, fmt.Errorf("tms: cannot parse embedded definition %q: %w", entry, err)
		}

		grid, err := New(def, raw)
		if err != nil {
			return nil, fmt.Errorf("tms: cannot build TileMatrixSet %q: %w", id, err)
		}

		if reason := gatingReason(grid); reason != nil {
			return nil, ErrGridUnavailable{ID: id, Reason: reason}
		}

		return grid, nil
	}
}

// gatingReason reports why a grid is not activated in this build, or nil when it
// is servable.
//
// The three reasons are genuinely different, and each must name itself honestly.
// Most gated grids are projected and have no arithmetic Transformer. The
// variable-width grids are geographic — a transform does exist — and are held
// back because coalesced columns do not fit tegola's tile pipeline. The last case
// is a grid this build could serve but has not been asked to.
func gatingReason(grid *TileMatrixSet) error {
	switch {
	case !grid.TransformAvailable():
		return ErrNoTransformBackend
	case grid.IsVariable():
		return ErrVariableWidthUnsupported
	case !activeGrids[grid.ID()]:
		return ErrGridNotActivated
	default:
		return nil
	}
}

// LoadDefinition returns a bundled grid definition and its original JSON,
// bypassing the registry's activation policy.
//
// This exists so that a gated grid's document and tile arithmetic remain
// testable and inspectable: everything except geographic conversion works
// without a transform backend.
func LoadDefinition(id string) (Definition, []byte, error) {
	raw, err := gridData.ReadFile("data/" + id + ".json")
	if err != nil {
		return Definition{}, nil, ErrInvalidIdentifier{Identifier: id}
	}

	def, err := ParseDefinition(raw)
	if err != nil {
		return Definition{}, nil, err
	}

	return def, raw, nil
}

// LoadGrid builds a bundled grid regardless of whether this build activates it,
// for the same reason as LoadDefinition.
func LoadGrid(id string) (*TileMatrixSet, error) {
	def, raw, err := LoadDefinition(id)
	if err != nil {
		return nil, err
	}

	return New(def, raw)
}

// BundledIDs returns the ids of every embedded grid definition.
func BundledIDs() []string {
	entries, err := fs.Glob(gridData, "data/*.json")
	if err != nil {
		return nil
	}

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, strings.TrimSuffix(path.Base(entry), ".json"))
	}

	sort.Strings(out)

	return out
}

// marshalDefinition encodes a Definition as OGC TMS 2.0 JSON. It is the fallback
// for grids built in memory, where no original document exists to serve.
func marshalDefinition(def Definition) ([]byte, error) {
	b, err := json.Marshal(def)
	if err != nil {
		return nil, fmt.Errorf("tms: cannot encode TileMatrixSet %q: %w", def.ID, err)
	}

	return b, nil
}
