package main

import (
	"fmt"
	"os"

	"github.com/confluentinc/kcp-internal/internal/cli"
)

// Default values for build information during local development. Real values injected at release time by GoReleaser.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	cli.SetBuildInfo(version, commit, date)

	if err := cli.RootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
