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

// Kind is the type an attribute arrived as.
//
// It is kept because losing it hides a real change: a column that starts
// arriving as a string where it used to be an integer changes what a client
// reads, and a description rendering both as 42 would say nothing happened.
type Kind string

const (
	KindString Kind = "string"
	KindInt    Kind = "int"
	KindUint   Kind = "uint"
	KindSint   Kind = "sint"
	KindFloat  Kind = "float"
	KindDouble Kind = "double"
	KindBool   Kind = "bool"
	KindEmpty  Kind = "empty"
)

// Value is one attribute value with the type it arrived as. It is comparable,
// so a test states what it expects as a literal and compares with ==.
type Value struct {
	Kind   Kind
	Str    string
	Int    int64
	Uint   uint64
	Number float64
	Bool   bool
}

// Constructors, so a test reads as the value it expects rather than as a struct
// literal with five zero fields.
func String(v string) Value  { return Value{Kind: KindString, Str: v} }
func Int(v int64) Value      { return Value{Kind: KindInt, Int: v} }
func Uint(v uint64) Value    { return Value{Kind: KindUint, Uint: v} }
func Sint(v int64) Value     { return Value{Kind: KindSint, Int: v} }
func Float(v float64) Value  { return Value{Kind: KindFloat, Number: v} }
func Double(v float64) Value { return Value{Kind: KindDouble, Number: v} }
func Bool(v bool) Value      { return Value{Kind: KindBool, Bool: v} }

func (v Value) String() string {
	switch v.Kind {
	case KindString:
		return fmt.Sprintf("string(%q)", v.Str)
	case KindInt:
		return fmt.Sprintf("int(%d)", v.Int)
	case KindSint:
		return fmt.Sprintf("sint(%d)", v.Int)
	case KindUint:
		return fmt.Sprintf("uint(%d)", v.Uint)
	case KindFloat:
		return fmt.Sprintf("float(%v)", v.Number)
	case KindDouble:
		return fmt.Sprintf("double(%v)", v.Number)
	case KindBool:
		return fmt.Sprintf("bool(%t)", v.Bool)
	default:
		return "empty()"
	}
}

// Point is one vertex, in tile-space integer coordinates.
type Point struct{ X, Y int32 }

func (p Point) String() string { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }

// Part is one run of vertices: a ring of a polygon, one line of a
// multilinestring, or the points of a multipoint.
//
// Grouping them is what lets a caller assert a shape without knowing the
// geometry type. A part begins at each MoveTo, which is how the specification
// separates them.
type Part struct {
	Points []Point
	// Closed records that a ClosePath ended this part. The closing vertex
	// itself stays implicit, as the encoding leaves it: adding it would render
	// every ring one point longer than it was encoded.
	Closed bool
}

func (p Part) String() string {
	pts := make([]string, 0, len(p.Points))
	for _, pt := range p.Points {
		pts = append(pts, pt.String())
	}
	out := strings.Join(pts, " ")
	if p.Closed {
		out += " closed"
	}
	return "[" + out + "]"
}

