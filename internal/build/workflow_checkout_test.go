package build_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	usesLine = regexp.MustCompile(`^\s*-?\s*uses:\s*['"]?([^'"\s#]+)`)

	// A key and nothing else, so `build:` opens a job but `run: echo hi` does not.
	jobHeader = regexp.MustCompile(`^([A-Za-z0-9_.-]+):\s*(?:#.*)?$`)

	blockScalar = regexp.MustCompile(`:\s*[|>][0-9+-]*\s*$`)
)

func workflowFile(rel string) bool {
	rel = filepath.ToSlash(rel)

	if !strings.HasPrefix(rel, ".github/workflows/") {
		return false
	}

	switch filepath.Ext(rel) {
	case ".yml", ".yaml":
		return true
	}

	return false
}

type yamlLine struct {
	indent int
	text   string
}

// yamlLines drops blanks, comments and block-scalar bodies: these workflows are
// full of `run: |` shell, and a line of it is not a step.
func yamlLines(body string) []yamlLine {
	var (
		lines       []yamlLine
		scalarAt    = -1
		inBlockBody bool
	)

	for _, raw := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(trimmed)

		if inBlockBody {
			if indent > scalarAt {
				continue
			}
			inBlockBody = false
		}

		if blockScalar.MatchString(raw) {
			scalarAt, inBlockBody = indent, true
		}

		lines = append(lines, yamlLine{indent: indent, text: strings.TrimSpace(trimmed)})
	}

	return lines
}

// workflowJobs maps each job to the actions its steps use. Split by indentation
// because no YAML library is vendored.
func workflowJobs(body string) map[string][]string {
	jobs := map[string][]string{}

	var (
		inJobs    bool
		jobIndent = -1
		current   string
	)

	for _, line := range yamlLines(body) {
		if line.indent == 0 {
			inJobs = line.text == "jobs:"
			jobIndent, current = -1, ""

			continue
		}

		if !inJobs {
			continue
		}

		if jobIndent < 0 {
			jobIndent = line.indent
		}

		if line.indent == jobIndent {
			m := jobHeader.FindStringSubmatch(line.text)
			if m == nil {
				continue
			}

			current = m[1]
			jobs[current] = nil

			continue
		}

		if current == "" {
			continue
		}

		if m := usesLine.FindStringSubmatch(line.text); m != nil {
			jobs[current] = append(jobs[current], m[1])
		}
	}

	return jobs
}

// actionUses returns the actions a local composite action runs. A `./` step
// naming no readable action fails: expanding nothing silently is how this guard
// would stop seeing a checkout.
func actionUses(t *testing.T, root, ref string) []string {
	t.Helper()

	dir := filepath.Join(root, filepath.FromSlash(ref))

	for _, name := range []string{"action.yml", "action.yaml"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}

		var uses []string

		for _, line := range yamlLines(string(body)) {
			if m := usesLine.FindStringSubmatch(line.text); m != nil {
				uses = append(uses, m[1])
			}
		}

		return uses
	}

	t.Errorf("step uses %v, which has no action.yml", ref)

	return nil
}

// resolveUses expands local composite actions, so a job that checks out through
// one is not read as having none. Recursive because they nest.
func resolveUses(t *testing.T, root string, uses []string) []string {
	t.Helper()

	seen := map[string]bool{}

	var walk func([]string) []string

	walk = func(in []string) []string {
		out := make([]string, 0, len(in))

		for _, u := range in {
			out = append(out, u)

			if !strings.HasPrefix(u, "./") || seen[u] {
				continue
			}

			seen[u] = true
			out = append(out, walk(actionUses(t, root, u))...)
		}

		return out
	}

	return walk(uses)
}

func usesAction(uses []string, action string) bool {
	for _, u := range uses {
		if name, _, _ := strings.Cut(u, "@"); name == action {
			return true
		}
	}

	return false
}

// TestDockerBuildJobsCheckOutTheTree asserts every workflow job building a
// docker image also checks the tree out, which MAPCO-11501's job did not. The
// floor at the end matters as much: a scan finding no build job would pass
// every check in it.
func TestDockerBuildJobsCheckOutTheTree(t *testing.T) {
	root := repoRoot(t)

	var builders int

	walkTree(t,
		func(name string) bool {
			ext := filepath.Ext(name)

			return ext == ".yml" || ext == ".yaml"
		},
		func(rel, body string) {
			if !workflowFile(rel) {
				return
			}

			for job, uses := range workflowJobs(body) {
				uses = resolveUses(t, root, uses)

				if !usesAction(uses, "docker/build-push-action") {
					continue
				}

				builders++

				if !usesAction(uses, "actions/checkout") {
					t.Errorf("%v job %q builds a docker image without checking the tree out", rel, job)
				}
			}
		})

	if builders == 0 {
		t.Error("no workflow job uses docker/build-push-action; this guard checked nothing")
	}
}

