package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Baseline is the committed record of what coverage was, and of the floor CI
// enforces. Keeping the floor in the same file as the measurement it came from
// is the point: a floor in the workflow and a baseline in the tree drift apart,
// and then nobody can say which number was ever true.
type Baseline struct {
	Mode     string
	Floor    float64
	Gates    string
	Total    float64
	Packages []PackageCoverage
}

// ParseBaseline reads the format renderBaseline writes. Comments and blank lines
// are ignored; every other line is "<key> <value...>" for the header keys, or a
// package record.
func ParseBaseline(r io.Reader) (*Baseline, error) {
	b := &Baseline{Floor: -1}

	s := bufio.NewScanner(r)
	for lineNo := 1; s.Scan(); lineNo++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)

		switch key {
		case "mode":
			b.Mode = rest
		case "gates":
			b.Gates = rest
		case "floor", "total":
			v, err := strconv.ParseFloat(rest, 64)
			if err != nil {
				return nil, fmt.Errorf("coverage baseline line %d: unreadable %s %q", lineNo, key, rest)
			}
			if key == "floor" {
				b.Floor = v
			} else {
				b.Total = v
			}
		default:
			// A package record: "<percent>  <covered>/<statements>  <package>".
			pkg, err := parsePackageRecord(line)
			if err != nil {
				return nil, fmt.Errorf("coverage baseline line %d: %w", lineNo, err)
			}
			b.Packages = append(b.Packages, pkg)
		}
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("reading the coverage baseline: %w", err)
	}
	if b.Floor < 0 {
		return nil, fmt.Errorf("coverage baseline: no floor recorded")
	}
	return b, nil
}

func parsePackageRecord(line string) (PackageCoverage, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return PackageCoverage{}, fmt.Errorf("malformed package record %q", line)
	}
	covered, statements, ok := strings.Cut(fields[1], "/")
	if !ok {
		return PackageCoverage{}, fmt.Errorf("malformed covered/statements in %q", line)
	}
	c, err := strconv.Atoi(covered)
	if err != nil {
		return PackageCoverage{}, fmt.Errorf("malformed covered count in %q", line)
	}
	t, err := strconv.Atoi(statements)
	if err != nil {
		return PackageCoverage{}, fmt.Errorf("malformed statement count in %q", line)
	}
	return PackageCoverage{Package: fields[2], Covered: c, Statements: t}, nil
}

func readBaseline(path string) (*Baseline, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening the coverage baseline: %w", err)
	}
	defer f.Close()
	return ParseBaseline(f)
}
