// Command impulse is the command-line tool for Impulse applications: apps built on the
// Impulse Framework, with its resource package at the center.
package main

import (
	"os"

	"github.com/impulseframework/impulse/cmd/impulse/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
