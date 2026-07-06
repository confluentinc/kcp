package execute

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/migration"
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
	detectUnroutedProducersDuration = 0
}

func TestMigrationExecute_NoAuthFlag_ReturnsError(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--detect-unrouted-producers-duration", "0",
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
		"--detect-unrouted-producers-duration", "0",
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
		"--detect-unrouted-producers-duration", "0",
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
		"--detect-unrouted-producers-duration", "0",
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
		"--detect-unrouted-producers-duration", "0",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group")
}

// ===========================================================================
// --sasl-scram-mechanism flag tests
// ===========================================================================

func TestMigrationExecute_SaslScramMechanism_DefaultIsSHA512(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
	}))

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
	assert.Equal(t, "SHA512", opts.SaslScramMechanism, "default --sasl-scram-mechanism should be SHA512 for MSK compatibility")
}

func TestMigrationExecute_SaslScramMechanism_ExplicitSHA256(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
		"--sasl-scram-mechanism", "SHA256",
	}))

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
	assert.Equal(t, "SHA256", opts.SaslScramMechanism)
}

func TestMigrationExecute_SaslScramMechanism_BindFromEnvVar(t *testing.T) {
	resetAuthFlags()
	t.Setenv("SASL_SCRAM_MECHANISM", "SHA256")

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
	}))
	require.NoError(t, utils.BindEnvToFlags(cmd))

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
	assert.Equal(t, "SHA256", opts.SaslScramMechanism, "SASL_SCRAM_MECHANISM env var should override the default")
}

func TestMigrationExecute_SaslScramMechanism_InvalidValueRejected(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
		"--sasl-scram-mechanism", "MD5",
		"--detect-unrouted-producers-duration", "0",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --sasl-scram-mechanism")
}

// ===========================================================================
// SASL/SCRAM mechanism end-to-end test
// ===========================================================================

func TestMigrationExecute_SaslScramMechanism_ReachesKafkaClient(t *testing.T) {
	// Verify the mechanism value propagates from opts through createSourceOffset
	// into the Kafka client SASL configuration. We capture slog output to confirm
	// configureSASLTypeSCRAMAuthentication receives the correct mechanism.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	original := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(original)

	executor := NewMigrationExecutor(MigrationExecutorOpts{
		SourceBootstrap:    "localhost:19999", // bogus port, will fail to connect
		AuthType:           types.AuthTypeSASLSCRAM,
		SaslScramUsername:  "user",
		SaslScramPassword:  "pass",
		SaslScramMechanism: "SHA512",
	})

	_, err := executor.createSourceOffset(context.Background())
	require.Error(t, err, "should fail to connect to bogus broker")

	logOutput := buf.String()
	assert.Contains(t, logOutput, "mechanism=SHA512",
		"SASL/SCRAM configuration should log the mechanism that was passed through opts")
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

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
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

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
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

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
	assert.Equal(t, 7*time.Minute, opts.RolloutTimeout, "ROLLOUT_TIMEOUT env var should populate the flag")
}

// ===========================================================================
// --detect-unrouted-producers-duration flag tests
// ===========================================================================

func TestMigrationExecute_DetectUnroutedProducersDuration_ZeroSkipsCheck(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--detect-unrouted-producers-duration", "0",
	})

	err := cmd.Execute()
	// Should fail on missing state file, not on duration validation
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration state file")
}

func TestMigrationExecute_DetectUnroutedProducersDuration_ValidDuration(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--detect-unrouted-producers-duration", "10s",
	})

	err := cmd.Execute()
	// Should fail on missing state file, not on duration validation
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration state file")
}

func TestMigrationExecute_DetectUnroutedProducersDuration_BelowMinimumRejected(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--detect-unrouted-producers-duration", "5s",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be at least 10s")
}

func TestMigrationExecute_DetectUnroutedProducersDuration_Required(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	cmd.SetArgs([]string{
		"--migration-id", "test-migration",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		// --detect-unrouted-producers-duration intentionally omitted
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "detect-unrouted-producers-duration")
}

// ===========================================================================
// --promote-batch-size flag tests
// ===========================================================================

func TestMigrationExecute_PromoteBatchSize_DefaultIsZero(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	}))

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
	assert.Equal(t, 0, opts.PromoteBatchSize, "default --promote-batch-size should be 0 (promote all at once)")
}

func TestMigrationExecute_PromoteBatchSize_ExplicitValueParsed(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--promote-batch-size", "10",
	}))

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
	assert.Equal(t, 10, opts.PromoteBatchSize)
}

func TestMigrationExecute_PromoteBatchSize_InvalidValueFails(t *testing.T) {
	resetAuthFlags()

	cmd := NewMigrationExecuteCmd()
	err := cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--promote-batch-size", "not-an-int",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "promote-batch-size")
}

func TestMigrationExecute_PromoteBatchSize_BindFromEnvVar(t *testing.T) {
	resetAuthFlags()
	t.Setenv("PROMOTE_BATCH_SIZE", "25")

	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--migration-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	}))
	require.NoError(t, utils.BindEnvToFlags(cmd))

	opts := parseMigrationExecutorOpts(migration.MigrationState{}, migration.MigrationConfig{})
	assert.Equal(t, 25, opts.PromoteBatchSize, "PROMOTE_BATCH_SIZE env var should populate the flag")
}
