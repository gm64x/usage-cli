package main

import (
	"os"

	"github.com/gm64x/usage-cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
