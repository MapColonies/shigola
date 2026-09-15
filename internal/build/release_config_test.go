package build_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// release-please takes the version and the changelog from files in this tree,
// and every way those files can be wrong is silent. A config-file: naming a
// path that does not exist, a manifest already carrying a version, a
// target-branch that is not the branch the workflow triggers on -- each of
// those is a workflow run that succeeds and opens no release pull request,
// which is the same shape of failure as MAPCO-11500 and MAPCO-11501: green,
// and doing nothing. None of it is checkable by building or running the tree,
// so it is read here, the way stamp_path_test.go reads stamp paths.

// A plain X.Y.Z, which is what these files carry. Deliberately not semver: a
// `v` prefix and a prerelease suffix are both rejected, because a manifest or
// an initial-version carrying either is a misconfiguration rather than a
// version this tree should read past.
var plainVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// A `key: value` line. The anchor is what does the work: a step's `- name:`
// list item starts with `-` and so never matches, and shell inside a `run:`
// block never reaches here because yamlLines drops block scalars. The charset
// only keeps the match to the key shape these workflows actually use.
var workflowInputLine = regexp.MustCompile(`^([a-z][a-z-]*):\s*(\S+)`)

const (
	releaseConfigFile   = "release-please-config.json"
	releaseManifestFile = ".release-please-manifest.json"
	releaseAction       = "googleapis/release-please-action"
)

// releasePleaseConfig is the part of the config this file has something to say
// about. Unknown keys are ignored, so adding one to the JSON does not break
// the suite.
type releasePleaseConfig struct {
	BootstrapSHA string `json:"bootstrap-sha"`
	Packages     map[string]struct {
		ReleaseType    string `json:"release-type"`
		ChangelogPath  string `json:"changelog-path"`
		InitialVersion string `json:"initial-version"`
	} `json:"packages"`
}

// parseVersion returns a version's major, minor and patch.
func parseVersion(v string) (major, minor, patch int, ok bool) {
	m := plainVersion.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, 0, false
	}

	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	patch, _ = strconv.Atoi(m[3])

	return major, minor, patch, true
}

// workflowPushBranches returns the branches a workflow's triggers name.
// Indentation-driven, because no YAML library is vendored.
func workflowPushBranches(body string) []string {
	var (
		branches []string
		listAt   = -1
	)

	for _, line := range yamlLines(body) {
		if listAt >= 0 {
			if line.indent > listAt {
				if item, ok := strings.CutPrefix(line.text, "- "); ok {
					branches = append(branches, strings.Trim(strings.TrimSpace(item), `'"`))
				}

				continue
			}

			listAt = -1
		}

		if line.text == "branches:" {
			listAt = line.indent
		}
	}

	return branches
}

// workflowInputs returns the `key: value` lines below a workflow's top level.
// A key appearing more than once keeps its last value, which is why callers
// only ask it for keys these workflows carry once.
func workflowInputs(body string) map[string]string {
	inputs := map[string]string{}

	for _, line := range yamlLines(body) {
		if line.indent == 0 {
			continue
		}

		if m := workflowInputLine.FindStringSubmatch(line.text); m != nil {
			inputs[m[1]] = strings.Trim(m[2], `'"`)
		}
	}

	return inputs
}

func readReleaseConfig(t *testing.T) releasePleaseConfig {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), releaseConfigFile))
	if err != nil {
		t.Fatalf("reading %v: %v", releaseConfigFile, err)
	}

	var config releasePleaseConfig
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatalf("parsing %v: %v", releaseConfigFile, err)
	}

	return config
}

