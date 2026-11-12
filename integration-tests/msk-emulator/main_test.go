//go:build integration

package mskemulator

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := applyEmulatorEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
