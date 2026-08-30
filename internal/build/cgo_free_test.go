package build_test

import (
	"go/build/constraint"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cgoConstraint matches a build constraint that mentions cgo, so that both
// halves of a pair -- `cgo` and `!cgo` -- are found.
func cgoConstraint(line string) bool {
	expr, err := constraint.Parse(line)
	if err != nil {
		return false
	}

	// Eval with cgo true and again with it false: a constraint that mentions
	// cgo is one whose value depends on it, and this is cheaper than walking
	// the expression tree by hand.
	with := expr.Eval(func(tag string) bool { return tag == "cgo" })
	without := expr.Eval(func(string) bool { return false })

	return with != without
}

// TestNoCgoConstraints asserts that nothing in this tree is compiled
// conditionally on cgo.
//
// This is a property of the whole repository rather than of any one package, so
// it is checked by reading the tree rather than by building it: a constraint
// only excludes a file, and a file excluded in both CGO modes is invisible to
// every other kind of test.
//
// It matters because CGO_ENABLED silently changes what a shigola binary can
// do. While the GeoPackage provider existed, a cgo-less build simply did not
// know the gpkg provider type, so an operator's config was accepted or rejected
// according to how the binary happened to be compiled. Nothing in the tree
// depends on cgo now, and CI's two CGO modes therefore produce the same server.
func TestNoCgoConstraints(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			// vendor/ is other people's code, and this says nothing about it.
			if d.Name() == "vendor" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) != ".go" {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}

		for _, line := range strings.Split(string(body), "\n") {
			// Constraints precede the package clause; stopping there keeps a
			// comment quoting a constraint from failing the test.
			if strings.HasPrefix(line, "package ") {
				break
			}
			if cgoConstraint(line) {
				t.Errorf("%v carries a cgo build constraint: %v", rel, strings.TrimSpace(line))
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
}
