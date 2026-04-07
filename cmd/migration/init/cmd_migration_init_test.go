package init

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetAuthFlags() {
	useSaslIam = false
	useSaslScram = false
	useSaslPlain = false
	useTls = false
	useUnauthenticatedTLS = false
	useUnauthenticatedPlaintext = false
}

func TestMigrationInit_NoAuthFlag_ReturnsError(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationInitCmd()
	cmd.SetArgs([]string{
		"--source-bootstrap", "broker:9092",
		"--cluster-bootstrap", "pkc-abc.confluent.cloud:9092",
		"--k8s-namespace", "test-ns",
		"--initial-cr-name", "test-cr",
		"--cluster-id", "lkc-123",
		"--cluster-rest-endpoint", "https://pkc-abc.confluent.cloud:443",
		"--cluster-link-name", "test-link",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--fenced-cr-yaml", "fenced.yaml",
		"--switchover-cr-yaml", "switchover.yaml",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags")
}

func TestMigrationInit_WithAuthFlag_PassesValidation(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationInitCmd()
	cmd.SetArgs([]string{
		"--source-bootstrap", "broker:9092",
		"--cluster-bootstrap", "pkc-abc.confluent.cloud:9092",
		"--k8s-namespace", "test-ns",
		"--initial-cr-name", "test-cr",
		"--cluster-id", "lkc-123",
		"--cluster-rest-endpoint", "https://pkc-abc.confluent.cloud:443",
		"--cluster-link-name", "test-link",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--fenced-cr-yaml", "fenced.yaml",
		"--switchover-cr-yaml", "switchover.yaml",
		"--use-unauthenticated-plaintext",
	})

	err := cmd.Execute()
	// Should fail later (missing YAML files), NOT on auth validation.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "at least one of the flags")
}
