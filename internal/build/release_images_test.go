package build_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A release publishes container images, and every way that can go wrong is
// quiet. An architecture dropped from `platforms` still pushes a valid
// manifest, so the release looks published and half the fleet cannot pull it.
// A missing `latest` still pushes the versioned tag. An assertion that runs
// the image this runner built rather than the image the registry now serves
// passes while the registry holds something else entirely.
//
// None of that fails a build. It is the shape MAPCO-11500 and MAPCO-11501 both
// had -- green, and wrong -- and none of it is reachable by compiling or
// running this tree, so the workflow is read here (MAPCO-11503).

const (
	buildPushAction = "docker/build-push-action"
	loginAction     = "docker/login-action"

	// The condition gating everything a release does. Matched as a substring:
	// a step is free to `&&` further conditions onto it.
	releaseOnly = "github.event_name == 'release'"
)

// The architectures every published image carries. A release that publishes
// fewer is a release half the fleet cannot run.
var publishedPlatforms = []string{"linux/amd64", "linux/arm64"}

// A secret reference, whatever spacing the expression uses. The group is the
// secret's name.
var secretExpression = regexp.MustCompile(`\$\{\{\s*secrets\.([A-Za-z0-9_]+)\s*\}\}`)

// workflowStep is one step of a workflow job: what it runs, what gates it, and
// the inputs it passes.
type workflowStep struct {
	name   string
	uses   string
	ifCond string
	run    string
	with   map[string]string
}

func yamlFile(name string) bool {
	switch filepath.Ext(name) {
	case ".yml", ".yaml":
		return true
	}

	return false
}

// yamlValue reads the right-hand side of a `key: value` line.
func yamlValue(s string) string {
	s = strings.TrimSpace(s)

	// A `#` opens a comment only after whitespace, so a value carrying one
	// survives.
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}

	// Matching quotes only. `env.ACR_URL != ''` ends in a quote that is part
	// of the value, and trimming quote characters off both ends would eat it.
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}

	return s
}

// setStepKey applies one `key: value` line to a step. It returns the indent a
// `run:` block scalar's body sits under, or -1 for every other line.
func setStepKey(step *workflowStep, text string, indent int) int {
	key, value, ok := strings.Cut(text, ":")
	if !ok {
		return -1
	}

	value = yamlValue(value)

	switch strings.TrimSpace(key) {
	case "name":
		step.name = value
	case "uses":
		step.uses = value
	case "if":
		step.ifCond = value
	case "run":
		// `run: |` and `run: >` carry their shell below; `run: echo hi` is
		// the whole of it.
		if value == "" || strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
			return indent
		}

		step.run = value
	}

	return -1
}

// workflowSteps returns a workflow's steps in file order, carrying the shell of
// any `run:` block. Every other scan in this package reads yamlLines, which
// drops block scalars on purpose -- shell is not structure. Here the shell is
// the thing being checked, so this walks the raw lines instead.
//
// Indentation-driven, because no YAML library is vendored. Steps from every job
// in the file arrive in one list: what matters below is what a step does and
// what gates it, not which job holds it.
func workflowSteps(body string) []workflowStep {
	var (
		steps       []workflowStep
		current     *workflowStep
		runLines    []string
		withBlock   string
		stepsAt     = -1
		itemAt      = -1
		withAt      = -1
		withBlockAt = -1
		runAt       = -1
	)

	closeRun := func() {
		if runAt < 0 {
			return
		}

		// Trailing blanks are the file's own final newline as often as they
		// are the step's, and neither says anything about the shell.
		current.run = strings.TrimRight(strings.Join(runLines, "\n"), "\n")
		runAt, runLines = -1, nil
	}

	flush := func() {
		if current == nil {
			return
		}

		closeRun()

		steps = append(steps, *current)
		current = nil
	}

	for _, raw := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(raw, " \t")
		indent := len(raw) - len(trimmed)

		// Inside a run block every line is shell, including blank ones and
		// anything shaped like a key.
		if runAt >= 0 {
			if trimmed == "" || indent > runAt {
				runLines = append(runLines, strings.TrimSpace(trimmed))

				continue
			}

			closeRun()
		}

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		text := strings.TrimSpace(trimmed)
		item, isItem := strings.CutPrefix(text, "- ")

		// These workflows write steps both ways: indented under `steps:`, and
		// at its own column. So the list ends at the first line left of it, or
		// at the first line level with it that is not another list item.
		if stepsAt >= 0 && (indent < stepsAt || (indent == stepsAt && !isItem)) {
			flush()

			stepsAt, itemAt, withAt = -1, -1, -1
			withBlock, withBlockAt = "", -1
		}

		if text == "steps:" {
			flush()

			stepsAt, itemAt, withAt = indent, -1, -1
			withBlock, withBlockAt = "", -1

			continue
		}

		if stepsAt < 0 {
			continue
		}

		if isItem && (itemAt < 0 || indent == itemAt) {
			flush()

			itemAt, withAt = indent, -1
			withBlock, withBlockAt = "", -1
			current = &workflowStep{with: map[string]string{}}

			// `- ` is two characters wide, so the step's own keys line up two
			// columns right of the list item.
			runAt = setStepKey(current, item, indent+2)

			continue
		}

		if current == nil {
			continue
		}

		if withBlockAt >= 0 {
			if indent > withBlockAt {
				if prev, ok := current.with[withBlock]; ok {
					current.with[withBlock] = prev + "\n" + text
				} else {
					current.with[withBlock] = text
				}

				continue
			}

			withBlock, withBlockAt = "", -1
		}

		switch {
		case indent == itemAt+2:
			withAt = -1

			if text == "with:" {
				withAt = indent

				continue
			}

			runAt = setStepKey(current, text, indent)
		case withAt >= 0 && indent > withAt:
			key, value, ok := strings.Cut(text, ":")
			if !ok {
				continue
			}

			key, value = strings.TrimSpace(key), yamlValue(value)

			// `tags: |` over several lines and `tags: a,b` on one are the
			// same input written two ways, and the guards below read these
			// values by substring. Folding the block into its key keeps a
			// reformat from reading as a deleted tag.
			if value == "|" || value == ">" {
				withBlock, withBlockAt = key, indent

				continue
			}

			current.with[key] = value
		}
	}

	flush()

	return steps
}

