package main

import (
	"math"
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
