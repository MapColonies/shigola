package build_test

import (
	"go/build/constraint"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// cgoEnabled matches a CGO_ENABLED setting in any of the forms the build
// configuration uses: `CGO_ENABLED=1` in a shell script or a Dockerfile ENV,
// `CGO_ENABLED: "1"` in compose and workflow YAML, and `"CGO_ENABLED": "1"` in
// devcontainer.json. The value is captured so a setting can be judged rather
// than merely spotted.
var cgoEnabled = regexp.MustCompile(`CGO_ENABLED"?\s*[=:]\s*"?([A-Za-z0-9_${}]+)`)

// buildConfig reports whether a file configures how shigola is built or tested,
// which is the only place a CGO_ENABLED setting can take effect.
//
// Matched by name and extension rather than by directory, so a workflow or a
// compose file that moves stays covered.
func buildConfig(name string) bool {
	switch filepath.Ext(name) {
	case ".yml", ".yaml", ".sh":
		return true
	}

	switch {
	case name == "Makefile", name == "devcontainer.json":
		return true
	case strings.HasPrefix(name, "Dockerfile"), strings.HasSuffix(name, ".Dockerfile"):
		return true
	}

	return false
}

// TestNoCgoEnabledInBuildConfig asserts that no build configuration in this tree
// turns cgo on.
//
// TestNoCgoConstraints above says nothing here is *compiled* conditionally on
// cgo. This says the build configuration has stopped acting as though something
// were: with no cgo-dependent package left, a CGO_ENABLED=1 in a compose file or
// a release workflow buys a C toolchain, a dynamically linked binary and a
// second test mode for a dependency that does not exist (MAPCO-11489).
//
// Setting it to 0 is fine and is the point -- what this rejects is enabling it,
// or leaving the decision to whether the runner happens to have a C compiler,
// which is what made the release matrix produce differently linked binaries per
// architecture.
//
// The one place cgo is still genuinely required is `go test -race`, whose
// detector links a C runtime. That is a property of the test command, not of
// this tree, and it is why CI does not set CGO_ENABLED=0 globally.
func TestNoCgoEnabledInBuildConfig(t *testing.T) {
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

		if !buildConfig(d.Name()) {
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

		for n, line := range strings.Split(string(body), "\n") {
			// A wholly commented-out line is prose about the setting, not the
			// setting. An inline trailing comment is not stripped, so an
			// `ENV CGO_ENABLED=1 # why` is still caught.
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}

			m := cgoEnabled.FindStringSubmatch(line)
			if m == nil {
				continue
			}

			if m[1] != "0" {
				t.Errorf("%v:%d enables cgo: %v", rel, n+1, strings.TrimSpace(line))
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
}
