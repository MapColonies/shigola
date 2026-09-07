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

// walkTree calls check for every file in the repository that keep selects,
// passing the repository-relative path and the file's contents.
//
// Shared by the two tests below because both are properties of the whole
// repository rather than of any one package, and both therefore have to read
// the tree rather than build it. The skips are the same for both: vendor/ is
// other people's code and neither test says anything about it.
func walkTree(t *testing.T, keep func(name string) bool, check func(rel, body string)) {
	t.Helper()

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}

		if !keep(d.Name()) {
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

		check(rel, string(body))

		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
}

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
// depends on cgo now, and a shigola built either way is therefore the same
// server.
func TestNoCgoConstraints(t *testing.T) {
	walkTree(t,
		func(name string) bool { return filepath.Ext(name) == ".go" },
		func(rel, body string) {
			for _, line := range strings.Split(body, "\n") {
				// Constraints precede the package clause; stopping there keeps
				// a comment quoting a constraint from failing the test.
				if strings.HasPrefix(line, "package ") {
					break
				}
				if cgoConstraint(line) {
					t.Errorf("%v carries a cgo build constraint: %v", rel, strings.TrimSpace(line))
				}
			}
		})
}

var (
	// cgoEnabledSetting matches a CGO_ENABLED assignment written with a
	// separator: `CGO_ENABLED=1` in a shell script or a Dockerfile ENV,
	// `CGO_ENABLED: "1"` or `CGO_ENABLED: '1'` in compose and workflow YAML,
	// and `"CGO_ENABLED": "1"` in devcontainer.json. Either quote style, on
	// either side, because all of them are valid in the files this scans.
	cgoEnabledSetting = regexp.MustCompile(`CGO_ENABLED["']?\s*[=:]\s*["']?([A-Za-z0-9_${}.]+)`)

	// cgoEnvDirective matches the space-separated Dockerfile form,
	// `ENV CGO_ENABLED 1`, which carries no separator character at all.
	//
	// Kept apart from cgoEnabledSetting rather than folded into it by allowing
	// whitespace as a separator: `CGO_ENABLED no longer decides ...` is prose
	// that a whitespace separator would read as setting it to "no".
	cgoEnvDirective = regexp.MustCompile(`(?i)^\s*(?:ENV|ARG)\s+CGO_ENABLED\s+["']?([A-Za-z0-9_${}.]+)`)

	// cgoBuildArg matches a valueless `ARG CGO_ENABLED`, which does not set
	// cgo so much as hand the decision to whoever passes --build-arg.
	cgoBuildArg = regexp.MustCompile(`(?i)^\s*ARG\s+CGO_ENABLED\s*$`)
)

// cgoEnabledValue returns the value a line sets CGO_ENABLED to, and whether the
// line sets it at all.
func cgoEnabledValue(line string) (string, bool) {
	if cgoBuildArg.MatchString(line) {
		// Named so the failure message reads as what it is rather than as an
		// empty value. The regexp captures nothing: there is no value to read,
		// which is the whole problem with the form.
		return "(from --build-arg)", true
	}
	if m := cgoEnvDirective.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	if m := cgoEnabledSetting.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	return "", false
}

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

// commentPrefix returns the line-comment marker a build-configuration file
// uses, so prose about a setting is not mistaken for the setting.
//
// devcontainer.json is JSONC and genuinely carries `//` comments; everything
// else here is `#`. Matched by name rather than by the .json extension because
// it is the only .json buildConfig admits -- keying off the extension implied a
// breadth this cannot reach.
func commentPrefix(name string) string {
	if name == "devcontainer.json" {
		return "//"
	}
	return "#"
}

