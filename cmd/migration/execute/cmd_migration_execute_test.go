package execute

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

func TestMigrationExecute_NoAuthFlag_ReturnsError(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags")
}

func TestMigrationExecute_WithAuthFlag_PassesValidation(t *testing.T) {
	resetAuthFlags()

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
	assert.NotContains(t, err.Error(), "at least one of the flags")
	assert.Contains(t, err.Error(), "migration state file")
}

func TestMigrationExecute_WithSaslPlainFlag_RequiresCredentials(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-plain",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sasl-plain-username")
}

func TestMigrationExecute_WithSaslPlainFlagAndCredentials_PassesValidation(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-plain",
		"--sasl-plain-username", "user",
		"--sasl-plain-password", "pass",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "at least one of the flags")
	assert.Contains(t, err.Error(), "migration state file")
}

func TestMigrationExecute_MultipleAuthFlags_ReturnsError(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-tls",
		"--use-unauthenticated-plaintext",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group")
}
