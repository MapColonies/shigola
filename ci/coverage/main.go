// Command coverage records and enforces this repository's test-coverage floor.
//
// Coverage was measured and uploaded long before it gated anything, which meant
// a regression was only visible if somebody went looking. This turns it into a
// build failure.
//
//	# regenerate the committed baseline from a fresh profile
//	go run ./ci/coverage -write
//
//	# fail if the profile is below the floor the baseline records
//	go run ./ci/coverage
//
// The floor lives in the baseline file rather than in the workflow, so the
// number and the measurement it came from cannot drift apart.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

var (
	profilePath  = flag.String("profile", "profile.cov", "the coverage profile to read")
	baselinePath = flag.String("baseline", "ci/coverage-baseline.txt", "the committed baseline file")
	write        = flag.Bool("write", false, "rewrite the baseline from the profile instead of checking against it")
	floorFlag    = flag.Float64("floor", -1, "the floor to record, as a percentage; with -write, defaults to keeping the recorded floor")
	gatesFlag    = flag.String("gates", "", "with -write, the RUN_*_TESTS gates that were enabled, recorded in the baseline header")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	f, err := os.Open(*profilePath)
	if err != nil {
		return fmt.Errorf("opening the coverage profile: %w", err)
	}
	defer f.Close()

	profile, err := ParseProfile(f)
	if err != nil {
		return err
	}

	if *write {
		return writeBaseline(profile)
	}
	return checkAgainstBaseline(profile)
}

func writeBaseline(profile *Profile) error {
	floor := *floorFlag
	gates := *gatesFlag
	if existing, err := readBaseline(*baselinePath); err == nil {
		// Keep whatever is already recorded unless this run was told otherwise.
		// Lowering the floor should be a deliberate edit, not a side effect of
		// regenerating the numbers on a machine with fewer gates enabled.
		if floor < 0 {
			floor = existing.Floor
		}
		if gates == "" {
			gates = existing.Gates
		}
	}
	if floor < 0 {
		// No floor anywhere: seed it from what was actually measured, which is
		// what "set from the measured baseline rather than aspirationally" means.
		floor = floorFor(profile.Percent())
	}

	if err := os.WriteFile(*baselinePath, []byte(renderBaseline(profile, floor, gates)), 0o644); err != nil {
		return fmt.Errorf("writing the baseline: %w", err)
	}
	fmt.Printf("wrote %s: %.2f%% across %d packages, floor %.2f%%\n",
		*baselinePath, profile.Percent(), len(profile.Packages), floor)
	return nil
}

func checkAgainstBaseline(profile *Profile) error {
	baseline, err := readBaseline(*baselinePath)
	if err != nil {
		return err
	}
	if baseline.Mode != "" && profile.Mode != baseline.Mode {
		// Not fatal: the profile is still readable. But -covermode is how the
		// race detector and coverage counting coexist here, so a silent change
		// to it is worth saying out loud.
		fmt.Fprintf(os.Stderr,
			"coverage: warning: profile mode is %q but the baseline was measured with %q\n",
			profile.Mode, baseline.Mode)
	}

	pct := profile.Percent()
	fmt.Printf("coverage: %.2f%% (floor %.2f%%)\n", pct, baseline.Floor)

	if BelowFloor(pct, baseline.Floor) {
		return fmt.Errorf(
			"coverage %.2f%% is below the floor of %.2f%%.\n"+
				"        Add tests for what you changed, or -- if the drop is justified --\n"+
				"        rerun `go run ./ci/coverage -write -floor=<n>` and say why in the PR.",
			pct, baseline.Floor)
	}
	return nil
}

// floorFor rounds a measured percentage down to a whole percent. A floor pinned
// to the exact measurement would fail on any change that moves a single
// statement, which trains people to edit the floor rather than read it.
func floorFor(pct float64) float64 {
	floor := float64(int(pct))
	if floor < 0 {
		return 0
	}
	return floor
}

func renderBaseline(profile *Profile, floor float64, gates string) string {
	var b strings.Builder

	b.WriteString(`# Test-coverage baseline for MapColonies/shigola.
#
# Regenerate with:
#
#     go run ./ci/coverage -write
#
# and check a profile against it with:
#
#     go run ./ci/coverage
#
# The floor below is what CI enforces. It is deliberately set from the measured
# total rather than from an aspiration, so it starts green: raising it is a
# decision to make on purpose, after the coverage is really there.
#
# This file exists to be a before-picture. Percentages here were taken while the
# tree still had every provider in it, so they are the thing later work -- the
# provider removals in particular -- gets held against. Regenerating it discards
# that comparison, so regenerate it when the numbers are meant to move, not to
# make a red build green.
#
`)
	fmt.Fprintf(&b, "mode %s\n", profile.Mode)
	fmt.Fprintf(&b, "floor %.2f\n", floor)
	if gates != "" {
		fmt.Fprintf(&b, "gates %s\n", gates)
	}
	fmt.Fprintf(&b, "total %.2f\n", profile.Percent())
	b.WriteString("#\n# percent  covered/statements  package\n")

	for _, pkg := range profile.Packages {
		fmt.Fprintf(&b, "%7.2f  %d/%d  %s\n",
			pkg.Percent(), pkg.Covered, pkg.Statements, pkg.Package)
	}
	return b.String()
}
