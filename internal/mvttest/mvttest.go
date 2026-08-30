// Package mvttest decodes a Mapbox Vector Tile into the terms tests reason
// about, and renders it as text a reviewer can read.
//
// It exists because more than one test needs to look inside a tile and they
// should describe what they find the same way: provider tests assert what the
// database encoded, and server tests assert what a client received. Two
// consumers is a real seam rather than a hypothetical one.
//
// Nothing here is production code. It is deliberately strict -- a malformed
// tile fails the test rather than being tolerated -- because the only thing
// feeding it is a tile shigola just produced.
package mvttest

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	vectorTile "github.com/go-spatial/geom/encoding/mvt/vector_tile"
	"github.com/golang/protobuf/proto"
)

// updateGolden rewrites golden files instead of comparing against them.
//
// Regenerating is meant to be a deliberate act followed by reading the diff,
// which is why it is a flag rather than an environment variable and why the
// golden files it writes are kept small enough to read. A test that leans on
// the golden alone is one blessed regeneration away from asserting nothing --
// see AssertGolden.
var updateGolden = flag.Bool("update-golden", false, "rewrite golden files from what the code currently produces")

// Geometry command names, as the specification names them.
const (
	MoveTo    = "MoveTo"
	LineTo    = "LineTo"
	ClosePath = "ClosePath"
)

// Op is one geometry command with its cursor position resolved. The
// specification encodes coordinates as deltas; a test wants to say where a
// feature is, not how far it moved.
type Op struct {
	Cmd  string
	X, Y int32
}

func (o Op) String() string {
	if o.Cmd == ClosePath {
		return ClosePath
	}
	return fmt.Sprintf("%s(%d,%d)", o.Cmd, o.X, o.Y)
}

// Feature is one decoded feature.
type Feature struct {
	// ID is the feature id. HasID says whether the tile carried one at all,
	// which is the difference between an id of zero and no id: a layer whose
	// id_fieldname never reached ST_AsMVT produces the latter.
	ID    uint64
	HasID bool
	Tags  map[string]string
	Geom  []Op
}

// Layer is one decoded layer.
type Layer struct {
	Name     string
	Extent   uint32
	Keys     []string
	Features []Feature
}

// Tile is a decoded vector tile.
type Tile struct {
	Layers []Layer
}

// Decode gunzips and decodes a tile. Tiles leave shigola gzipped, so this is
// what a test holding a response body wants.
func Decode(t *testing.T, gzipped []byte) Tile {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(gzipped))
	if err != nil {
		t.Fatalf("mvttest: opening the gzip stream: %v", err)
	}
	defer zr.Close()

	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("mvttest: reading the gzip stream: %v", err)
	}

	return DecodeRaw(t, raw)
}

// DecodeRaw decodes an uncompressed tile.
func DecodeRaw(t *testing.T, raw []byte) Tile {
	t.Helper()

	var pb vectorTile.Tile
	if err := proto.Unmarshal(raw, &pb); err != nil {
		t.Fatalf("mvttest: decoding the tile: %v", err)
	}

	out := Tile{Layers: make([]Layer, 0, len(pb.Layers))}
	for _, l := range pb.Layers {
		if l.Name == nil {
			t.Fatal("mvttest: a layer arrived with no name")
		}

		layer := Layer{
			Name:     *l.Name,
			Extent:   l.GetExtent(),
			Keys:     append([]string(nil), l.Keys...),
			Features: make([]Feature, 0, len(l.Features)),
		}

		for _, f := range l.Features {
			layer.Features = append(layer.Features, Feature{
				ID:    f.GetId(),
				HasID: f.Id != nil,
				Tags:  decodeTags(t, l, f),
				Geom:  decodeGeometry(t, f.Geometry),
			})
		}

		out.Layers = append(out.Layers, layer)
	}

	// Ordered by name, not left as encoded: the specification does not order
	// layers within a tile, so anything downstream that read them positionally
	// would be pinning something ST_AsMVT is free to change.
	sort.Slice(out.Layers, func(i, j int) bool { return out.Layers[i].Name < out.Layers[j].Name })

	return out
}

func decodeTags(t *testing.T, l *vectorTile.Tile_Layer, f *vectorTile.Tile_Feature) map[string]string {
	t.Helper()

	tags := make(map[string]string, len(f.Tags)/2)
	if len(f.Tags)%2 != 0 {
		t.Fatalf("mvttest: feature has an odd number of tag indexes (%d)", len(f.Tags))
	}

	for i := 0; i+1 < len(f.Tags); i += 2 {
		k, v := int(f.Tags[i]), int(f.Tags[i+1])
		if k >= len(l.Keys) || v >= len(l.Values) {
			t.Fatalf("mvttest: tag index out of range: key %d of %d, value %d of %d", k, len(l.Keys), v, len(l.Values))
		}
		tags[l.Keys[k]] = renderValue(l.Values[v])
	}

	return tags
}