// shellCode returns the lines of a run block that actually run. The comments in
// these blocks explain the flags below them by name, so a guard reading the
// whole body is satisfied by prose about a flag that was deleted -- which is
// the failure this package exists to catch, reproduced inside the guard.
func shellCode(run string) string {
	var code []string

	for _, line := range strings.Split(run, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code = append(code, line)
		}
	}

	return strings.Join(code, "\n")
}

// pushesImage reports whether a step pushes a built image to a registry.
func pushesImage(step workflowStep) bool {
	return stepUses(step, buildPushAction) && step.with["push"] == "true"
}

// stepUses reports whether a step runs an action. usesAction takes the list a
// job scan produces; a step carries the one reference.
func stepUses(step workflowStep, action string) bool {
	return usesAction([]string{step.uses}, action)
}

// releasePushAt returns the index of the step that publishes on a release, or
// -1 where the workflow has none. The last one wins, so a workflow growing a
// second publish step is read as publishing at the later one rather than
// silently checking only the first.
func releasePushAt(steps []workflowStep) int {
	at := -1

	for i, step := range steps {
		if pushesImage(step) && strings.Contains(step.ifCond, releaseOnly) {
			at = i
		}
	}

	return at
}

// forEachWorkflow calls check with the steps of every workflow in the tree.
func forEachWorkflow(t *testing.T, check func(rel string, steps []workflowStep)) {
	t.Helper()

	walkTree(t, yamlFile, func(rel, body string) {
		if !workflowFile(rel) {
			return
		}

		check(rel, workflowSteps(body))
	})
}

// TestEveryPushedImageIsMultiArch asserts no workflow publishes an image for
// one architecture. A single-platform push is not an error to buildx -- it
// writes a perfectly valid manifest naming one platform, and the failure lands
// on whoever pulls it.
func TestEveryPushedImageIsMultiArch(t *testing.T) {
	var pushes int

	forEachWorkflow(t, func(rel string, steps []workflowStep) {
		for _, step := range steps {
			if !pushesImage(step) {
				continue
			}

			pushes++

			for _, platform := range publishedPlatforms {
				if !strings.Contains(step.with["platforms"], platform) {
					t.Errorf("%v step %q pushes platforms %q, missing %v", rel, step.name, step.with["platforms"], platform)
				}
			}
		}
	})

	// A scan that stopped finding pushes would pass every check above.
	if pushes == 0 {
		t.Error("no workflow step pushes a docker image; this guard checked nothing")
	}
}