// TestReleaseConfigMakesTheFirstReleaseMajor asserts the configured initial
// version is a major one. Shigola removed the whole conventional tile surface,
// so no 0.x describes the step from Tegola honestly -- and release-please
// would otherwise have nothing to bump from and fall back to a default.
func TestReleaseConfigMakesTheFirstReleaseMajor(t *testing.T) {
	config := readReleaseConfig(t)

	pkg, ok := config.Packages["."]
	if !ok {
		t.Fatalf("%v configures no package at \".\"; nothing releases this repository", releaseConfigFile)
	}

	major, minor, patch, ok := parseVersion(pkg.InitialVersion)
	switch {
	case !ok:
		t.Errorf("initial-version is %q, which is not a version", pkg.InitialVersion)
	case major < 1:
		t.Errorf("initial-version is %q; the first release must be a major one", pkg.InitialVersion)
	case minor != 0 || patch != 0:
		t.Errorf("initial-version is %q; a major release ends .0.0", pkg.InitialVersion)
	}

	// Exclusive, and the commit it names must still be reachable, or the first
	// release reads the inherited upstream history as unreleased work.
	if len(config.BootstrapSHA) != 40 {
		t.Errorf("bootstrap-sha is %q, want a full 40-character commit sha", config.BootstrapSHA)
	}

	// The changelog release-please appends to is the one carrying Tegola's
	// history. Pointed at a path that does not exist, it would create a second
	// changelog and leave that history behind.
	if _, err := os.Stat(filepath.Join(repoRoot(t), pkg.ChangelogPath)); err != nil {
		t.Errorf("changelog-path is %q, which does not exist: %v", pkg.ChangelogPath, err)
	}
}

// TestReleaseManifestNeverDropsBelowMajor asserts the manifest does not carry a
// version below 1.0.0. Empty is correct before the first release -- an absent
// version is what makes it an initial one -- and after it, the release pull
// request writes the released version here. Either way a 0.x in this file
// means the next release is not the major the config asks for.
func TestReleaseManifestNeverDropsBelowMajor(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), releaseManifestFile))
	if err != nil {
		t.Fatalf("reading %v: %v", releaseManifestFile, err)
	}

	var manifest map[string]string
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("parsing %v: %v", releaseManifestFile, err)
	}

	version, ok := manifest["."]
	if !ok {
		return
	}

	major, _, _, ok := parseVersion(version)
	if !ok {
		t.Fatalf("%v records %q for \".\", which is not a version", releaseManifestFile, version)
	}

	if major < 1 {
		t.Errorf("%v records %q for \".\"; this repository released 1.0.0 and cannot go back", releaseManifestFile, version)
	}
}

// TestReleaseWorkflowAgreesWithItsConfig asserts the workflow running
// release-please names files that exist and releases the branch it triggers
// on. A wrong filename is not an error to release-please: it falls back to its
// own defaults, and the config in this tree stops being the one in force.
func TestReleaseWorkflowAgreesWithItsConfig(t *testing.T) {
	root := repoRoot(t)

	var runners int

	walkTree(t,
		func(name string) bool {
			ext := filepath.Ext(name)

			return ext == ".yml" || ext == ".yaml"
		},
		func(rel, body string) {
			if !workflowFile(rel) {
				return
			}

			var uses []string
			for _, jobUses := range workflowJobs(body) {
				uses = append(uses, resolveUses(t, root, jobUses)...)
			}

			if !usesAction(uses, releaseAction) {
				return
			}

			runners++

			inputs := workflowInputs(body)

			for key, want := range map[string]string{
				"config-file":   releaseConfigFile,
				"manifest-file": releaseManifestFile,
			} {
				got, ok := inputs[key]
				if !ok {
					t.Errorf("%v runs release-please without %v, so it uses release-please's defaults rather than this tree's config", rel, key)

					continue
				}

				if got != want {
					t.Errorf("%v sets %v to %q, want %q", rel, key, got, want)
				}

				if _, err := os.Stat(filepath.Join(root, got)); err != nil {
					t.Errorf("%v sets %v to %q, which does not exist: %v", rel, key, got, err)
				}
			}

			target, ok := inputs["target-branch"]
			if !ok {
				t.Errorf("%v runs release-please without target-branch, so which branch it releases depends on a repository setting", rel)

				return
			}

			branches := workflowPushBranches(body)
			if len(branches) == 0 {
				t.Errorf("%v names no push branches; release-please would never run", rel)

				return
			}

			if !slices.Contains(branches, target) {
				t.Errorf("%v releases %q but triggers on %v; a merge to the branch it releases would open nothing", rel, target, branches)
			}
		})

	// A scan that stops finding the workflow would pass every check above.
	if runners != 1 {
		t.Errorf("found %d workflows running %v, want exactly 1", runners, releaseAction)
	}
}

