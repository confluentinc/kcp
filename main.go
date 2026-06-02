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
	if err := cmd.RootCmd.Execute(); err != nil {
		slog.Error(err.Error())
		return err
	}
	return nil
}
