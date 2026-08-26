package main

import (
	"math"
	"os"
	"strings"
	"testing"
)

func TestParseProfile(t *testing.T) {
	type tcase struct {
		profile string
		mode    string
		// want maps a package import path to its expected percentage.
		want    map[string]float64
		wantAll float64
		err     string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			p, err := ParseProfile(strings.NewReader(tc.profile))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("expected error containing %q, got: %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProfile unexpected error: %v", err)
			}
			if p.Mode != tc.mode {
				t.Errorf("mode = %q, want %q", p.Mode, tc.mode)
			}
			if len(p.Packages) != len(tc.want) {
				t.Errorf("package count = %d, want %d", len(p.Packages), len(tc.want))
			}
			for _, pkg := range p.Packages {
				want, ok := tc.want[pkg.Package]
				if !ok {
					t.Errorf("unexpected package %q", pkg.Package)
					continue
				}
				if math.Abs(pkg.Percent()-want) > 0.005 {
					t.Errorf("%s = %.2f%%, want %.2f%%", pkg.Package, pkg.Percent(), want)
				}
			}
			if math.Abs(p.Percent()-tc.wantAll) > 0.005 {
				t.Errorf("total = %.2f%%, want %.2f%%", p.Percent(), tc.wantAll)
			}
		}
	}

	tests := map[string]tcase{
		"one package, partly covered": {
			profile: `mode: atomic
github.com/MapColonies/shigola/atlas/atlas.go:52.13,55.2 2 1
github.com/MapColonies/shigola/atlas/atlas.go:56.2,58.3 3 0
`,
			mode:    "atomic",
			want:    map[string]float64{"github.com/MapColonies/shigola/atlas": 40},
			wantAll: 40,
		},
		"packages are aggregated separately": {
			profile: `mode: atomic
github.com/MapColonies/shigola/atlas/atlas.go:52.13,55.2 2 1
github.com/MapColonies/shigola/atlas/atlas.go:56.2,58.3 3 0
github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 1 5
`,
			mode: "atomic",
			want: map[string]float64{
				"github.com/MapColonies/shigola/atlas": 40,
				"github.com/MapColonies/shigola/tms":   100,
			},
			wantAll: 50,
		},
		// The property that makes a merged profile safe to read: `go test ./...`
		// can emit the same block more than once when a package is exercised by
		// more than one test binary. Counting its statements twice would inflate
		// the denominator and quietly lower every percentage.
		"a repeated block counts its statements once": {
			profile: `mode: atomic
github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 4 0
github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 4 3
`,
			mode:    "atomic",
			want:    map[string]float64{"github.com/MapColonies/shigola/tms": 100},
			wantAll: 100,
		},
		"a repeated block stays uncovered if no copy ran": {
			profile: `mode: atomic
github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 4 0
github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 4 0
`,
			mode:    "atomic",
			want:    map[string]float64{"github.com/MapColonies/shigola/tms": 0},
			wantAll: 0,
		},
		// A package with no statements at all must not divide by zero.
		"no statements is 100%, not NaN": {
			profile: `mode: atomic
github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 0 0
`,
			mode:    "atomic",
			want:    map[string]float64{"github.com/MapColonies/shigola/tms": 100},
			wantAll: 100,
		},
		"an empty profile is not an error": {
			profile: "mode: atomic\n",
			mode:    "atomic",
			want:    map[string]float64{},
			wantAll: 100,
		},
		"a missing mode line is rejected": {
			profile: "github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 1 1\n",
			err:     "missing \"mode:\" line",
		},
		"a malformed block is rejected": {
			profile: `mode: atomic
this is not a coverage line
`,
			err: "malformed",
		},
		"a non-numeric statement count is rejected": {
			profile: `mode: atomic
github.com/MapColonies/shigola/tms/registry.go:10.1,12.2 x 1
`,
			err: "malformed",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestBelowFloor(t *testing.T) {
	type tcase struct {
		pct   float64
		floor float64
		want  bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := BelowFloor(tc.pct, tc.floor); got != tc.want {
				t.Errorf("BelowFloor(%v, %v) = %v, want %v", tc.pct, tc.floor, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"above the floor passes":      {pct: 61.2, floor: 60, want: false},
		"exactly on the floor passes": {pct: 60, floor: 60, want: false},
		"below the floor fails":       {pct: 59.99, floor: 60, want: true},
		// Reading a percentage back from a formatted baseline loses precision, so
		// a hair under the floor must not fail a build that is really level with it.
		"a rounding-width shortfall passes": {pct: 59.9999, floor: 60, want: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestParseBaseline(t *testing.T) {
	type tcase struct {
		body  string
		floor float64
		mode  string
		gates string
		pkgs  int
		err   string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			b, err := ParseBaseline(strings.NewReader(tc.body))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("expected error containing %q, got: %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBaseline unexpected error: %v", err)
			}
			if b.Floor != tc.floor {
				t.Errorf("floor = %v, want %v", b.Floor, tc.floor)
			}
			if b.Mode != tc.mode {
				t.Errorf("mode = %q, want %q", b.Mode, tc.mode)
			}
			if b.Gates != tc.gates {
				t.Errorf("gates = %q, want %q", b.Gates, tc.gates)
			}
			if len(b.Packages) != tc.pkgs {
				t.Errorf("package count = %d, want %d", len(b.Packages), tc.pkgs)
			}
		}
	}

	tests := map[string]tcase{
		"a full baseline round-trips": {
			body: `# a comment
mode atomic
floor 57.00
gates RUN_POSTGIS_TESTS RUN_REDIS_TESTS
total 57.31
#
#  percent  covered/statements  package
  45.21  123/272  github.com/MapColonies/shigola/atlas
  92.00  46/50  github.com/MapColonies/shigola/tms
`,
			floor: 57, mode: "atomic",
			gates: "RUN_POSTGIS_TESTS RUN_REDIS_TESTS", pkgs: 2,
		},
		"gates may be absent": {
			body:  "mode atomic\nfloor 12.50\ntotal 12.50\n",
			floor: 12.5, mode: "atomic", gates: "", pkgs: 0,
		},
		// The floor is the whole point of the file; a baseline without one would
		// silently gate on zero.
		"a missing floor is rejected": {
			body: "mode atomic\ntotal 57.31\n",
			err:  "no floor",
		},
		"an unreadable floor is rejected": {
			body: "mode atomic\nfloor not-a-number\n",
			err:  "floor",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// The baseline is written by one function and read back by another, and CI
// depends on the two agreeing. Nothing else in this package tests them against
// each other: TestParseBaseline reads a hand-written body, so a change to
// renderBaseline's formatting would leave it green while the gate stopped being
// able to read its own file.
func TestRenderBaselineRoundTrips(t *testing.T) {
	profile := &Profile{
		Mode: "atomic",
		Packages: []PackageCoverage{
			{Package: "github.com/MapColonies/shigola/atlas", Covered: 123, Statements: 272},
			{Package: "github.com/MapColonies/shigola/tms", Covered: 46, Statements: 50},
			// A package with no statements renders as 100% over 0/0, which is the
			// shape most likely to trip a naive parser.
			{Package: "github.com/MapColonies/shigola/dict", Covered: 0, Statements: 0},
		},
	}

	rendered := renderBaseline(profile, 57, "RUN_POSTGIS_TESTS RUN_REDIS_TESTS")

	got, err := ParseBaseline(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("ParseBaseline could not read renderBaseline's own output: %v\n%s", err, rendered)
	}

	if got.Mode != profile.Mode {
		t.Errorf("mode = %q, want %q", got.Mode, profile.Mode)
	}
	if got.Floor != 57 {
		t.Errorf("floor = %v, want 57", got.Floor)
	}
	if got.Gates != "RUN_POSTGIS_TESTS RUN_REDIS_TESTS" {
		t.Errorf("gates = %q, want the two gates", got.Gates)
	}
	if math.Abs(got.Total-profile.Percent()) > 0.005 {
		t.Errorf("total = %.2f, want %.2f", got.Total, profile.Percent())
	}
	if len(got.Packages) != len(profile.Packages) {
		t.Fatalf("package count = %d, want %d", len(got.Packages), len(profile.Packages))
	}
	for i, want := range profile.Packages {
		if got.Packages[i] != want {
			t.Errorf("package %d = %+v, want %+v", i, got.Packages[i], want)
		}
	}
}

func TestFloorFor(t *testing.T) {
	type tcase struct {
		pct  float64
		want float64
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := floorFor(tc.pct); got != tc.want {
				t.Errorf("floorFor(%v) = %v, want %v", tc.pct, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		// Rounding down, not to nearest: a floor above the measurement would not
		// start green, which is the one thing the ticket asks of it.
		"rounds down to a whole percent": {pct: 57.94, want: 57},
		// Truncation alone is not enough when the measurement sits just above a
		// whole percent: 44.01% would yield a floor of 44.00, barely more than one
		// statement of headroom once BelowFloor's tolerance is taken off. See
		// minHeadroom for why that is too thin to gate on.
		"a measurement just above a whole percent drops one": {pct: 44.01, want: 43},
		"an exact percent drops one":                         {pct: 60, want: 59},
		"just under a whole percent":                         {pct: 59.999, want: 59},
		"zero cannot go negative":                            {pct: 0, want: 0},
		"a sub-one-percent measurement cannot go negative":   {pct: 0.4, want: 0},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// Regenerating the baseline on a machine with fewer integration gates enabled
// measures less coverage. The floor must not follow it down: a floor that
// quietly drops every time somebody runs -write on a laptop is not a floor. The
// only way down is to pass -floor deliberately.
func TestWriteBaselineKeepsTheRecordedFloor(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/coverage-baseline.txt"

	oldPath, oldFloor, oldGates := *baselinePath, *floorFlag, *gatesFlag
	defer func() {
		*baselinePath, *floorFlag, *gatesFlag = oldPath, oldFloor, oldGates
	}()
	*baselinePath = path

	// A first run with no baseline present seeds the floor from what it measured.
	rich := &Profile{Mode: "atomic", Packages: []PackageCoverage{
		{Package: "github.com/MapColonies/shigola/atlas", Covered: 60, Statements: 100},
	}}
	*floorFlag, *gatesFlag = -1, "RUN_POSTGIS_TESTS RUN_REDIS_TESTS"
	if err := writeBaseline(rich); err != nil {
		t.Fatalf("seeding the baseline: %v", err)
	}

	seeded, err := readBaseline(path)
	if err != nil {
		t.Fatalf("reading the seeded baseline: %v", err)
	}
	// 60% measured seeds a floor of 59: floorFor leaves headroom below the
	// measurement. See TestFloorFor.
	if seeded.Floor != 59 {
		t.Fatalf("seeded floor = %v, want 59", seeded.Floor)
	}

	// A second run measuring less, with no -floor and no -gates, must keep both.
	lean := &Profile{Mode: "atomic", Packages: []PackageCoverage{
		{Package: "github.com/MapColonies/shigola/atlas", Covered: 20, Statements: 100},
	}}
	*floorFlag, *gatesFlag = -1, ""
	if err := writeBaseline(lean); err != nil {
		t.Fatalf("regenerating the baseline: %v", err)
	}

	after, err := readBaseline(path)
	if err != nil {
		t.Fatalf("reading the regenerated baseline: %v", err)
	}
	if after.Floor != 59 {
		t.Errorf("floor = %v after regenerating on a leaner run, want it held at 59", after.Floor)
	}
	if after.Gates != "RUN_POSTGIS_TESTS RUN_REDIS_TESTS" {
		t.Errorf("gates = %q, want the recorded gates kept", after.Gates)
	}
	if math.Abs(after.Total-20) > 0.005 {
		t.Errorf("total = %.2f, want the newly measured 20.00", after.Total)
	}
}

// A baseline that exists but cannot be parsed must not be mistaken for one that
// is absent. Treating the two the same reseeds the floor from whatever was just
// measured, which is exactly the silent lowering the recorded floor exists to
// prevent -- and it would happen at the moment the file is least trustworthy.
func TestWriteBaselineRefusesAnUnreadableBaseline(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/coverage-baseline.txt"
	if err := os.WriteFile(path, []byte("mode atomic\nthis is not a baseline\n"), 0o644); err != nil {
		t.Fatalf("seeding a malformed baseline: %v", err)
	}

	old := *baselinePath
	*baselinePath = path
	defer func() { *baselinePath = old }()

	oldFloor := *floorFlag
	*floorFlag = -1
	defer func() { *floorFlag = oldFloor }()

	err := writeBaseline(&Profile{Mode: "atomic", Packages: []PackageCoverage{
		{Package: "github.com/MapColonies/shigola/atlas", Covered: 5, Statements: 100},
	}})
	if err == nil {
		t.Fatal("writeBaseline overwrote an unreadable baseline; want an error")
	}

	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading the baseline back: %v", readErr)
	}
	if !strings.Contains(string(body), "this is not a baseline") {
		t.Errorf("the unreadable baseline was overwritten:\n%s", body)
	}
}
