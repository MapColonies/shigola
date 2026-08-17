//go:build !noViewer
// +build !noViewer

package build

import (
	"github.com/MapColonies/shigola/ui"
)

func ViewerVersion() string {
	version := ui.Version()
	if version == "" {
		return uiVersionDefaultText
	}

	return version
}
