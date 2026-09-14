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

var (
	// stampPkgPath matches anything shaped like a Go package path ending in the
	// version-stamp package -- whatever its owner, its depth or its case -- so
	// that a wrongly-cased path and a path still naming the upstream project
	// are both found and then compared against the one correct value.
	//
	// The depth is `(?:/[\w.-]+)+` rather than a fixed two segments because a
	// module path is not always host/owner/repo: a v2+ module carries a
	// /v2 suffix, and a fixed depth would match nothing at all on one of
	// those. Matching nothing is the one outcome a guard like this must not
	// have, which is what TestStampPathMatchesModulePath's found-something
	// assertion is for.
	stampPkgPath = regexp.MustCompile(`(?i)[\w.-]+\.[\w.-]+(?:/[\w.-]+)+/internal/build`)

	// ldflagStamp matches the left-hand side of an -X, up to the `=`:
	// `${BUILD_PKG}.Version`, `$Env:BUILD_PKG.GitBranch`, or a path written out
	// in full. Either quote style and either separator, because all of them are
	// valid in the files this scans -- the Dockerfile quotes each -X with
	// single quotes and the workflow leaves them bare, and neither spelling is
	// more correct than the other.
	//
	// The `=` is what keeps `curl -X POST` and friends out: an -X that stamps
	// nothing has no assignment in it.
	ldflagStamp = regexp.MustCompile(`-X[\s=]+["']?([^\s'"=]+)=`)
)

// repoRoot returns the path of the repository root, which these tests read
// rather than build.
func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}

	return root
}

// modulePath returns the module path declared in go.mod, which is the authority
// the stamp paths are checked against -- rather than a constant here, which
// would only move the place the two can disagree.
func modulePath(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
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

// stampComment returns the line-comment marker a file stampPathFile admits
// uses, so that prose about a stamp path is not read as one.
//
// This matters more here than the equivalent does in cgo_free_test.go: the
// rationale comments this repo asks for are exactly the place someone explains
// which path was wrong and why, and a scan that read those would make the
// documented convention fail the build.
func stampComment(name string) string {
	if filepath.Ext(name) == ".go" {
		return "//"
	}
	return commentPrefix(name)
}

// stampedSymbols returns the names of the symbols a line stamps with -X, in
// order. The symbol is whatever follows the last dot of the left-hand side, so
// this does not have to understand how any one shell spells a variable.
func stampedSymbols(line string) []string {
	var symbols []string

	for _, m := range ldflagStamp.FindAllStringSubmatch(line, -1) {
		symbol := m[1]
		if i := strings.LastIndex(symbol, "."); i >= 0 {
			symbol = symbol[i+1:]
		}
		symbols = append(symbols, symbol)
	}

	return symbols
}

// scanStampFiles walks the files that may carry a stamp, calling check for each
// non-comment line. Shared by the two tree-reading tests below.
func scanStampFiles(t *testing.T, check func(rel string, n int, line string)) {
	t.Helper()

	walkTree(t, stampPathFile, func(rel, body string) {
		// This file quotes wrong paths on purpose, in its prose and in the
		// tables below. Reading it would fail the tests with their own
		// examples.
		if filepath.Base(rel) == "stamp_path_test.go" {
			return
		}

		comment := stampComment(filepath.Base(rel))

		for n, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), comment) {
				continue
			}

			check(rel, n+1, line)
		}
	})
}

// TestStampPathMatchesModulePath asserts that every version-stamp package path
// in this tree is the real one, exactly, including case.
//
// Deliberately broader than the case bug that prompted it: the check is that
// each path *equals* the module path plus /internal/build, so a path left
// pointing at the upstream project fails here too.
func TestStampPathMatchesModulePath(t *testing.T) {
	want := modulePath(t) + "/internal/build"

	seen := map[string]int{}

	scanStampFiles(t, func(rel string, n int, line string) {
		for _, got := range stampPkgPath.FindAllString(line, -1) {
			seen[filepath.Base(rel)]++

			if got != want {
				t.Errorf("%v:%d stamps into %q, want %q", rel, n, got, want)
			}
		}
	})

	// A scan that matches nothing passes every assertion above it, so the
	// matcher going quiet would look exactly like the tree being correct. The
	// Dockerfile is named specifically because it is the file that carried the
	// defect, and the one a future edit is most likely to reshape past the
	// regexp.
	if len(seen) == 0 {
		t.Fatal("found no version-stamp package paths anywhere in the tree; stampPkgPath has stopped matching")
	}
	if seen["Dockerfile"] == 0 {
		t.Error("found no version-stamp package path in the Dockerfile; either it no longer stamps one or stampPkgPath no longer matches it")
	}
}

// TestStampedSymbolsExist asserts that every symbol the ldflags stamp into is a
// variable this package actually has.
//
// The same silence covers a renamed variable as covers a wrongly-cased path:
// `-X pkg.Verison=1.2.3` links cleanly and stamps nothing. The compile-time
// references below are the other half -- they are what makes this test fail to
// build, rather than fail to notice, if one of the three is removed.
func TestStampedSymbolsExist(t *testing.T) {
	// Referenced so this set cannot drift from the package by deletion: remove
	// one of the three and this test stops compiling.
	known := map[string]*string{
		"Version":     &build.Version,
		"GitRevision": &build.GitRevision,
		"GitBranch":   &build.GitBranch,
	}

	found := 0

	scanStampFiles(t, func(rel string, n int, line string) {
		for _, symbol := range stampedSymbols(line) {
			found++

			if _, ok := known[symbol]; !ok {
				t.Errorf("%v:%d stamps unknown symbol %q", rel, n, symbol)
			}
		}
	})

	if found == 0 {
		t.Fatal("found no -X stamps anywhere in the tree; ldflagStamp has stopped matching")
	}
}

