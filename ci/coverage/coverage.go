package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// PackageCoverage is one package's share of the profile: how many statements it
// has, and how many of them ran at least once.
type PackageCoverage struct {
	Package    string
	Statements int
	Covered    int
}

// Percent is the fraction of statements that ran, as Go's own cover tooling
// computes it. A package with no statements counts as fully covered rather than
// as a division by zero: there is nothing in it left to test.
func (p PackageCoverage) Percent() float64 {
	if p.Statements == 0 {
		return 100
	}
	return float64(p.Covered) / float64(p.Statements) * 100
}

// Profile is a parsed Go coverage profile, aggregated by package.
type Profile struct {
	Mode     string
	Packages []PackageCoverage
}

// Percent is coverage across every package, weighted by statement count. It is
// deliberately not the mean of the per-package percentages: that would give a
// twenty-statement package the same say as a two-thousand-statement one.
func (p Profile) Percent() float64 {
	var statements, covered int
	for _, pkg := range p.Packages {
		statements += pkg.Statements
		covered += pkg.Covered
	}
	return PackageCoverage{Statements: statements, Covered: covered}.Percent()
}

// blockKey identifies one counted region of one file. Blocks are keyed rather
// than accumulated because `go test ./...` can emit the same block more than
// once — a package exercised by several test binaries appears several times in
// the merged profile. Adding those copies up would count the same statements
// repeatedly and quietly deflate every percentage.
type blockKey struct {
	file string
	span string
}

// ParseProfile reads the format `go test -coverprofile` writes: a "mode:" line,
// then one line per block of the form
//
//	<import path>/<file>:<startLine>.<startCol>,<endLine>.<endCol> <statements> <count>
func ParseProfile(r io.Reader) (*Profile, error) {
	var (
		mode   string
		blocks = map[blockKey]*struct{ statements, count int }{}
		order  []blockKey
	)

	s := bufio.NewScanner(r)
	// Coverage lines are short, but a generated file can carry a long path; the
	// default 64KiB token limit is ample and left alone deliberately.
	for lineNo := 1; s.Scan(); lineNo++ {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode:") {
			mode = strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			continue
		}
		if mode == "" {
			return nil, fmt.Errorf(`coverage profile line %d: missing "mode:" line`, lineNo)
		}

		// "<file>:<span> <statements> <count>"
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("coverage profile line %d: malformed block %q", lineNo, line)
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			return nil, fmt.Errorf("coverage profile line %d: malformed block %q", lineNo, line)
		}
		statements, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("coverage profile line %d: malformed statement count in %q", lineNo, line)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("coverage profile line %d: malformed execution count in %q", lineNo, line)
		}

		key := blockKey{file: fields[0][:colon], span: fields[0][colon+1:]}
		b, seen := blocks[key]
		if !seen {
			b = &struct{ statements, count int }{}
			blocks[key] = b
			order = append(order, key)
		}
		b.statements = statements
		b.count += count
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("reading coverage profile: %w", err)
	}
	if mode == "" {
		return nil, fmt.Errorf(`coverage profile: missing "mode:" line`)
	}

	byPackage := map[string]*PackageCoverage{}
	for _, key := range order {
		b := blocks[key]
		pkg := path.Dir(key.file)
		p, ok := byPackage[pkg]
		if !ok {
			p = &PackageCoverage{Package: pkg}
			byPackage[pkg] = p
		}
		p.Statements += b.statements
		if b.count > 0 {
			p.Covered += b.statements
		}
	}

	profile := &Profile{Mode: mode, Packages: make([]PackageCoverage, 0, len(byPackage))}
	for _, p := range byPackage {
		profile.Packages = append(profile.Packages, *p)
	}
	sort.Slice(profile.Packages, func(i, j int) bool {
		return profile.Packages[i].Package < profile.Packages[j].Package
	})
	return profile, nil
}

// floorTolerance absorbs the precision lost when a percentage is written to the
// baseline file at two decimal places and read back. Without it a build exactly
// level with the floor could fail on a difference that only exists in the
// formatting.
const floorTolerance = 0.005

// BelowFloor reports whether pct misses floor by more than the width of the
// rounding used to record it.
func BelowFloor(pct, floor float64) bool {
	return pct < floor-floorTolerance
}
