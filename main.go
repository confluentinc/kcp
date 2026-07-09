package main

import (
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/cmd"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Drain the terminal->kcp.log mirror on every exit path (success, error,
	// panic-unwind) before main calls os.Exit. The mirror is filled by pump
	// goroutines, so this must run to guarantee the last lines reach the log.
	defer cmd.FlushCapture()

	if err := cmd.RootCmd.Execute(); err != nil {
		slog.Error(err.Error())
		return err
	}
	return nil
}