// TestParseVersion pins parseVersion. The checks above are only as good as it.
func TestParseVersion(t *testing.T) {
	type tcase struct {
		version             string
		major, minor, patch int
		ok                  bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			major, minor, patch, ok := parseVersion(tc.version)

			if ok != tc.ok {
				t.Fatalf("parseVersion(%q) ok = %v, want %v", tc.version, ok, tc.ok)
			}
			if major != tc.major || minor != tc.minor || patch != tc.patch {
				t.Errorf("parseVersion(%q) = %d.%d.%d, want %d.%d.%d",
					tc.version, major, minor, patch, tc.major, tc.minor, tc.patch)
			}
		}
	}

	tests := map[string]tcase{
		"a major release":          {version: "1.0.0", major: 1, ok: true},
		"a later major":            {version: "12.0.0", major: 12, ok: true},
		"a minor release":          {version: "1.4.0", major: 1, minor: 4, ok: true},
		"a patch release":          {version: "1.4.9", major: 1, minor: 4, patch: 9, ok: true},
		"a pre-1.0 version":        {version: "0.17.0", minor: 17, ok: true},
		"a v prefix is not one":    {version: "v1.0.0"},
		"a prerelease is not one":  {version: "1.0.0-rc1"},
		"two components are not":   {version: "1.0"},
		"the empty string is not":  {version: ""},
		"a range is not a version": {version: ">=1.0.0"},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestWorkflowPushBranches pins the trigger scan, which is the half of
// TestReleaseWorkflowAgreesWithItsConfig that has to read YAML by hand.
func TestWorkflowPushBranches(t *testing.T) {
	type tcase struct {
		body string
		want []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got := workflowPushBranches(tc.body)

			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("workflowPushBranches = %v, want %v", got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"one branch": {
			body: "on:\n  push:\n    branches:\n      - master\n",
			want: []string{"master"},
		},
		"several branches": {
			body: "on:\n  push:\n    branches:\n      - master\n      - development\n",
			want: []string{"master", "development"},
		},
		"quoted": {
			body: "on:\n  push:\n    branches:\n      - 'master'\n",
			want: []string{"master"},
		},
		"the list ends where the indent does": {
			body: "on:\n  push:\n    branches:\n      - master\n  workflow_dispatch:\njobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\n",
			want: []string{"master"},
		},
		"a branch named in a run block is not a trigger": {
			body: "jobs:\n  build:\n    steps:\n      - run: |\n          branches:\n          - development\n      - uses: actions/checkout@v4\n",
			want: nil,
		},
		"no trigger at all": {
			body: "on: [push, pull_request]\njobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\n",
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestWorkflowInputs pins the input scan, for the same reason.
func TestWorkflowInputs(t *testing.T) {
	type tcase struct {
		body string
		key  string
		want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := workflowInputs(tc.body)[tc.key]; got != tc.want {
				t.Errorf("workflowInputs[%q] = %q, want %q", tc.key, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"a step input": {
			body: "jobs:\n  release:\n    steps:\n      - uses: googleapis/release-please-action@v4\n        with:\n          config-file: release-please-config.json\n",
			key:  "config-file",
			want: "release-please-config.json",
		},
		"a dotfile value": {
			body: "        with:\n          manifest-file: .release-please-manifest.json\n",
			key:  "manifest-file",
			want: ".release-please-manifest.json",
		},
		"quoted": {
			body: "        with:\n          target-branch: 'master'\n",
			key:  "target-branch",
			want: "master",
		},
		"a top-level key is not an input": {
			body: "name: Release please\n",
			key:  "name",
			want: "",
		},
		"a list item is not an input": {
			body: "    steps:\n      - name: Run release-please\n",
			key:  "name",
			want: "",
		},
		"shell inside a run block is not an input": {
			body: "    steps:\n      - run: |\n          target-branch: development\n",
			key:  "target-branch",
			want: "",
		},
		"absent": {
			body: "        with:\n          token: x\n",
			key:  "target-branch",
			want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
