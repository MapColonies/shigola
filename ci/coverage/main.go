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
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sort"
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
	if gates == "" {
		// Read the provenance off the environment that was actually in effect
		// rather than trusting what someone typed. A `gates` line nobody checks
		// is worse than none: it is the only record of which suites the numbers
		// below were measured with, and the whole claim that the baseline is
		// reproducible rests on it.
		gates = enabledGates()
	}

	existing, err := readBaseline(*baselinePath)
	switch {
	case err == nil:
		// Keep whatever is already recorded unless this run was told otherwise.
		// Lowering the floor should be a deliberate edit, not a side effect of
		// regenerating the numbers on a machine with fewer gates enabled.
		if floor < 0 {
			floor = existing.Floor
		}
		if gates == "" {
			gates = existing.Gates
		}
	case !errors.Is(err, fs.ErrNotExist):
		// A baseline that exists but will not parse must not be treated as an
		// absent one: that path reseeds the floor from whatever was just
		// measured, silently lowering it at the moment the file is least
		// trustworthy. Refuse instead, and let the author look at it.
		return fmt.Errorf("refusing to overwrite a baseline that cannot be read: %w", err)
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

// minHeadroom is the least margin floorFor will leave between the measured
// coverage and the floor it records, in percentage points.
//
// The floor is meant to catch a coverage regression, not to pin the exact
// measurement. Incidental movement of a few statements is expected here: the
// cache write path runs a detached goroutine pool (cache/writepool.go), so
// whether some statements are counted plausibly depends on scheduling, and
// ordinary refactoring moves statement counts around without changing what is
// tested. A gate that fires on that gets edited rather than read, which is the
// failure mode this whole file exists to avoid.
//
// This is a lower bound, not the margin itself: floorFor drops a whole percent
// at a time, so the actual headroom lands somewhere in [minHeadroom, 1 +
// minHeadroom) depending on where the measurement falls. At the size of this
// tree a percentage point is roughly 120 statements, so the realised margin is
// something like 60-180 -- wide enough to absorb the above, and far narrower
// than any real regression, since removing a provider moves whole points.
const minHeadroom = 0.5

// floorFor turns a measured percentage into the floor to record: rounded down to
// a whole percent, and then down another whole percent if truncation alone left
// less than minHeadroom of margin.
//
// The second step is what makes this more than truncation. A measurement that
// lands just above a whole percent -- 44.01%, say -- truncates to 44.00, which
// is barely more than one statement of headroom once BelowFloor's rounding
// tolerance is taken off. Whether the margin is usable would then depend on
// where the measurement happened to fall, which is not a property a gate should
// have.
//
// The result is deliberately derived rather than chosen, so that regenerating
// the baseline reproduces the same reasoning instead of depending on what
// somebody typed at a terminal once.
func floorFor(pct float64) float64 {
	floor := math.Floor(pct)
	if pct-floor < minHeadroom {
		floor--
	}
	if floor < 0 {
		return 0
	}
	return floor
}

// gateVars are the opt-in environment switches that decide which integration
// suites run at all (internal/ttools.ShouldSkip). Which of them were set is the
// single most load-bearing fact about a coverage number here: with none of them
// on, whole packages measure near zero for reasons that have nothing to do with
// how well they are tested.
var gateVars = []string{
	"RUN_POSTGIS_TESTS",
	"RUN_REDIS_TESTS",
	"RUN_S3_TESTS",
	"RUN_AZBLOB_TESTS",
	"RUN_GCS_TESTS",
	"RUN_HANA_TESTS",
}

// enabledGates lists the gate variables set to the literal "yes" that
// internal/ttools requires, in a stable order so a regenerated baseline does not
// churn on map iteration.
func enabledGates() string {
	var on []string
	for _, v := range gateVars {
		if os.Getenv(v) == "yes" {
			on = append(on, v)
		}
	}
	sort.Strings(on)
	return strings.Join(on, " ")
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