// TestReleaseTagsTheVersionAndMovesLatest asserts the push a release performs
// carries both tags. Only the versioned tag makes the release unfindable by
// anything tracking latest; only latest makes it unpinnable.
func TestReleaseTagsTheVersionAndMovesLatest(t *testing.T) {
	var releasePushes int

	forEachWorkflow(t, func(rel string, steps []workflowStep) {
		for _, step := range steps {
			if !pushesImage(step) || !strings.Contains(step.ifCond, releaseOnly) {
				continue
			}

			releasePushes++

			tags := step.with["tags"]

			if !strings.Contains(tags, "VERSION") {
				t.Errorf("%v step %q tags %q, which carries no release version", rel, step.name, tags)
			}

			if !strings.Contains(tags, ":latest") {
				t.Errorf("%v step %q tags %q, so latest never moves", rel, step.name, tags)
			}
		}
	})

	if releasePushes != 1 {
		t.Errorf("found %d steps pushing on a release, want exactly 1", releasePushes)
	}
}

// TestRegistryCredentialsComeFromSecrets asserts the registry login reads every
// credential from a secret. A literal here is a credential in the tree, and a
// value read from anywhere else is one that cannot be rotated.
func TestRegistryCredentialsComeFromSecrets(t *testing.T) {
	var logins int

	forEachWorkflow(t, func(rel string, steps []workflowStep) {
		for _, step := range steps {
			if !stepUses(step, loginAction) {
				continue
			}

			logins++

			for _, key := range []string{"registry", "username", "password"} {
				value, ok := step.with[key]
				if !ok {
					t.Errorf("%v step %q logs in without %v", rel, step.name, key)

					continue
				}

				if !secretExpression.MatchString(value) {
					t.Errorf("%v step %q takes %v from %q, want a ${{ secrets.* }} expression", rel, step.name, key, value)
				}
			}
		}
	})

	if logins == 0 {
		t.Error("no workflow step logs in to a registry; this guard checked nothing")
	}
}

// TestReleaseAssertsTheImagesItPublished asserts a release reads its own images
// back out of the registry. The step before the push asserts the image this
// runner built from the same build args, which is worth having and is a
// different claim: it says the args were right, not that the registry serves
// what they produced -- and it can only ever see the one architecture buildx
// will load.
func TestReleaseAssertsTheImagesItPublished(t *testing.T) {
	var asserted int

	forEachWorkflow(t, func(rel string, steps []workflowStep) {
		pushAt := releasePushAt(steps)
		if pushAt < 0 {
			return
		}

		// After the push, never before it.
		for _, step := range steps[pushAt+1:] {
			code := shellCode(step.run)

			if !strings.Contains(step.ifCond, releaseOnly) ||
				!strings.Contains(code, "docker run") ||
				!strings.Contains(code, "VERSION") {
				continue
			}

			asserted++

			// Pulled rather than taken from the build cache: the runner just
			// built these layers, so without this the assertion never reaches
			// the registry and proves nothing about what it holds.
			if !strings.Contains(code, "--pull always") {
				t.Errorf("%v step %q runs an image it did not pull, so it may be asserting the locally built one", rel, step.name)
			}

			for _, platform := range publishedPlatforms {
				if !strings.Contains(code, platform) {
					t.Errorf("%v step %q asserts no %v image, leaving that half of the manifest unchecked", rel, step.name, platform)
				}
			}
		}
	})

	if asserted != 1 {
		t.Errorf("found %d steps asserting a published image, want exactly 1", asserted)
	}
}

// TestReleaseRefusesToPublishWithoutCredentials asserts a release checks its
// credentials before it needs them. Without this the push still fails, but
// late and in the wrong place: the image reference falls back to a first
// component docker reads as a Docker Hub path rather than a registry host, so
// the release builds for several minutes and then tries to publish to a
// registry we do not own, failing on authorization and naming no secret.
func TestReleaseRefusesToPublishWithoutCredentials(t *testing.T) {
	var preflights int

	forEachWorkflow(t, func(rel string, steps []workflowStep) {
		pushAt := releasePushAt(steps)
		if pushAt < 0 {
			return
		}

		// Taken from the login step rather than written out here, so
		// changing which registry this publishes to cannot leave the check
		// guarding secrets nothing reads any more.
		var (
			needed []string
			seen   = map[string]bool{}
		)

		for _, step := range steps {
			if !stepUses(step, loginAction) {
				continue
			}

			for _, key := range []string{"registry", "username", "password"} {
				for _, m := range secretExpression.FindAllStringSubmatch(step.with[key], -1) {
					if !seen[m[1]] {
						seen[m[1]] = true
						needed = append(needed, m[1])
					}
				}
			}
		}

		// Before the push, and before the login that would otherwise be the
		// first thing to notice.
		for _, step := range steps[:pushAt] {
			code := shellCode(step.run)

			if !strings.Contains(step.ifCond, releaseOnly) || !strings.Contains(code, "::error::") {
				continue
			}

			preflights++

			if len(needed) == 0 {
				t.Errorf("%v checks credentials but no step logs in with a secret, so this checked nothing", rel)
			}

			for _, secret := range needed {
				if !strings.Contains(code, secret) {
					t.Errorf("%v step %q does not check %v, which the registry login reads; a release missing it fails inside buildx instead", rel, step.name, secret)
				}
			}
		}
	})

	if preflights != 1 {
		t.Errorf("found %d steps checking release credentials, want exactly 1", preflights)
	}
}

