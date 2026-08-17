package main

import (
	"fmt"
	"os"

	_ "github.com/theckman/goconstraint/go1.8/gte"

	"github.com/MapColonies/shigola/cmd/shigola/cmd"
)

func main() {
	if err := cmd.RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
