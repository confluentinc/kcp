package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandErrorNotDuplicated(t *testing.T) {
	// Save originals and restore after test
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	oldArgs := os.Args
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		os.Args = oldArgs
	}()

	// Run in a temp dir to avoid kcp.log in the repo root
	origDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(t.TempDir()))

	// Create pipes to capture stdout and stderr
	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)
	rErr, wErr, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = wOut
	os.Stderr = wErr

	// Trigger a validation error via an invalid --source-type value.
	// We use SetArgs so Cobra parses these instead of os.Args.
	cmd.RootCmd.SetArgs([]string{
		"scan", "clusters",
		"--source-type", "invalid",
		"--credentials-file", "dummy.yaml",
	})

	runErr := run()

	// Close writers so readers can reach EOF, then collect output
	require.NoError(t, wOut.Close())
	require.NoError(t, wErr.Close())

	var stdoutBuf, stderrBuf bytes.Buffer
	_, _ = io.Copy(&stdoutBuf, rOut)
	_, _ = io.Copy(&stderrBuf, rErr)

	combined := stdoutBuf.String() + stderrBuf.String()

	// The command must have failed
	require.Error(t, runErr)

	// Use the returned error's text to count occurrences — this avoids
	// hardcoding any specific message and stays resilient to wording changes.
	errText := runErr.Error()
	count := strings.Count(combined, errText)
	assert.Equalf(t, 1, count,
		"error message should appear exactly once in combined output, but appeared %d times.\nError text: %q\nCombined output:\n%s",
		count, errText, combined)
}