// TestWorkflowJobs covers the parser: a split that stops finding jobs turns the
// guard above into a test that passes by seeing nothing.
func TestWorkflowJobs(t *testing.T) {
	type tcase struct {
		body string
		want map[string][]string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got := workflowJobs(tc.body)

			if len(got) != len(tc.want) {
				t.Fatalf("jobs = %v, want %v", got, tc.want)
			}

			for name, want := range tc.want {
				if strings.Join(got[name], ",") != strings.Join(want, ",") {
					t.Errorf("job %q uses %v, want %v", name, got[name], want)
				}
			}
		}
	}

	tests := map[string]tcase{
		"a job and its steps": {
			body: "jobs:\n  build:\n    steps:\n      - name: Check out\n        uses: actions/checkout@v4\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"steps at the job's own depth": {
			body: "jobs:\n  build:\n    steps:\n    - uses: actions/checkout@v4\n    - uses: docker/build-push-action@v5\n",
			want: map[string][]string{"build": {"actions/checkout@v4", "docker/build-push-action@v5"}},
		},
		"two jobs do not share steps": {
			body: "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v4\n  b:\n    steps:\n      - uses: docker/build-push-action@v6\n",
			want: map[string][]string{"a": {"actions/checkout@v4"}, "b": {"docker/build-push-action@v6"}},
		},
		"a job with no steps at all": {
			body: "jobs:\n  empty:\n    runs-on: ubuntu-latest\n",
			want: map[string][]string{"empty": nil},
		},
		"the trigger block is not a job": {
			body: "on:\n  pull_request:\n  push:\n    branches:\n      - master\njobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"keys after the jobs block are not jobs": {
			body: "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\nname: On push\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a commented-out step is not a step": {
			body: "jobs:\n  build:\n    steps:\n      # - uses: docker/build-push-action@v5\n      - uses: actions/checkout@v4\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a local composite action": {
			body: "jobs:\n  build:\n    steps:\n      - uses: ./.github/actions/shigola-setup-env\n",
			want: map[string][]string{"build": {"./.github/actions/shigola-setup-env"}},
		},
		"a quoted action reference": {
			body: "jobs:\n  build:\n    steps:\n      - uses: 'actions/checkout@v4'\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a trailing comment is not part of the reference": {
			body: "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4 # pinned\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a uses: inside a run block is shell, not a step": {
			body: "jobs:\n  build:\n    steps:\n      - run: |\n          echo \"uses: docker/build-push-action@v5\"\n      - uses: actions/checkout@v4\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a run block does not end the job it sits in": {
			body: "jobs:\n  build:\n    steps:\n      - run: |\n          cat <<EOF\n          notajob:\n          EOF\n      - uses: actions/checkout@v4\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a folded block is skipped too": {
			body: "jobs:\n  build:\n    steps:\n      - run: >\n          uses: docker/build-push-action@v5\n      - uses: actions/checkout@v4\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a job header may carry a trailing comment": {
			body: "jobs:\n  build: # the only one\n    steps:\n      - uses: actions/checkout@v4\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
		"a non-key line at the job depth opens no job": {
			body: "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\n  - stray: value\n",
			want: map[string][]string{"build": {"actions/checkout@v4"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestResolveUses(t *testing.T) {
	root := repoRoot(t)

	type tcase struct {
		uses []string
		want []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got := resolveUses(t, root, tc.uses)

			for _, want := range tc.want {
				if !usesAction(got, want) {
					t.Errorf("resolveUses(%v) = %v, missing %v", tc.uses, got, want)
				}
			}
		}
	}

	tests := map[string]tcase{
		"a composite action brings its checkout": {
			uses: []string{"./.github/actions/shigola-setup-env"},
			want: []string{"actions/checkout", "actions/setup-go"},
		},
		"a nested composite is followed": {
			uses: []string{"./.github/actions/upload-artifact"},
			want: []string{"./.github/actions/shigola-upload-path", "actions/upload-artifact"},
		},
		"a plain action is passed through": {
			uses: []string{"actions/checkout@v4"},
			want: []string{"actions/checkout"},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestWorkflowFile(t *testing.T) {
	type tcase struct {
		rel  string
		want bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := workflowFile(tc.rel); got != tc.want {
				t.Errorf("workflowFile(%q) = %v, want %v", tc.rel, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"a workflow":            {rel: ".github/workflows/on_pr_push.yml", want: true},
		"a yaml workflow":       {rel: ".github/workflows/ogc_cite.yaml", want: true},
		"a composite action":    {rel: ".github/actions/upload-artifact/action.yml", want: false},
		"a compose file":        {rel: "docker-compose.yml", want: false},
		"a nested compose file": {rel: ".github/cite/docker-compose.yml", want: false},
		"go source":             {rel: "internal/build/build.go", want: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestUsesAction(t *testing.T) {
	type tcase struct {
		uses   []string
		action string
		want   bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := usesAction(tc.uses, tc.action); got != tc.want {
				t.Errorf("usesAction(%v, %q) = %v, want %v", tc.uses, tc.action, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"an exact match":        {uses: []string{"actions/checkout@v4"}, action: "actions/checkout", want: true},
		"a different major":     {uses: []string{"actions/checkout@v5"}, action: "actions/checkout", want: true},
		"pinned to a sha":       {uses: []string{"actions/checkout@a1b2c3d"}, action: "actions/checkout", want: true},
		"no ref at all":         {uses: []string{"actions/checkout"}, action: "actions/checkout", want: true},
		"found among others":    {uses: []string{"actions/setup-go@v5", "actions/checkout@v4"}, action: "actions/checkout", want: true},
		"absent":                {uses: []string{"actions/setup-go@v5"}, action: "actions/checkout", want: false},
		"a longer name is not":  {uses: []string{"actions/checkout-files@v4"}, action: "actions/checkout", want: false},
		"a fork is not the one": {uses: []string{"someone/actions/checkout@v4"}, action: "actions/checkout", want: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
