package connect_topics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMinimalStateFile writes a valid empty kcp state JSON so the state-file
// validation in preRunScanConnectTopics loads cleanly. The validator only
// checks load-ability; no fields are read.
func writeMinimalStateFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "kcp-state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0644))
	return path
}

// writeMinimalOSKCredentials writes an OSK credentials file with one cluster.
// Just enough YAML for LoadCredentials to succeed in tests that get that far.
func writeMinimalOSKCredentials(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "osk-credentials.yaml")
	content := `
clusters:
  - id: test-cluster
    bootstrap_servers:
      - localhost:9092
    auth_method:
      unauthenticated_plaintext:
        use: true
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// resetGlobals zeros the package-level flag variables between subtests so
// values set by one test do not bleed into the next.
func resetGlobals() {
	stateFile = ""
	credentialsFile = ""
	clusterID = ""
	topics = nil
}

func TestCommand_RequiredFlags(t *testing.T) {
	// Cobra enforces MarkFlagRequired by checking whether the flag was set on
	// the command line. To exercise each branch we pass real, valid values for
	// every other required flag (so preRunE doesn't error first) and omit the
	// one under test.
	tmpDir := t.TempDir()
	stateFilePath := writeMinimalStateFile(t, tmpDir)
	credsPath := writeMinimalOSKCredentials(t, tmpDir)

	tests := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		{
			name:       "missing --credentials-file",
			args:       []string{"--state-file", stateFilePath, "--cluster-id", "test-cluster", "--topics", "connect-status"},
			wantSubstr: "credentials-file",
		},
		{
			name:       "missing --state-file",
			args:       []string{"--credentials-file", credsPath, "--cluster-id", "test-cluster", "--topics", "connect-status"},
			wantSubstr: "state-file",
		},
		{
			name:       "missing --cluster-id",
			args:       []string{"--credentials-file", credsPath, "--state-file", stateFilePath, "--topics", "connect-status"},
			wantSubstr: "cluster-id",
		},
		{
			name:       "missing --topics",
			args:       []string{"--credentials-file", credsPath, "--state-file", stateFilePath, "--cluster-id", "test-cluster"},
			wantSubstr: "topics",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobals()
			cmd := NewScanConnectTopicsCmd()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantSubstr)
		})
	}
}

func TestCommand_NonexistentStateFile(t *testing.T) {
	resetGlobals()
	tmpDir := t.TempDir()
	credsPath := writeMinimalOSKCredentials(t, tmpDir)

	cmd := NewScanConnectTopicsCmd()
	cmd.SetArgs([]string{
		"--credentials-file", credsPath,
		"--state-file", filepath.Join(tmpDir, "missing.json"),
		"--cluster-id", "test-cluster",
		"--topics", "connect-status",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state file does not exist")
}

func TestCommand_CorruptStateFile(t *testing.T) {
	resetGlobals()
	tmpDir := t.TempDir()
	credsPath := writeMinimalOSKCredentials(t, tmpDir)
	corrupt := filepath.Join(tmpDir, "kcp-state.json")
	require.NoError(t, os.WriteFile(corrupt, []byte("not json"), 0644))

	cmd := NewScanConnectTopicsCmd()
	cmd.SetArgs([]string{
		"--credentials-file", credsPath,
		"--state-file", corrupt,
		"--cluster-id", "test-cluster",
		"--topics", "connect-status",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load state file")
}

func TestCommand_TopicsParsing(t *testing.T) {
	tests := []struct {
		name       string
		topicsFlag []string // each element is one --topics value
		want       []string
	}{
		{
			name:       "comma-separated single flag",
			topicsFlag: []string{"--topics", "t1,t2,t3"},
			want:       []string{"t1", "t2", "t3"},
		},
		{
			name:       "repeated flag",
			topicsFlag: []string{"--topics", "t1", "--topics", "t2", "--topics", "t3"},
			want:       []string{"t1", "t2", "t3"},
		},
		{
			name:       "comma-separated with whitespace stripped",
			topicsFlag: []string{"--topics", "t1, t2 ,t3"},
			want:       []string{"t1", "t2", "t3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobals()
			tmpDir := t.TempDir()
			stateFilePath := writeMinimalStateFile(t, tmpDir)
			credsPath := writeMinimalOSKCredentials(t, tmpDir)

			cmd := NewScanConnectTopicsCmd()
			// Stub out RunE so we don't actually try to scan Kafka.
			cmd.RunE = func(_ *cobra.Command, _ []string) error { return nil }
			args := append([]string{
				"--credentials-file", credsPath,
				"--state-file", stateFilePath,
				"--cluster-id", "test-cluster",
			}, tt.topicsFlag...)
			cmd.SetArgs(args)
			require.NoError(t, cmd.Execute())

			assert.Equal(t, tt.want, topics)
		})
	}
}