// TestWorkflowSteps covers the parser. Every guard above is a scan with a floor
// under it, and a parser that quietly stopped finding steps would trip the
// floor rather than pass -- but one that found steps and lost their `with:` or
// their shell would pass while checking nothing, which is the failure this
// pins.
func TestWorkflowSteps(t *testing.T) {
	type tcase struct {
		body string
		want []workflowStep
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			got := workflowSteps(tc.body)

			if len(got) != len(tc.want) {
				t.Fatalf("steps = %+v, want %+v", got, tc.want)
			}

			for i, want := range tc.want {
				if got[i].name != want.name {
					t.Errorf("step %d name = %q, want %q", i, got[i].name, want.name)
				}
				if got[i].uses != want.uses {
					t.Errorf("step %d uses = %q, want %q", i, got[i].uses, want.uses)
				}
				if got[i].ifCond != want.ifCond {
					t.Errorf("step %d if = %q, want %q", i, got[i].ifCond, want.ifCond)
				}
				if got[i].run != want.run {
					t.Errorf("step %d run = %q, want %q", i, got[i].run, want.run)
				}

				// Both directions: a key the parser invents is as wrong as one
				// it drops, and only comparing the wanted keys would miss it.
				if len(got[i].with) != len(want.with) {
					t.Errorf("step %d with = %v, want %v", i, got[i].with, want.with)
				}

				for key, value := range want.with {
					if got[i].with[key] != value {
						t.Errorf("step %d with[%q] = %q, want %q", i, key, got[i].with[key], value)
					}
				}
			}
		}
	}

	tests := map[string]tcase{
		"a step indented under its list": {
			body: "jobs:\n  build:\n    steps:\n      - name: Check out\n        uses: actions/checkout@v4\n",
			want: []workflowStep{{name: "Check out", uses: "actions/checkout@v4"}},
		},
		// How every job in on_release_publish.yml is written.
		"a step level with its list": {
			body: "jobs:\n  build:\n    steps:\n    - name: Check out\n      uses: actions/checkout@v4\n",
			want: []workflowStep{{name: "Check out", uses: "actions/checkout@v4"}},
		},
		"with: inputs": {
			body: "    steps:\n    - uses: docker/build-push-action@v5\n      with:\n        push: true\n        platforms: linux/amd64,linux/arm64\n",
			want: []workflowStep{{
				uses: "docker/build-push-action@v5",
				with: map[string]string{"push": "true", "platforms": "linux/amd64,linux/arm64"},
			}},
		},
		// The tags input is the one value in these workflows whose colons are
		// not key separators.
		"a value carrying colons": {
			body: "    steps:\n    - uses: docker/build-push-action@v5\n      with:\n        tags: ${{ env.IMAGE }}:${{ env.VERSION }},${{ env.IMAGE }}:latest\n",
			want: []workflowStep{{
				uses: "docker/build-push-action@v5",
				with: map[string]string{"tags": "${{ env.IMAGE }}:${{ env.VERSION }},${{ env.IMAGE }}:latest"},
			}},
		},
		// The same input two ways: on_pr_push.yml already writes build-args in
		// the block form, and tags is one reformat away from it.
		"a with: value written as a block": {
			body: "    steps:\n    - uses: docker/build-push-action@v5\n      with:\n        tags: |\n          img:1.0\n          img:latest\n        push: true\n",
			want: []workflowStep{{
				uses: "docker/build-push-action@v5",
				with: map[string]string{"tags": "img:1.0\nimg:latest", "push": "true"},
			}},
		},
		"a condition keeps its quotes": {
			body: "    steps:\n    - name: Publish\n      if: github.event_name == 'release'\n",
			want: []workflowStep{{name: "Publish", ifCond: "github.event_name == 'release'"}},
		},
		"a condition ending in a quoted empty string": {
			body: "    steps:\n    - name: Publish\n      if: env.ACR_URL != ''\n",
			want: []workflowStep{{name: "Publish", ifCond: "env.ACR_URL != ''"}},
		},
		"a run block is carried": {
			body: "    steps:\n    - name: Assert\n      run: |\n        docker run --rm img version\n        exit 0\n",
			want: []workflowStep{{name: "Assert", run: "docker run --rm img version\nexit 0"}},
		},
		"an inline run": {
			body: "    steps:\n    - run: echo hi\n",
			want: []workflowStep{{run: "echo hi"}},
		},
		// A run block is shell, so nothing in it opens a step, closes the list
		// or sets a key.
		"a run block does not end the step it sits in": {
			body: "    steps:\n    - name: Assert\n      run: |\n        for x in a b; do\n        - not a step\n        uses: not/an-action\n        done\n    - uses: actions/checkout@v4\n",
			want: []workflowStep{
				{name: "Assert", run: "for x in a b; do\n- not a step\nuses: not/an-action\ndone"},
				{uses: "actions/checkout@v4"},
			},
		},
		"a blank line inside a run block does not end it": {
			body: "    steps:\n    - name: Assert\n      run: |\n        one\n\n        two\n",
			want: []workflowStep{{name: "Assert", run: "one\n\ntwo"}},
		},
		"an env block is not with: inputs": {
			body: "    steps:\n    - uses: docker/build-push-action@v5\n      with:\n        push: true\n      env:\n        push: false\n",
			want: []workflowStep{{
				uses: "docker/build-push-action@v5",
				with: map[string]string{"push": "true"},
			}},
		},
		"two jobs' steps both arrive": {
			body: "jobs:\n  a:\n    steps:\n    - uses: actions/checkout@v4\n  b:\n    steps:\n    - uses: docker/login-action@v3\n",
			want: []workflowStep{{uses: "actions/checkout@v4"}, {uses: "docker/login-action@v3"}},
		},
		"keys after the steps list are not steps": {
			body: "jobs:\n  a:\n    steps:\n    - uses: actions/checkout@v4\n  b:\n    runs-on: ubuntu-22.04\n",
			want: []workflowStep{{uses: "actions/checkout@v4"}},
		},
		"a commented-out step is not a step": {
			body: "    steps:\n    # - uses: docker/build-push-action@v5\n    - uses: actions/checkout@v4\n",
			want: []workflowStep{{uses: "actions/checkout@v4"}},
		},
		"a trailing comment is not part of a value": {
			body: "    steps:\n    - uses: actions/checkout@v4 # pinned\n",
			want: []workflowStep{{uses: "actions/checkout@v4"}},
		},
		"no steps at all": {
			body: "jobs:\n  a:\n    runs-on: ubuntu-22.04\n",
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestYAMLValue pins the value reader, whose quote rule is the one place this
// parser can quietly corrupt a condition rather than fail to find one.
func TestYAMLValue(t *testing.T) {
	type tcase struct {
		in, want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := yamlValue(tc.in); got != tc.want {
				t.Errorf("yamlValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"plain":                      {in: " actions/checkout@v4", want: "actions/checkout@v4"},
		"quoted":                     {in: " 'master'", want: "master"},
		"double quoted":              {in: ` "shigola"`, want: "shigola"},
		"an interior quote survives": {in: " github.event_name == 'release'", want: "github.event_name == 'release'"},
		"a trailing quote survives":  {in: " env.ACR_URL != ''", want: "env.ACR_URL != ''"},
		"a comment is dropped":       {in: " actions/checkout@v4 # pinned", want: "actions/checkout@v4"},
		"a hash without space stays": {in: " image:tag#1", want: "image:tag#1"},
		"a block scalar marker":      {in: " |", want: "|"},
		"empty":                      {in: "", want: ""},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestShellCode pins the comment strip. A guard reading prose instead of shell
// is a guard that passes on a deleted flag.
func TestShellCode(t *testing.T) {
	type tcase struct {
		in, want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			if got := shellCode(tc.in); got != tc.want {
				t.Errorf("shellCode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"a comment naming a flag is not the flag": {
			in:   "# --pull always: why\ndocker run --rm img version",
			want: "docker run --rm img version",
		},
		"an indented comment":   {in: "  # note\n  echo hi", want: "  echo hi"},
		"a trailing hash stays": {in: "echo 'a # b'", want: "echo 'a # b'"},
		"no comments at all":    {in: "echo one\necho two", want: "echo one\necho two"},
		"nothing but comments":  {in: "# one\n# two", want: ""},
		"empty":                 {in: "", want: ""},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
