// These tests are in package mvttest rather than mvttest_test because one of
// them exercises the -update-golden path, and the flag that gates it is
// deliberately package-private.
package mvttest

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

// tileHex is one vector tile, as bytes.
//
// Bytes rather than a tile this package encodes for itself, because a test that
// encodes and decodes through the same protobuf binding agrees with itself about
// a schema it could be reading wrongly. These bytes were produced from the
// specification's schema and are the thing a client would actually receive, so
// they keep saying the same thing when the binding underneath changes -- which
// is what they were written for (MAPCO-11516).
//
// What the tile is built to cover: two layers, encoded beta-first so the
// decoder's ordering is observable; every value kind the specification defines;
// a feature carrying no id, so HasID has something to distinguish; a non-default
// extent; and geometry using MoveTo with a count above one, LineTo with a count
// above one, ClosePath, and several parts in one feature.
const tileHex = "1a290a0462657461120f0809120200001801220511060802021a046e616d6522050a0374776f28800478021abb010a05616c706861120d08011202000018012203090a0e121a080212060101020203031802220c0914140a0a000909140a000a121a12060404050506061803220e0900001ac8010000c801c701000f1a046e616d651a056e5f696e741a066e5f75696e741a066e5f73696e741a076e5f666c6f61741a086e5f646f75626c651a04666c616722050a036f6e65220b20f9ffffffffffffffff01220228072202300d2205150000c03f2209190000000000000240220238012880207802"

func rawTile(t *testing.T) []byte {
	t.Helper()

	raw, err := hex.DecodeString(tileHex)
	if err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}

	return raw
}

// decoded is the fixture as this package reads it, for the tests that assert
// about a decoded tile rather than about decoding.
func decoded(t *testing.T) Tile {
	t.Helper()

	return DecodeRaw(t, rawTile(t))
}

func gzippedTile(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(rawTile(t)); err != nil {
		t.Fatalf("gzipping the fixture: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the gzip writer: %v", err)
	}

	return buf.Bytes()
}

// TestDecodeRawStructure states the decoded tile as a literal, in full. Stated
// rather than spot-checked because the point is to pin what a client reads:
// a field that silently stops arriving is exactly the regression a sampling
// assertion misses.
func TestDecodeRawStructure(t *testing.T) {
	want := Tile{Layers: []Layer{
		{
			Name:   "alpha",
			Extent: 4096,
			Keys:   []string{"name", "n_int", "n_uint", "n_sint", "n_float", "n_double", "flag"},
			Features: []Feature{
				{
					ID:       1,
					HasID:    true,
					GeomType: "POINT",
					Tags:     map[string]Value{"name": String("one")},
					Geom:     []Part{{Points: []Point{{5, 7}}}},
				},
				{
					ID:       2,
					HasID:    true,
					GeomType: "LINESTRING",
					Tags: map[string]Value{
						"n_int":  Int(-7),
						"n_uint": Uint(7),
						"n_sint": Sint(-7),
					},
					Geom: []Part{
						{Points: []Point{{10, 10}, {15, 10}}},
						{Points: []Point{{10, 20}, {10, 25}}},
					},
				},
				{
					// No id in the tile, so HasID is false and ID stays zero.
					GeomType: "POLYGON",
					Tags: map[string]Value{
						"n_float":  Float(1.5),
						"n_double": Double(2.25),
						"flag":     Bool(true),
					},
					Geom: []Part{{
						Points: []Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}},
						Closed: true,
					}},
				},
			},
		},
		{
			Name:   "beta",
			Extent: 512,
			Keys:   []string{"name"},
			Features: []Feature{{
				ID:       9,
				HasID:    true,
				GeomType: "POINT",
				Tags:     map[string]Value{"name": String("two")},
				Geom: []Part{
					{Points: []Point{{3, 4}}},
					{Points: []Point{{4, 5}}},
				},
			}},
		},
	}}

	got := decoded(t)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded tile does not match.\n--- got ---\n%s\n--- want ---\n%s", got.Render(), want.Render())
	}
}

