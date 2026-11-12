//go:build integration

package mskemulator

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := applyEmulatorEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var err error
	if fx, err = seedFixture(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "seeding the AWS Emulator:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
