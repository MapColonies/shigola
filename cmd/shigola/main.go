package main

import (
	"log/slog"
	"os"

	_ "github.com/theckman/goconstraint/go1.8/gte"

	"github.com/MapColonies/shigola/cmd/shigola/cmd"
	"github.com/MapColonies/shigola/internal/build"
	"github.com/MapColonies/shigola/internal/log"
)

func main() {
	// Installed before anything can fail, so that an error returned before
	// initConfig installs the logger at the requested --log-level — a bad flag,
	// say — is a record in the same format as every other. initConfig replaces
	// it.
	slog.SetDefault(log.New(os.Stderr, slog.LevelInfo, build.Version, build.GitRevision))

	if err := cmd.RootCmd.Execute(); err != nil {
		log.Error(err)
		os.Exit(1)
	}
}
