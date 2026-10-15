package main

import (
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/cmd"
	"github.com/confluentinc/kcp/internal/tracing"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() (err error) {
	// Shutdown runs here rather than in a post-run hook, which cobra skips when
	// a command fails — and a failed run's trace is the one most worth having.
	defer func() { tracing.Shutdown(err) }()
	if err = cmd.RootCmd.Execute(); err != nil {
		slog.Error(err.Error())
		return err
	}
	return nil
}
