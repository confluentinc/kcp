package execute

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationExecute_NoAuthFlag_ReturnsError(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one source cluster authentication flag is required")
}

func TestMigrationExecute_WithAuthFlag_PassesValidation(t *testing.T) {
	// Reset package-level vars to avoid cross-test pollution.
	useSaslIam = false
	useSaslScram = false
	useTls = false
	useUnauthenticatedTLS = false
	useUnauthenticatedPlaintext = false

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	})

	err := cmd.Execute()
	// Should fail later (missing state file), NOT on auth validation.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "at least one source cluster authentication flag is required")
	assert.Contains(t, err.Error(), "migration state file")
}
