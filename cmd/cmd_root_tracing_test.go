package cmd

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestRootHook_StartsARootSpanForTheCommand(t *testing.T) {
	t.Chdir(t.TempDir()) // the hook writes kcp.log and probes write access in the current directory
	path := filepath.Join(t.TempDir(), "t.jsonl")
	t.Setenv(tracing.EnvEnabled, "true")
	t.Setenv(tracing.EnvTraceFile, path)

	var sawSpan bool
	probe := &cobra.Command{
		Use: "trace-probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sawSpan = trace.SpanFromContext(cmd.Context()).SpanContext().IsValid()
			return nil
		},
	}
	RootCmd.AddCommand(probe)
	t.Cleanup(func() {
		RootCmd.RemoveCommand(probe)
		RootCmd.SetArgs(nil)
	})

	RootCmd.SetArgs([]string{"trace-probe"})
	err := RootCmd.Execute()
	tracing.Shutdown(err)
	require.NoError(t, err)

	assert.True(t, sawSpan, "the command's context carries the root span")
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	require.True(t, sc.Scan(), "the root span is flushed to the file")
	assert.Contains(t, sc.Text(), `"name":"kcp trace-probe"`)
}
