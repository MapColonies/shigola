package build_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/internal/build"
)

// The release builds stamp Version, GitRevision and GitBranch into this package
// with `go build -ldflags "-X <pkg>.Version=..."`. That stamp is silent when it
// misses: the linker discards an -X whose symbol does not resolve, and the
// binary is built, published and run reporting "version not set" with nothing
// anywhere having failed. Go import paths are case-sensitive, so
// `github.com/mapcolonies/...` and `github.com/MapColonies/...` are two
// different packages and only one of them is this one.
//
// That is what happened: the Dockerfile's BUILDPKG default was lower-cased
// against the module path, so every image built from the ARG default reported no
// version at all (MAPCO-11500). Nothing caught it because there was nothing
// that could -- no build fails, no test fails, and the only evidence is the
// output of `shigola version` on a published image.
//
// So this is checked by reading the tree. A stamp path is a string in a
// Dockerfile or a workflow, never a Go import the compiler could verify, and a
// test that built the tree would not see it.

// stampPkgPath matches anything shaped like a Go package path ending in the
// version-stamp package, whatever its owner or case, so that a wrongly-cased
// path and a path still naming the upstream project are both found and then
// compared against the one correct value.
var stampPkgPath = regexp.MustCompile(`(?i)[\w.-]+\.[\w.-]+/[\w.-]+/[\w.-]+/internal/build`)

// modulePath returns the module path declared in go.mod, which is the authority
// the stamp paths are checked against -- rather than a constant here, which
// would only move the place the two can disagree.
func modulePath(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}

	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}

	t.Fatal("go.mod declares no module path")

	return ""
}

// stampPathFile reports whether a file can carry a version-stamp package path:
// the build configuration that passes one to the linker, plus Go source, which
// is where the CI helper and the tag generator keep theirs as flag defaults.
func stampPathFile(name string) bool {
	return buildConfig(name) || filepath.Ext(name) == ".go"
}

// TestStampPathMatchesModulePath asserts that every version-stamp package path
// in this tree is the real one, exactly, including case.
//
// Deliberately broader than the case bug that prompted it: the check is that
// each path *equals* the module path plus /internal/build, so a path left
// pointing at the upstream project fails here too.
func TestStampPathMatchesModulePath(t *testing.T) {
	want := modulePath(t) + "/internal/build"

	walkTree(t, stampPathFile, func(rel, body string) {
		// This file quotes wrong paths on purpose, in prose and in its own
		// table. Reading it would fail the test with its own examples.
		if filepath.Base(rel) == "stamp_path_test.go" {
			return
		}

		for n, line := range strings.Split(body, "\n") {
			for _, got := range stampPkgPath.FindAllString(line, -1) {
				if got != want {
					t.Errorf("%v:%d stamps into %q, want %q", rel, n+1, got, want)
				}
			}
		}
	})
}

// TestStampedSymbolsExist asserts that every symbol the ldflags stamp into is a
// variable this package actually has.
//
// The same silence covers a renamed variable as covers a wrongly-cased path:
// `-X pkg.Verison=1.2.3` links cleanly and stamps nothing. The compile-time
// references below are the other half -- they are what makes this test fail to
// build, rather than fail to notice, if one of the three is removed.
func TestStampedSymbolsExist(t *testing.T) {
	// The left-hand side of an -X, up to the `=`: `${BUILD_PKG}.Version`,
	// `$Env:BUILD_PKG.GitBranch`, or a path written out in full. The symbol is
	// whatever follows the last dot, so this does not have to understand how
	// any one shell spells a variable.
	stamped := regexp.MustCompile(`-X\s+'?([^\s'"=]+)=`)

	// Referenced so this set cannot drift from the package by deletion: remove
	// one of the three and this test stops compiling.
	known := map[string]*string{
		"Version":     &build.Version,
		"GitRevision": &build.GitRevision,
		"GitBranch":   &build.GitBranch,
	}

	walkTree(t, buildConfig, func(rel, body string) {
		for n, line := range strings.Split(body, "\n") {
			for _, m := range stamped.FindAllStringSubmatch(line, -1) {
				symbol := m[1]
				if i := strings.LastIndex(symbol, "."); i >= 0 {
					symbol = symbol[i+1:]
				}

				if _, ok := known[symbol]; !ok {
					t.Errorf("%v:%d stamps unknown symbol %q", rel, n+1, symbol)
				}
			}
		}
	})
}
