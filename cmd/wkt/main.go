package main

import (
	"os"

	"github.com/Venut-Labs/wkt/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
// A build without it says "dev", which is the truth for a working copy.
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