// Feature is one decoded feature.
type Feature struct {
	// ID is the feature id. HasID says whether the tile carried one at all,
	// which is the difference between an id of zero and no id: a layer whose
	// id_fieldname never reached ST_AsMVT produces the latter.
	ID    uint64
	HasID bool
	// GeomType is the type the tile declares for this feature, as the
	// specification names it: POINT, LINESTRING, POLYGON or UNKNOWN. A client
	// reads it to decide how to interpret the geometry, so a layer that starts
	// declaring the wrong one is a real change even when the coordinates are
	// unchanged.
	GeomType string
	Tags     map[string]Value
	Geom     []Part
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
				ID:       f.GetId(),
				HasID:    f.Id != nil,
				GeomType: f.GetType().String(),
				Tags:     decodeTags(t, l, f),
				Geom:     decodeGeometry(t, f.Geometry),
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

func decodeTags(t *testing.T, l *vectorTile.Tile_Layer, f *vectorTile.Tile_Feature) map[string]Value {
	t.Helper()

	tags := make(map[string]Value, len(f.Tags)/2)
	if len(f.Tags)%2 != 0 {
		t.Fatalf("mvttest: feature has an odd number of tag indexes (%d)", len(f.Tags))
	}

	for i := 0; i+1 < len(f.Tags); i += 2 {
		k, v := int(f.Tags[i]), int(f.Tags[i+1])
		if k >= len(l.Keys) || v >= len(l.Values) {
			t.Fatalf("mvttest: tag index out of range: key %d of %d, value %d of %d", k, len(l.Keys), v, len(l.Values))
		}
		tags[l.Keys[k]] = decodeValue(l.Values[v])
	}

	return tags
}

// decodeValue keeps the type the value arrived as. The specification gives a
// value exactly one populated field; which one is the type.
func decodeValue(v *vectorTile.Tile_Value) Value {
	switch {
	case v == nil:
		return Value{Kind: KindEmpty}
	case v.StringValue != nil:
		return String(v.GetStringValue())
	case v.BoolValue != nil:
		return Bool(v.GetBoolValue())
	case v.IntValue != nil:
		return Int(v.GetIntValue())
	case v.SintValue != nil:
		return Sint(v.GetSintValue())
	case v.UintValue != nil:
		return Uint(v.GetUintValue())
	case v.FloatValue != nil:
		return Float(float64(v.GetFloatValue()))
	case v.DoubleValue != nil:
		return Double(v.GetDoubleValue())
	default:
		return Value{Kind: KindEmpty}
	}
}

// decodeGeometry resolves the specification's delta-encoded command stream into
// parts of absolute tile-space positions.
//
// A part begins at each MoveTo, which is how the encoding separates a polygon's
// rings, a multilinestring's lines and a multipoint's points. Grouping them
// here is what lets a caller assert a shape without knowing its type.
func decodeGeometry(t *testing.T, g []uint32) []Part {
	t.Helper()

	var (
		parts  []Part
		cur    *Part
		cx, cy int32
	)

	// Only ever called with a part open: MoveTo opens one before appending, and
	// LineTo without a preceding MoveTo is not a geometry the encoding can
	// produce.
	appendPoint := func(x, y int32) {
		if cur == nil {
			t.Fatal("mvttest: geometry continues a part that was never started")
		}
		cur.Points = append(cur.Points, Point{X: x, Y: y})
	}

	for i := 0; i < len(g); {
		cmd := g[i] & 0x7
		count := int(g[i] >> 3)
		i++

		switch cmd {
		case 1: // MoveTo starts a part
			for n := 0; n < count; n++ {
				if i+1 >= len(g) {
					t.Fatal("mvttest: geometry ends mid-MoveTo")
				}
				cx += unzigzag(g[i])
				cy += unzigzag(g[i+1])
				i += 2
				parts = append(parts, Part{})
				cur = &parts[len(parts)-1]
				appendPoint(cx, cy)
			}
		case 2: // LineTo continues it
			for n := 0; n < count; n++ {
				if i+1 >= len(g) {
					t.Fatal("mvttest: geometry ends mid-LineTo")
				}
				cx += unzigzag(g[i])
				cy += unzigzag(g[i+1])
				i += 2
				appendPoint(cx, cy)
			}
		case 7: // ClosePath ends it, leaving the closing vertex implicit
			if cur == nil {
				t.Fatal("mvttest: ClosePath with no part open")
			}
			cur.Closed = true
		default:
			t.Fatalf("mvttest: unknown geometry command %d", cmd)
		}
	}

	return parts
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
func (l Layer) FeatureByTag(key string, value Value) (Feature, bool) {
	var (
		found Feature
		n     int
	)
	for _, f := range l.Features {
		if got, ok := f.Tags[key]; ok && got == value {
			found, n = f, n+1
		}
	}
	return found, n == 1
}

// FeatureIDs returns the ids the layer carries, ordered, so a caller can pin
// the set rather than sample it.
func (l Layer) FeatureIDs() []uint64 {
	ids := make([]uint64, 0, len(l.Features))
	for _, f := range l.Features {
		ids = append(ids, f.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Points returns the feature's vertices flattened, for the common case of a
// geometry whose parts do not matter to the caller.
func (f Feature) Points() []Point {
	var pts []Point
	for _, part := range f.Geom {
		pts = append(pts, part.Points...)
	}
	return pts
}

// Render returns the tile as deterministic text: layers by name, features by
// id, tags by key.
//
// Ordered rather than left as encoded, throughout. The specification does not
// order layers within a tile, and a rendering that follows the encoding pins
// something ST_AsMVT is free to change -- which reads as a regression when it
// is not one, and trains people to regenerate the golden without looking.
func (t Tile) Render() string {
	var b strings.Builder

	for _, l := range t.Layers {
		keys := append([]string(nil), l.Keys...)
		sort.Strings(keys)
		fmt.Fprintf(&b, "layer %q extent=%d keys=[%s]\n", l.Name, l.Extent, strings.Join(keys, " "))

		features := append([]Feature(nil), l.Features...)
		sort.Slice(features, func(i, j int) bool { return features[i].ID < features[j].ID })

		for _, f := range features {
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
			for _, part := range f.Geom {
				geom = append(geom, part.String())
			}

			fmt.Fprintf(&b, "  id=%s %s tags={%s} %s\n", id, f.GeomType, strings.Join(pairs, " "), strings.Join(geom, " "))
		}
	}

	if b.Len() == 0 {
		return "(no layers)\n"
	}

	return b.String()
}

// Summary renders a tile on one line, for a log: layer names with the features
// each holds, named by their name tag where they have one.
//
// The same walk as Render, condensed. A check that reports only its own verdict
// cannot tell "the tile held what I expected" from "the tile held nothing and I
// expected nothing", and the second is how a fixture that stopped loading looks.
func (t Tile) Summary() string {
	if len(t.Layers) == 0 {
		return "no layers"
	}

	parts := make([]string, 0, len(t.Layers))
	for _, l := range t.Layers {
		names := make([]string, 0, len(l.Features))
		for _, f := range l.Features {
			label := "unnamed"
			if v, ok := f.Tags["name"]; ok && v.Kind == KindString {
				label = v.Str
			}
			where := "no geometry"
			if pts := f.Points(); len(pts) > 0 {
				where = pts[0].String()
			}
			names = append(names, fmt.Sprintf("%s#%d%s", label, f.ID, where))
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", l.Name, strings.Join(names, " ")))
	}

	return strings.Join(parts, " ")
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