// TestStampPkgPath covers the forms stampPkgPath claims to read. A regexp that
// quietly stops matching one of them turns TestStampPathMatchesModulePath into
// a test that passes by seeing nothing, which is the failure a guard like that
// exists to rule out.
func TestStampPkgPath(t *testing.T) {
	type tcase struct {
		line string
		want []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got := stampPkgPath.FindAllString(tc.line, -1)

			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("match %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		}
	}

	tests := map[string]tcase{
		"the correct path": {
			line: `ARG BUILDPKG="github.com/MapColonies/shigola/internal/build"`,
			want: []string{"github.com/MapColonies/shigola/internal/build"},
		},
		"the wrongly-cased path": {
			line: `ARG BUILDPKG="github.com/mapcolonies/shigola/internal/build"`,
			want: []string{"github.com/mapcolonies/shigola/internal/build"},
		},
		"a path still naming upstream": {
			line: `versionPkg = flag.String("pkg", "github.com/go-spatial/tegola/internal/build", "")`,
			want: []string{"github.com/go-spatial/tegola/internal/build"},
		},
		// The reason the depth is not fixed at two segments: a v2+ module
		// carries a version suffix, and a fixed depth would match nothing.
		"a versioned module path": {
			line: `ARG BUILDPKG="github.com/MapColonies/shigola/v2/internal/build"`,
			want: []string{"github.com/MapColonies/shigola/v2/internal/build"},
		},
		"a go import line": {
			line: `	"github.com/MapColonies/shigola/internal/build"`,
			want: []string{"github.com/MapColonies/shigola/internal/build"},
		},
		"inside an ldflags string": {
			line: `-ldflags "-X github.com/MapColonies/shigola/internal/build.Version=1.2.3"`,
			want: []string{"github.com/MapColonies/shigola/internal/build"},
		},
		"a host with no dot is not a module path": {
			line: `COPY . /go/src/internal/build`,
			want: nil,
		},
		"a different package of ours": {
			line: `	"github.com/MapColonies/shigola/internal/env"`,
			want: nil,
		},
		"prose naming the package": {
			line: `the version is stamped into internal/build at link time`,
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestStampedSymbols covers the forms stampedSymbols claims to read, for the
// same reason TestStampPkgPath covers stampPkgPath: the cost of this matcher
// getting quietly narrower is a test that notices nothing.
func TestStampedSymbols(t *testing.T) {
	type tcase struct {
		line string
		want []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got := stampedSymbols(tc.line)

			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("symbol %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		}
	}

	tests := map[string]tcase{
		"bare, as the workflow writes it": {
			line: `-X ${BUILD_PKG}.Version=${VERSION}`,
			want: []string{"Version"},
		},
		"single quoted, as the Dockerfile writes it": {
			line: `-X '${BUILD_PKG}.GitRevision=${GIT_REVISION}'`,
			want: []string{"GitRevision"},
		},
		"double quoted": {
			line: `-X "${BUILD_PKG}.GitBranch=${GIT_BRANCH}"`,
			want: []string{"GitBranch"},
		},
		"joined with an equals sign": {
			line: `-X=github.com/MapColonies/shigola/internal/build.Version=1.2.3`,
			want: []string{"Version"},
		},
		"powershell, as the windows job writes it": {
			line: `-X $Env:BUILD_PKG.GitBranch=$Env:GIT_BRANCH`,
			want: []string{"GitBranch"},
		},
		"several on one line": {
			line: `-ldflags "-w -X ${P}.Version=${V} -X ${P}.GitRevision=${R} -X ${P}.GitBranch=${B}"`,
			want: []string{"Version", "GitRevision", "GitBranch"},
		},
		"a misspelled symbol is still read": {
			line: `-X ${BUILD_PKG}.Verison=${VERSION}`,
			want: []string{"Verison"},
		},
		// An -X that assigns nothing is not a stamp. This is what keeps the
		// HTTP verb flag out.
		"an unrelated -X flag": {
			line: `curl -X POST https://example.com/`,
			want: nil,
		},
		"no -X at all": {
			line: `go build -mod vendor -o /opt/shigola`,
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestStampPathFile pins which files the two tree-reading tests look at. The
// cost of getting this wrong is silence, not a failure.
func TestStampPathFile(t *testing.T) {
	type tcase struct {
		name string
		want bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := stampPathFile(tc.name); got != tc.want {
				t.Errorf("stampPathFile(%q) = %v, want %v", tc.name, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"the dockerfile that carried the bug": {name: "Dockerfile", want: true},
		"a release workflow":                  {name: "on_release_publish.yml", want: true},
		"a yaml workflow":                     {name: "action.yaml", want: true},
		"the ci version helper":               {name: "cat_version_envs.go", want: true},
		"a shell script":                      {name: "entrypoint.sh", want: true},
		"a makefile":                          {name: "Makefile", want: true},
		"a toml config":                       {name: "config.toml", want: false},
		"a markdown doc":                      {name: "README.md", want: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestStampComment pins the comment marker each kind of file gets, because
// reading a comment as configuration and failing to read configuration at all
// are both silent.
func TestStampComment(t *testing.T) {
	type tcase struct {
		name string
		want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := stampComment(tc.name); got != tc.want {
				t.Errorf("stampComment(%q) = %q, want %q", tc.name, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"go source":    {name: "cat_version_envs.go", want: "//"},
		"a dockerfile": {name: "Dockerfile", want: "#"},
		"a workflow":   {name: "on_release_publish.yml", want: "#"},
		"the shell":    {name: "run.sh", want: "#"},
		"devcontainer": {name: "devcontainer.json", want: "//"},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
