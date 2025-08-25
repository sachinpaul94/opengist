package main

import (
	"github.com/sachinpaul94/opengist/internal/cli"
	"os"
)

func main() {
	if err := cli.App(); err != nil {
		os.Exit(1)
	}
}