// TestDecodeOrdersLayersByName pins the reordering specifically. The fixture
// encodes beta before alpha, so a decoder that passed the layers through in
// encoded order would still satisfy a per-layer assertion.
func TestDecodeOrdersLayersByName(t *testing.T) {
	got := decoded(t).LayerNames()

	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("layer names: got %v, want %v", got, want)
	}
}

// TestDecodeGunzips pins that the gzipped and uncompressed entry points agree.
// They are separate because tiles leave shigola gzipped but the provider tests
// hold raw bytes, and nothing else would notice the two drifting apart.
func TestDecodeGunzips(t *testing.T) {
	got := Decode(t, gzippedTile(t))

	if want := decoded(t); !reflect.DeepEqual(got, want) {
		t.Errorf("Decode and DecodeRaw disagree.\n--- Decode ---\n%s\n--- DecodeRaw ---\n%s", got.Render(), want.Render())
	}
}

// TestRender pins the rendering as an exact string. It is what golden files
// hold, so a change to it changes every golden in the tree at once.
func TestRender(t *testing.T) {
	want := `layer "alpha" extent=4096 keys=[flag n_double n_float n_int n_sint n_uint name]
  id=none POLYGON tags={flag=bool(true) n_double=double(2.25) n_float=float(1.5)} [(0,0) (100,0) (100,100) (0,100) closed]
  id=1 POINT tags={name=string("one")} [(5,7)]
  id=2 LINESTRING tags={n_int=int(-7) n_sint=sint(-7) n_uint=uint(7)} [(10,10) (15,10)] [(10,20) (10,25)]
layer "beta" extent=512 keys=[name]
  id=9 POINT tags={name=string("two")} [(3,4)] [(4,5)]
`

	if got := decoded(t).Render(); got != want {
		t.Errorf("Render.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderEmptyTile(t *testing.T) {
	if got, want := (Tile{}).Render(), "(no layers)\n"; got != want {
		t.Errorf("Render of an empty tile: got %q, want %q", got, want)
	}
}

func TestSummary(t *testing.T) {
	type tcase struct {
		tile Tile
		want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := tc.tile.Summary(); got != tc.want {
				t.Errorf("Summary: got %q, want %q", got, tc.want)
			}
		}
	}

	testcases := map[string]tcase{
		// Features in encoded order, not sorted: Summary is for a log line,
		// and reordering it would stop it describing the tile as it arrived.
		"the fixture": {
			tile: decoded(t),
			want: `alpha[one#1(5,7) unnamed#2(10,10) unnamed#0(0,0)] beta[two#9(3,4)]`,
		},
		// The case the doc comment calls out: a tile that held nothing has to
		// say so, or it reads the same as a fixture that stopped loading.
		"no layers": {
			tile: Tile{},
			want: "no layers",
		},
		"a layer with no features": {
			tile: Tile{Layers: []Layer{{Name: "empty"}}},
			want: "empty[]",
		},
		// Pinned as it reads today, run-on and all: Summary joins the id and
		// the position with no separator, which looks right for "one#1(5,7)"
		// and wrong for this. Recorded rather than corrected because Summary
		// feeds a log line and nothing parses it, so changing the format is
		// not this change's to make -- but it should not go unwritten either.
		"a feature with no geometry": {
			tile: Tile{Layers: []Layer{{Name: "l", Features: []Feature{{ID: 3}}}}},
			want: "l[unnamed#3no geometry]",
		},
	}

	for name, tc := range testcases {
		t.Run(name, fn(tc))
	}
}

func TestLayerLookup(t *testing.T) {
	type tcase struct {
		name       string
		wantOK     bool
		wantExtent uint32
	}

	tile := decoded(t)

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got, ok := tile.Layer(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("Layer(%v): ok is %t, want %t", tc.name, ok, tc.wantOK)
			}
			if ok && got.Extent != tc.wantExtent {
				t.Errorf("Layer(%v).Extent: got %d, want %d", tc.name, got.Extent, tc.wantExtent)
			}
		}
	}

	testcases := map[string]tcase{
		"a layer the tile carries":     {name: "beta", wantOK: true, wantExtent: 512},
		"the other one":                {name: "alpha", wantOK: true, wantExtent: 4096},
		"a layer the tile does not":    {name: "gamma", wantOK: false},
		"a name that is not a layer's": {name: "", wantOK: false},
	}

	for name, tc := range testcases {
		t.Run(name, fn(tc))
	}
}

