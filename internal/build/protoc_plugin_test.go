package build_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// internal/vectortile is generated code, and three files have to agree about
// one version for it to stay regenerable:
//
//   - go.mod's google.golang.org/protobuf, the runtime the generated file is
//     compiled against;
//   - the .devcontainer image's `go install ...cmd/protoc-gen-go@v...`, the
//     plugin anyone regenerating actually runs;
//   - the `protoc-gen-go v...` line the checked-in .pb.go stamps into itself,
//     which records the plugin that produced what is committed.
//
// Nothing enforces that today. The Dockerfile says "Bump the two together" and
// that is the whole mechanism. Drift is quiet in both directions: generated
// code asserts at compile time only that its gencode API version is one the
// runtime still supports, so a plugin some minor versions ahead of the vendored
// runtime keeps building until it does not, and a regeneration run against a
// plugin behind go.mod rewrites the committed file with no signal at all. This
// branch already produced one such window -- a .pb.go stamped v1.36.12 sitting
// beside a go.mod pinning v1.36.11 -- which is what asked for this (MAPCO-11516).
//
// Read off the tree rather than checked by building, for the same reason
// stamp_path_test.go reads it: a version in a Dockerfile is not something the
// compiler can see.

var (
	// The install pin, `...cmd/protoc-gen-go@v1.36.12`. The group is the
	// version. Anchored on the binary name rather than the module path so the
	// `go run` and `go install` spellings both match.
	protocGenGoPin = regexp.MustCompile(`protoc-gen-go@(v[\w.+-]+)`)

	// The version protoc-gen-go stamps into its own output, written as a
	// tab-indented comment under `// versions:`. Anchored to a comment because
	// that is the only place it appears in a .pb.go.
	protocGenGoStamp = regexp.MustCompile(`^//\s+protoc-gen-go\s+(v[\w.+-]+)`)

	// The go.mod require line for the protobuf runtime. Anchored at the start
	// so a module whose path merely ends in it cannot match.
	protobufRequire = regexp.MustCompile(`^\s*google\.golang\.org/protobuf\s+(v[\w.+-]+)`)
)

// protobufRuntimeVersion reads google.golang.org/protobuf's version from go.mod,
// so the version this file compares against is the one the build resolves rather
// than a second copy written down here.
func protobufRuntimeVersion(t *testing.T) string {
	t.Helper()

	var found []string
	for _, line := range goModLines(t) {
		if m := protobufRequire.FindStringSubmatch(line); m != nil {
			found = append(found, m[1])
		}
	}

	// More than one is a replace directive or a second require, and picking the
	// first would compare against whichever happened to come first in the file.
	if len(found) != 1 {
		t.Fatalf("go.mod names google.golang.org/protobuf %d times, want once: %q", len(found), found)
	}

	return found[0]
}

// TestProtocGenGoPinMatchesProtobufRuntime asserts every protoc-gen-go install
// pin in the tree's build configuration names go.mod's protobuf version.
func TestProtocGenGoPinMatchesProtobufRuntime(t *testing.T) {
	want := protobufRuntimeVersion(t)

	seen := map[string]int{}

	walkTree(t, buildConfig, func(rel, body string) {
		comment := commentPrefix(filepath.Base(rel))

		for n, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), comment) {
				continue
			}

			for _, m := range protocGenGoPin.FindAllStringSubmatch(line, -1) {
				seen[rel]++

				if m[1] != want {
					t.Errorf("%v:%d installs protoc-gen-go@%v, but go.mod requires google.golang.org/protobuf %v -- bump both", rel, n+1, m[1], want)
				}
			}
		}
	})

	// A matcher that goes quiet looks exactly like a tree that agrees with
	// itself, so require it to have found the pin, in the file that carries it.
	if len(seen) == 0 {
		t.Fatal("found no protoc-gen-go install pin anywhere in the tree; either the devcontainer stopped installing it or protocGenGoPin no longer matches")
	}
	if seen[filepath.Join(".devcontainer", "Dockerfile")] == 0 {
		t.Errorf("found no protoc-gen-go pin in .devcontainer/Dockerfile; found them in %v instead", seen)
	}
}

