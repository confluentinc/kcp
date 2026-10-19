// Command lambda-scanner is a one-shot AWS Lambda (custom runtime, provided.al2023) that runs
// kcp scan steps from inside the cluster's VPC, so private MSK clusters can be scanned, and returns
// the resulting kcp-state.json in the invoke response.
//
// Design: the handler exec's the bundled kcp CLI (found in $LAMBDA_TASK_ROOT) for each step with
// only the credentials from the event payload in its environment; the function role's own
// credentials are dropped. This keeps the Lambda a thin wrapper over the exact code path users
// run locally. A future version may call the kcp packages directly instead of exec'ing the CLI.
//
// This is not a Cobra subcommand and must not be imported by the main kcp binary.
package main

import (
	"log/slog"
	"os"
	"path/filepath"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := &handler{
		kcpPath: filepath.Join(os.Getenv("LAMBDA_TASK_ROOT"), "kcp"),
		tmpRoot: "/tmp",
		logger:  logger,
	}
	if err := runtimeLoop(os.Getenv("AWS_LAMBDA_RUNTIME_API"), h); err != nil {
		logger.Error("runtime loop failed", "error", err)
		os.Exit(1)
	}
}