func TestFeatureByTag(t *testing.T) {
	type tcase struct {
		layer  string
		key    string
		value  Value
		wantOK bool
		wantID uint64
	}

	tile := decoded(t)

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			layer, ok := tile.Layer(tc.layer)
			if !ok {
				t.Fatalf("Layer(%v): not found", tc.layer)
			}

			got, ok := layer.FeatureByTag(tc.key, tc.value)
			if ok != tc.wantOK {
				t.Fatalf("FeatureByTag(%v, %v): ok is %t, want %t", tc.key, tc.value, ok, tc.wantOK)
			}
			if ok && got.ID != tc.wantID {
				t.Errorf("FeatureByTag(%v, %v): id is %d, want %d", tc.key, tc.value, got.ID, tc.wantID)
			}
		}
	}

	testcases := map[string]tcase{
		"names the one feature carrying it": {
			layer: "alpha", key: "name", value: String("one"),
			wantOK: true, wantID: 1,
		},
		// The kind is part of the value, so asking for the same number as a
		// different type does not match. This is the distinction Kind exists
		// for, and losing it would make a column that changed type invisible.
		"a value of the wrong kind does not match": {
			layer: "alpha", key: "n_int", value: Sint(-7),
			wantOK: false,
		},
		"the right kind does": {
			layer: "alpha", key: "n_sint", value: Sint(-7),
			wantOK: true, wantID: 2,
		},
		"a key the layer does not carry": {
			layer: "beta", key: "absent", value: String("two"),
			wantOK: false,
		},
	}

	for name, tc := range testcases {
		t.Run(name, fn(tc))
	}
}

func TestFeatureIDsAndPoints(t *testing.T) {
	tile := decoded(t)

	alpha, ok := tile.Layer("alpha")
	if !ok {
		t.Fatal("Layer(alpha): not found")
	}

	// Ordered, and including the zero the id-less feature decodes to.
	if got, want := alpha.FeatureIDs(), []uint64{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("FeatureIDs: got %v, want %v", got, want)
	}

	// Points flattens the parts, so the multilinestring's two lines arrive as
	// one run in encoded order.
	line, ok := alpha.FeatureByTag("n_int", Int(-7))
	if !ok {
		t.Fatal("FeatureByTag(n_int): not found")
	}

	want := []Point{{10, 10}, {15, 10}, {10, 20}, {10, 25}}
	if got := line.Points(); !reflect.DeepEqual(got, want) {
		t.Errorf("Points: got %v, want %v", got, want)
	}
}

// setUpdateGolden sets the -update-golden flag for the duration of a test and
// restores it afterwards.
//
// The flag is process-wide, so a test that left it set would silently turn every
// later golden comparison into a rewrite -- the suite would pass, and it would
// pass by asserting nothing. Shared rather than written out at each call site
// because a save-and-restore pair is exactly the kind of thing that gets copied
// with the restore left behind.
func setUpdateGolden(t *testing.T, v bool) {
	t.Helper()

	was := *updateGolden
	t.Cleanup(func() { *updateGolden = was })

	*updateGolden = v
}

