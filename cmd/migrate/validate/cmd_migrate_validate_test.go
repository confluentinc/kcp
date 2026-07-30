package validate

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func runCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewMigrateValidateCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestValidateCmd_Valid(t *testing.T) {
	stdout, _, err := runCmd(t, "-f", "testdata/valid.yaml")
	require.NoError(t, err)
	require.Contains(t, stdout, "is valid")
}

func TestValidateCmd_Invalid(t *testing.T) {
	_, stderr, err := runCmd(t, "-f", "testdata/invalid.yaml")
	require.Error(t, err)
	require.Contains(t, stderr, "spec.source.type")
	require.Contains(t, stderr, "metadata.name")
}

func TestValidateCmd_MissingFile(t *testing.T) {
	_, _, err := runCmd(t, "-f", "testdata/does-not-exist.yaml")
	require.Error(t, err)
}