func renderValue(v *vectorTile.Tile_Value) string {
	switch {
	case v == nil:
		return "<nil>"
	case v.StringValue != nil:
		return v.GetStringValue()
	case v.BoolValue != nil:
		return strconv.FormatBool(v.GetBoolValue())
	case v.IntValue != nil:
		return strconv.FormatInt(v.GetIntValue(), 10)
	case v.SintValue != nil:
		return strconv.FormatInt(v.GetSintValue(), 10)
	case v.UintValue != nil:
		return strconv.FormatUint(v.GetUintValue(), 10)
	case v.FloatValue != nil:
		return strconv.FormatFloat(float64(v.GetFloatValue()), 'g', -1, 32)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(v.GetDoubleValue(), 'g', -1, 64)
	default:
		return "<empty>"
	}
}

// decodeGeometry resolves the specification's delta-encoded command stream into
// absolute tile-space positions.
func decodeGeometry(t *testing.T, g []uint32) []Op {
	t.Helper()

	var (
		ops    []Op
		cx, cy int32
	)

	for i := 0; i < len(g); {
		cmd := g[i] & 0x7
		count := int(g[i] >> 3)
		i++

		switch cmd {
		case 1, 2: // MoveTo, LineTo
			name := MoveTo
			if cmd == 2 {
				name = LineTo
			}
			for n := 0; n < count; n++ {
				if i+1 >= len(g) {
					t.Fatalf("mvttest: geometry ends mid-%s", name)
				}
				cx += unzigzag(g[i])
				cy += unzigzag(g[i+1])
				i += 2
				ops = append(ops, Op{Cmd: name, X: cx, Y: cy})
			}
		case 7: // ClosePath
			for n := 0; n < count; n++ {
				ops = append(ops, Op{Cmd: ClosePath})
			}
		default:
			t.Fatalf("mvttest: unknown geometry command %d", cmd)
		}
	}

	return ops
}

func unzigzag(v uint32) int32 { return int32(v>>1) ^ -int32(v&1) }

// Layer returns the named layer.
func (t Tile) Layer(name string) (Layer, bool) {
	for _, l := range t.Layers {
		if l.Name == name {
			return l, true
		}
	}
	return Layer{}, false
}

// LayerNames returns the layer names, ordered.
func (t Tile) LayerNames() []string {
	names := make([]string, 0, len(t.Layers))
	for _, l := range t.Layers {
		names = append(names, l.Name)
	}
	return names
}

// FeatureByTag returns the single feature carrying key=value, and whether
// exactly one was found. Fixtures name their features; this is how a test says
// which one it means without depending on encoding order.
func (l Layer) FeatureByTag(key, value string) (Feature, bool) {
	var (
		found Feature
		n     int
	)
	for _, f := range l.Features {
		if f.Tags[key] == value {
			found, n = f, n+1
		}
	}
	return found, n == 1
}

// Render returns the tile as deterministic text: layers by name, features in
// encoded order within a layer, tags by key.
func (t Tile) Render() string {
	var b strings.Builder

	for _, l := range t.Layers {
		keys := append([]string(nil), l.Keys...)
		sort.Strings(keys)
		fmt.Fprintf(&b, "layer %q extent=%d keys=[%s]\n", l.Name, l.Extent, strings.Join(keys, " "))

		for _, f := range l.Features {
			id := "none"
			if f.HasID {
				id = strconv.FormatUint(f.ID, 10)
			}

			tagKeys := make([]string, 0, len(f.Tags))
			for k := range f.Tags {
				tagKeys = append(tagKeys, k)
			}
			sort.Strings(tagKeys)

			pairs := make([]string, 0, len(tagKeys))
			for _, k := range tagKeys {
				pairs = append(pairs, fmt.Sprintf("%s=%s", k, f.Tags[k]))
			}

			geom := make([]string, 0, len(f.Geom))
			for _, op := range f.Geom {
				geom = append(geom, op.String())
			}

			fmt.Fprintf(&b, "  id=%s tags={%s} %s\n", id, strings.Join(pairs, " "), strings.Join(geom, " "))
		}
	}

	if b.Len() == 0 {
		return "(no layers)\n"
	}

	return b.String()
}

// AssertGolden compares got against the file at path, or rewrites it under
// -update-golden.
//
// A golden file catches changes nobody thought to assert, which is its whole
// value and also its whole danger: the way it fails is that someone regenerates
// it, skims a diff they cannot read, and commits. Two things keep that honest,
// and neither belongs here -- the fixture stays small enough that the diff is
// readable, and the test asserts separately, in literal terms, the handful of
// facts it would be unacceptable to lose. Those assertions do not move when
// this file is rewritten.
func AssertGolden(t *testing.T, path, got string) {
	t.Helper()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mvttest: creating %v: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("mvttest: writing %v: %v", path, err)
		}
		t.Logf("mvttest: rewrote %v -- read the diff before committing it", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("mvttest: reading %v: %v (re-run with -update-golden to create it)", path, err)
	}

	if got != string(want) {
		t.Errorf("%v does not match what was served.\n--- golden ---\n%s\n--- served ---\n%s", path, want, got)
		return
	}

	// Logged rather than silent, so a CI run says which goldens it actually
	// compared. A golden that stopped being checked -- renamed, or its test no
	// longer reached -- otherwise looks exactly like one that passed.
	t.Logf("golden ok: %v (%d lines)", path, strings.Count(got, "\n"))
}
