package targetinfra

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// --help prints the command's examples: the custom usage func must not drop them.
func TestTargetInfraHelp_ShowsExample(t *testing.T) {
	cmd := NewTargetInfraCmd()

	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	require.NoError(t, cmd.UsageFunc()(cmd))
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)

	require.Contains(t, string(out), "Examples:")
	require.Contains(t, string(out), "kcp create-asset target-infra \\")
}