// TestGeneratedProtobufCodeMatchesProtobufRuntime asserts the checked-in
// generated code was produced by a protoc-gen-go of go.mod's version.
//
// This is the half a pin alone does not cover: the Dockerfile can agree with
// go.mod while what is committed came from neither. Failing here means the
// .pb.go is stale -- `cd internal/vectortile && go generate` in the devcontainer
// -- not that the version is wrong.
func TestGeneratedProtobufCodeMatchesProtobufRuntime(t *testing.T) {
	want := protobufRuntimeVersion(t)

	found := 0

	walkTree(t,
		func(name string) bool { return strings.HasSuffix(name, ".pb.go") },
		func(rel, body string) {
			for n, line := range strings.Split(body, "\n") {
				m := protocGenGoStamp.FindStringSubmatch(line)
				if m == nil {
					continue
				}

				found++

				if m[1] != want {
					t.Errorf("%v:%d was generated by protoc-gen-go %v, but go.mod requires google.golang.org/protobuf %v -- regenerate it", rel, n+1, m[1], want)
				}
			}
		})

	if found == 0 {
		t.Fatal("found no protoc-gen-go version stamp in any .pb.go; either nothing is generated here any more or protocGenGoStamp no longer matches")
	}
}

// TestProtocGenGoPin pins protocGenGoPin. A matcher that stops matching turns
// the scan above into a test that passes by seeing nothing.
func TestProtocGenGoPin(t *testing.T) {
	type tcase struct {
		line string
		want []string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			var got []string
			for _, m := range protocGenGoPin.FindAllStringSubmatch(tc.line, -1) {
				got = append(got, m[1])
			}

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
		"the devcontainer's install line": {
			line: `RUN GOBIN=/usr/local/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12`,
			want: []string{"v1.36.12"},
		},
		"a go run spelling": {
			line: `	go run google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12 --help`,
			want: []string{"v1.36.12"},
		},
		"a prerelease": {
			line: `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.37.0-rc.1`,
			want: []string{"v1.37.0-rc.1"},
		},
		"a pseudo-version": {
			line: `go install google.golang.org/protobuf/cmd/protoc-gen-go@v0.0.0-20260101120000-abcdef123456`,
			want: []string{"v0.0.0-20260101120000-abcdef123456"},
		},
		"the grpc plugin is a different binary": {
			line: `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1`,
			want: nil,
		},
		"an unpinned install has no version to compare": {
			line: `go install google.golang.org/protobuf/cmd/protoc-gen-go`,
			want: nil,
		},
		"prose naming the plugin": {
			line: `# Regenerating needs protoc and a matching protoc-gen-go.`,
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestProtocGenGoStamp pins protocGenGoStamp, for the same reason.
func TestProtocGenGoStamp(t *testing.T) {
	type tcase struct {
		line string
		want string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			var got string
			if m := protocGenGoStamp.FindStringSubmatch(tc.line); m != nil {
				got = m[1]
			}

			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		}
	}

	tests := map[string]tcase{
		"the stamp, as protoc-gen-go writes it": {
			line: "// \tprotoc-gen-go v1.36.12",
			want: "v1.36.12",
		},
		"spaces rather than a tab": {
			line: "//    protoc-gen-go v1.36.12",
			want: "v1.36.12",
		},
		"the protoc stamp beside it is a different tool": {
			line: "// \tprotoc        v3.21.12",
			want: "",
		},
		"a version with no v is not one": {
			line: "// \tprotoc-gen-go 1.36.12",
			want: "",
		},
		"prose in a doc comment is not a stamp": {
			line: "// Regenerating needs a protoc-gen-go v1.36.12 or the file will not build.",
			want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}