// tSpy stands in for *testing.T where failing is the expected outcome, so that
// reporting it on the real T would fail the test doing the asserting.
//
// A zero testing.T does not serve. Its Errorf happens to work, but Fatalf calls
// runtime.Goexit on the calling goroutine -- and on a T the framework never
// started that ends the enclosing test, reported as a pass. So the read path's
// missing-golden branch could not be asserted at all, and the mismatch branch
// worked only for as long as it stayed an Errorf.
//
// This records instead, and run below reproduces the one behaviour that matters:
// Fatalf does not return.
type tSpy struct {
	failed bool
	fatal  bool
	msgs   []string
}

func (s *tSpy) Helper() {}

func (s *tSpy) Logf(format string, args ...any) {
	s.msgs = append(s.msgs, fmt.Sprintf(format, args...))
}

func (s *tSpy) Errorf(format string, args ...any) {
	s.failed = true
	s.Logf(format, args...)
}

func (s *tSpy) Fatalf(format string, args ...any) {
	s.failed, s.fatal = true, true
	s.Logf(format, args...)
	runtime.Goexit()
}

// run calls fn with the spy on its own goroutine, so that the spy's Fatalf can
// end it the way testing.T's does rather than falling through into code the
// real framework would never have reached.
func (s *tSpy) run(fn func(TestingT)) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		fn(s)
	}()
	wg.Wait()
}

// TestAssertGolden covers both halves of the flag. The writing half is the one
// nothing else in the tree exercises -- every other caller reads -- and it is
// the half that matters, because a -update-golden that wrote the wrong bytes
// would then be blessed by the read half agreeing with it.
func TestAssertGolden(t *testing.T) {
	// Nested, so the directory-creating path is exercised rather than assumed.
	path := filepath.Join(t.TempDir(), "nested", "tile.txt")
	rendered := decoded(t).Render()

	setUpdateGolden(t, true)
	AssertGolden(t, path, rendered)

	// Read back off the filesystem rather than trusting the round trip: this is
	// what a reviewer would open, and what every other golden in the tree is.
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back the golden it wrote: %v", err)
	}
	if string(written) != rendered {
		t.Errorf("wrote something other than what it was given.\n--- wrote ---\n%s\n--- given ---\n%s", written, rendered)
	}

	setUpdateGolden(t, false)
	AssertGolden(t, path, rendered)
}

// TestAssertGoldenReportsAMismatch is the other half of the read path: that a
// golden which does not match fails. Without it the suite above would pass just
// as happily against an AssertGolden that compared nothing.
//
// It runs AssertGolden against a spy, because the failure is the expected
// outcome and reporting it on this test's own T would fail this test.
func TestAssertGoldenReportsAMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tile.txt")
	if err := os.WriteFile(path, []byte("layer \"alpha\" extent=4096 keys=[]\n"), 0o644); err != nil {
		t.Fatalf("writing the golden: %v", err)
	}

	setUpdateGolden(t, false)
	rendered := decoded(t).Render()

	var spy tSpy
	spy.run(func(t TestingT) { AssertGolden(t, path, rendered) })

	if !spy.failed {
		t.Error("AssertGolden accepted a golden that does not match what was served")
	}
}

// TestAssertGoldenReportsAMissingGolden is the branch that fails with Fatalf
// rather than Errorf: a golden that is not there at all.
//
// It is the branch a new golden hits before anyone has run -update-golden, and
// the one that has to keep failing -- an AssertGolden that treated a missing
// file as nothing to compare would make every golden in the tree optional.
func TestAssertGoldenReportsAMissingGolden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-written.txt")
	rendered := decoded(t).Render()

	setUpdateGolden(t, false)

	var spy tSpy
	spy.run(func(t TestingT) { AssertGolden(t, path, rendered) })

	if !spy.fatal {
		t.Error("AssertGolden accepted a golden file that does not exist")
	}
	// Fatalf must have ended the call there. Reaching the comparison below it
	// would mean comparing against the empty bytes of a file it failed to read.
	if n := len(spy.msgs); n != 1 {
		t.Errorf("AssertGolden carried on past Fatalf: %d messages, want 1: %q", n, spy.msgs)
	}
}
