package main

import (
	"fmt"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/cli"
)

// Default values for build information during local development. Real values injected at release time by GoReleaser.
var (
	version = "local"
	commit  = ""
	branch  = ""
	date    = string(time.Now().Format(time.RFC3339))
)

func main() {
	cli.SetBuildInfo(version, commit, branch, date)

	if err := cli.RootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
