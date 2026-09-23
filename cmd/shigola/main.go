package main

import (
	"log/slog"
	"os"

	_ "github.com/theckman/goconstraint/go1.8/gte"

	"github.com/MapColonies/shigola/cmd/shigola/cmd"
	"github.com/MapColonies/shigola/internal/build"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/logexport"
)

func main() {
	// Installed before anything can fail, so that an error returned before
	// initConfig installs the logger at the requested --log-level — a bad flag,
	// say — is a record in the same format as every other. initConfig replaces
	// it.
	slog.SetDefault(log.New(os.Stderr, slog.LevelInfo, build.Version, build.GitRevision))

	err := cmd.RootCmd.Execute()
	if err != nil {
		log.Error(err)
	}

	// Here rather than in each command, because every command returns
	// through here — serve after its own shutdown has run — and this is the
	// last point anything logs. After the error above, so a failed command's
	// reason is exported too.
	logexport.Flush(logexport.Installed())

	if err != nil {
		os.Exit(1)
	}
}
