package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/cmd"
)

func main() {
	if err := cmd.RootCmd.Execute(); err != nil {
		slog.Error(err.Error())
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
