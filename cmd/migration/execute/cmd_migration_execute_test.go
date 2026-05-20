package execute

import (
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
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
	rolloutTimeout = 0
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

// ===========================================================================
// --rollout-timeout flag tests
// ===========================================================================

func TestMigrationExecute_RolloutTimeout_DefaultIsZero(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	}))

	opts := parseMigrationExecutorOpts(types.MigrationState{}, types.MigrationConfig{})
	assert.Equal(t, time.Duration(0), opts.RolloutTimeout, "default --rollout-timeout should be 0 (no deadline)")
}

func TestMigrationExecute_RolloutTimeout_ExplicitValueParsed(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--rollout-timeout", "10m",
	}))

	opts := parseMigrationExecutorOpts(types.MigrationState{}, types.MigrationConfig{})
	assert.Equal(t, 10*time.Minute, opts.RolloutTimeout)
}

func TestMigrationExecute_RolloutTimeout_InvalidDurationFails(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	err := cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--rollout-timeout", "not-a-duration",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rollout-timeout")
}

func TestMigrationExecute_RolloutTimeout_BindFromEnvVar(t *testing.T) {
	resetAuthFlags()
	t.Setenv("ROLLOUT_TIMEOUT", "7m")

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	}))
	require.NoError(t, utils.BindEnvToFlags(cmd))

	opts := parseMigrationExecutorOpts(types.MigrationState{}, types.MigrationConfig{})
	assert.Equal(t, 7*time.Minute, opts.RolloutTimeout, "ROLLOUT_TIMEOUT env var should populate the flag")
}