// TestNoCgoEnabledInBuildConfig asserts that no build configuration in this tree
// turns cgo on.
//
// TestNoCgoConstraints above says nothing here is *compiled* conditionally on
// cgo. This says the build configuration has stopped acting as though something
// were: with no cgo-dependent package left, a CGO_ENABLED=1 in a compose file or
// a release workflow buys a C toolchain and a dynamically linked binary for a
// dependency that does not exist (MAPCO-11489).
//
// Setting it to 0 is fine and is the point. What this catches is a setting that
// turns cgo on, and an `ARG CGO_ENABLED` that hands the decision to whoever runs
// the build.
//
// What it deliberately does NOT catch is the *absence* of a setting, so a
// deleted `CGO_ENABLED: 0` from a release job passes here. That is not an
// oversight: `go test -race` links a C runtime and so genuinely needs cgo, which
// is why the test commands leave CGO_ENABLED unset rather than pinning it to 0 --
// so "unset" cannot be the failure condition. The cgo-off build step in
// .github/workflows/on_pr_push.yml is what covers the other side.
func TestNoCgoEnabledInBuildConfig(t *testing.T) {
	walkTree(t, buildConfig, func(rel, body string) {
		comment := commentPrefix(filepath.Base(rel))

		for n, line := range strings.Split(body, "\n") {
			// A wholly commented-out line is prose about the setting, not the
			// setting. An inline trailing comment is not stripped, so an
			// `ENV CGO_ENABLED=1 # why` is still caught.
			if strings.HasPrefix(strings.TrimSpace(line), comment) {
				continue
			}

			value, ok := cgoEnabledValue(line)
			if !ok || value == "0" {
				continue
			}

			t.Errorf("%v:%d enables cgo: %v", rel, n+1, strings.TrimSpace(line))
		}
	})
}

// TestCgoEnabledValue covers the forms cgoEnabledValue claims to read. A regexp
// that quietly stops matching one of them turns TestNoCgoEnabledInBuildConfig
// into a test that passes by seeing nothing, which is the failure a guard like
// that exists to rule out.
func TestCgoEnabledValue(t *testing.T) {
	type tcase struct {
		line  string
		value string
		set   bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			value, set := cgoEnabledValue(tc.line)

			if set != tc.set {
				t.Fatalf("set = %v, want %v", set, tc.set)
			}
			if value != tc.value {
				t.Errorf("value = %q, want %q", value, tc.value)
			}
		}
	}

	tests := map[string]tcase{
		"shell assignment":           {line: `CGO_ENABLED=1 go build .`, value: "1", set: true},
		"shell assignment, disabled": {line: `CGO_ENABLED=0 go build .`, value: "0", set: true},
		"dockerfile env":             {line: `ENV CGO_ENABLED=1`, value: "1", set: true},
		"dockerfile env, space form": {line: `ENV CGO_ENABLED 1`, value: "1", set: true},
		"dockerfile arg, valueless":  {line: `ARG CGO_ENABLED`, value: "(from --build-arg)", set: true},
		"yaml, unquoted":             {line: `        CGO_ENABLED: 1`, value: "1", set: true},
		"yaml, double quoted":        {line: `      CGO_ENABLED: "1"`, value: "1", set: true},
		"yaml, single quoted":        {line: `      CGO_ENABLED: '1'`, value: "1", set: true},
		"json, quoted key and value": {line: `      "CGO_ENABLED": "1",`, value: "1", set: true},
		"expanded from a variable":   {line: `CGO_ENABLED=${CGO}`, value: "${CGO}", set: true},
		"prose mentioning the name":  {line: `CGO_ENABLED no longer decides what a binary can do`, set: false},
		"a different variable":       {line: `GOFLAGS=-mod=vendor`, set: false},
		"a longer name is not this":  {line: `CGO_ENABLEDX: 1`, set: false},
		// Over-matching in this direction is deliberate: no such variable
		// exists, and a loud false positive is the safe way to be wrong.
		"a prefixed name does match": {line: `MY_CGO_ENABLED=1`, value: "1", set: true},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestBuildConfig pins which files TestNoCgoEnabledInBuildConfig looks at. The
// cost of getting this wrong is silence, not a failure.
func TestBuildConfig(t *testing.T) {
	type tcase struct {
		name string
		want bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := buildConfig(tc.name); got != tc.want {
				t.Errorf("buildConfig(%q) = %v, want %v", tc.name, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"a workflow":          {name: "on_pr_push.yml", want: true},
		"a yaml workflow":     {name: "action.yaml", want: true},
		"a compose file":      {name: "docker-compose.yml", want: true},
		"a shell script":      {name: "entrypoint.sh", want: true},
		"a dockerfile":        {name: "Dockerfile", want: true},
		"a suffixed image":    {name: "lambda.Dockerfile", want: true},
		"a prefixed image":    {name: "Dockerfile.debug", want: true},
		"a makefile":          {name: "Makefile", want: true},
		"the devcontainer":    {name: "devcontainer.json", want: true},
		"go source":           {name: "cgo_free_test.go", want: false},
		"documentation":       {name: "CONTRIBUTING.md", want: false},
		"an unrelated json":   {name: "openapi.json", want: false},
		"a toml config":       {name: "config.toml", want: false},
		"a dockerignore file": {name: ".dockerignore", want: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
