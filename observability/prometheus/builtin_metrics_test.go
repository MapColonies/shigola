package prometheus_test

import (
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// README.md is this repo's metric reference -- the document an operator builds
// a dashboard or an alert from -- and a good part of what it lists is not ours.
// The go_* and process_* families come from client_golang's default collectors,
// so they change when that dependency is upgraded and not otherwise. Nothing in
// the tree noticed: upgrading client_golang v1.14.0 to v1.24.1 removed
// go_memstats_lookups_total and added five families, and the reference went on
// describing the set from 2022 (MAPCO-11516).
//
// Documentation drift is quiet by construction -- no test fails, no metric stops
// being served -- which is exactly why it is worth a test. An alert written
// against a family that no longer exists never fires, and that is
// indistinguishable from an alert that is not triggering.
//
// The shigola_* families are deliberately out of scope. They are registered
// lazily, when an observer is built and a request reaches it, so an idle
// registry cannot be asked what they are; checking them would mean a different
// and much larger fixture. What this covers is the set shigola does not choose.

// procfsDependent are families the process collector emits only when it can read
// the file behind them, mapped to that file.
//
// The default registry builds the collector with a zero ProcessCollectorOpts
// (registry.go: `MustRegister(NewProcessCollector(ProcessCollectorOpts{}))`), so
// ReportErrors is off and a procfs read that fails drops the family silently --
// Gather succeeds and simply returns less. On a host where /proc is restricted
// that is indistinguishable, from here, from client_golang having removed the
// family, and the reference would be blamed for an unreadable file.
//
// So a family named here is excused only when its file is genuinely unreadable.
// Where the file reads -- CI, and any ordinary container -- the assertion stays
// exactly as strict as for every other family.
var procfsDependent = map[string]string{
	"process_network_receive_bytes_total":  "/proc/self/net/netstat",
	"process_network_transmit_bytes_total": "/proc/self/net/netstat",
}

// builtinHeading matches a metric's own heading in the reference. The families
// here are documented at one level, `##### name`, with the support tags of a
// histogram written as bullets rather than headings.
var builtinHeading = regexp.MustCompile(`^#{5}\s+((?:go|process)_\w+)\s*$`)

// documentedBuiltins returns the go_* and process_* families README.md
// documents.
func documentedBuiltins(t *testing.T) map[string]bool {
	t.Helper()

	body, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading the metric reference: %v", err)
	}

	out := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		if m := builtinHeading.FindStringSubmatch(line); m != nil {
			if out[m[1]] {
				t.Errorf("README.md documents %v twice", m[1])
			}
			out[m[1]] = true
		}
	}

	// A matcher that stops matching would make this a test that passes by
	// comparing nothing against nothing.
	if len(out) == 0 {
		t.Fatal("found no go_* or process_* headings in README.md; either the reference stopped listing them or builtinHeading no longer matches")
	}

	return out
}

// exposedBuiltins returns the go_* and process_* families a shigola process
// actually serves on /metrics.
//
// Read from the default gatherer rather than listed here, so this compares the
// reference against the dependency rather than against a second copy of the
// reference. The observer registers into prometheus.DefaultRegisterer, so this
// is the same registry /metrics is served from.
func exposedBuiltins(t *testing.T) map[string]bool {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gathering the default registry: %v", err)
	}

	out := map[string]bool{}
	for _, f := range families {
		if name := f.GetName(); strings.HasPrefix(name, "go_") || strings.HasPrefix(name, "process_") {
			out[name] = true
		}
	}

	if len(out) == 0 {
		t.Fatal("the default registry exposes no go_* or process_* families; client_golang no longer registers its default collectors")
	}

	return out
}

// readable reports whether a file can be read, which is what decides a
// procfsDependent family's absence between a restricted host and a dependency
// that dropped it.
func readable(path string) bool {
	_, err := os.ReadFile(path)
	return err == nil
}

// sortedNames returns a family-name set in a stable order, so a run reports its
// differences the same way twice.
func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestREADMEDocumentsTheBuiltInMetrics asserts the reference lists exactly the
// built-in families a shigola process serves.
//
// Both directions matter and they fail differently. A documented family that is
// not exposed is the worse one: it is a dashboard panel that stays empty and an
// alert that cannot fire. An exposed family that is not documented is a metric
// nobody knows to use.
func TestREADMEDocumentsTheBuiltInMetrics(t *testing.T) {
	// The process_* collector is built on procfs, so which families exist is a
	// property of the operating system: process_network_receive_bytes_total has
	// no counterpart on darwin or windows. The reference describes a deployed
	// shigola, which is a linux container, so it is compared on linux. CI runs
	// the suite on ubuntu; this skips on a developer's laptop rather than
	// failing there with a difference that is not a defect.
	if runtime.GOOS != "linux" {
		t.Skipf("the built-in metric set is platform-dependent; README.md describes linux, this is %v", runtime.GOOS)
	}

	documented, exposed := documentedBuiltins(t), exposedBuiltins(t)

	for _, name := range sortedNames(documented) {
		if exposed[name] {
			continue
		}

		if src, ok := procfsDependent[name]; ok && !readable(src) {
			t.Logf("%v is documented but not exposed, and %v does not read here -- the host, not the reference", name, src)
			continue
		}

		t.Errorf("README.md documents %v, which is no longer exposed -- an alert on it can never fire", name)
	}

	for _, name := range sortedNames(exposed) {
		if !documented[name] {
			t.Errorf("%v is exposed but README.md does not document it", name)
		}
	}
}

// TestBuiltinHeading pins builtinHeading, so the scan above cannot go quiet.
func TestBuiltinHeading(t *testing.T) {
	type tcase struct {
		line string
		want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			var got string
			if m := builtinHeading.FindStringSubmatch(tc.line); m != nil {
				got = m[1]
			}

			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"a go runtime family":               {line: "##### go_goroutines", want: "go_goroutines"},
		"a process family":                  {line: "##### process_open_fds", want: "process_open_fds"},
		"trailing whitespace, as written":   {line: "#####  go_info ", want: "go_info"},
		"one of ours is not built in":       {line: "##### shigola_build_info", want: ""},
		"the section heading above them":    {line: "#### go runtime information", want: ""},
		"a deeper heading is a label list":  {line: "###### labels", want: ""},
		"a support tag written as a bullet": {line: "* shigola_api_duration_seconds_sum", want: ""},
		"prose naming a family":             {line: "go_goroutines is the number of goroutines.", want: ""},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
