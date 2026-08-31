package main

import (
	"os"

	"loadout.dev/loadout/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Stdout, os.Stderr, os.Args[1:]))
}
