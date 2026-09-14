package build_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MapColonies/shigola/internal/build"
)

// Release builds stamp this package with `-ldflags "-X <pkg>.Version=..."`. The
// linker silently discards an -X whose symbol does not resolve, so a wrong
// package path produces an image reporting "version not set" and no failure
// anywhere. Import paths are case-sensitive, and that is how MAPCO-11500
// happened. Checked by reading the tree, because a stamp path lives in a
// Dockerfile or a workflow, not in a Go import the compiler could verify.

var (
	// Any package path ending in /internal/build, whatever its owner or case.
	// Depth is open-ended so a /v2 module suffix still matches.
	stampPkgPath = regexp.MustCompile(`(?i)[\w.-]+\.[\w.-]+(?:/[\w.-]+)+/internal/build`)

	// The left-hand side of an -X, up to the `=`. Either quote style, either
	// separator. The `=` keeps `curl -X POST` out.
	ldflagStamp = regexp.MustCompile(`-X[\s=]+["']?([^\s'"=]+)=`)
)

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}

	return root
}

// modulePath reads the module path from go.mod, so there is no second copy of
// it here to disagree with.
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

// stampPathFile reports whether a file can carry a stamp path: build config,
// plus Go source for the CI helper and tag generator flag defaults.
func stampPathFile(name string) bool {
	return buildConfig(name) || filepath.Ext(name) == ".go"
}

// stampComment returns a file's line-comment marker, so prose about a stamp
// path is not read as one.
func stampComment(name string) string {
	if filepath.Ext(name) == ".go" {
		return "//"
	}
	return commentPrefix(name)
}

// stampedSymbols returns the symbols a line stamps with -X. The symbol is
// whatever follows the last dot, so shell variable syntax does not matter.
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

// scanStampFiles calls check for each non-comment line of every file that may
// carry a stamp.
func scanStampFiles(t *testing.T, check func(rel string, n int, line string)) {
	t.Helper()

	walkTree(t, stampPathFile, func(rel, body string) {
		// This file quotes wrong paths on purpose, in its tables below.
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

// TestStampPathMatchesModulePath asserts every stamp path in the tree is the
// module path plus /internal/build, exactly. Broader than the case bug: a path
// still naming upstream fails here too.
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

	// A matcher that goes quiet looks exactly like a correct tree, so require
	// it to have found something -- and to have found it in the Dockerfile,
	// which is the file that carried the defect.
	if len(seen) == 0 {
		t.Fatal("found no version-stamp package paths anywhere in the tree; stampPkgPath has stopped matching")
	}
	if seen["Dockerfile"] == 0 {
		t.Error("found no version-stamp package path in the Dockerfile; either it no longer stamps one or stampPkgPath no longer matches it")
	}
}

// TestStampedSymbolsExist asserts every stamped symbol is a variable this
// package has. `-X pkg.Verison=1.2.3` links cleanly and stamps nothing.
func TestStampedSymbolsExist(t *testing.T) {
	// Referenced so deleting one of the three breaks the build, not the scan.
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

// TestStampPkgPath pins stampPkgPath. A matcher that stops matching turns the
// scan above into a test that passes by seeing nothing.
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

// TestStampedSymbols pins stampedSymbols, for the same reason.
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

// TestStampPathFile pins which files get scanned. Getting this wrong is silent.
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

// TestStampComment pins each file kind's comment marker.
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
