package build

//go:generate go run tags/tags.go -v -runCommand="internal/build/tags.go" -source=../..

import (
	"sort"
	"strings"
)

var (
	Version     = "version not set"
	GitRevision = "not set"
	GitBranch   = "not set"
	Tags        []string
	Commands    = []string{"shigola"}
)

var ordered bool

func OrderedTags() []string {
	if !ordered {
		sort.Strings(Tags)
		ordered = true
	}
	return Tags
}

func Command() string { return strings.Join(Commands, " ") }
