package execute

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/cutover"
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

func TestCutoverExecute_NoAuthFlag_ReturnsError(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	cmd.SetArgs([]string{
		"--cutover-id", "test-cutover",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags")
}

func TestCutoverExecute_WithAuthFlag_PassesValidation(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	cmd.SetArgs([]string{
		"--cutover-id", "test-cutover",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	})

	err := cmd.Execute()
	// Should fail later (missing state file), NOT on auth validation.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "at least one of the flags")
	assert.Contains(t, err.Error(), "cutover state file")
}

func TestCutoverExecute_WithSaslPlainFlag_RequiresCredentials(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	cmd.SetArgs([]string{
		"--cutover-id", "test-cutover",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-plain",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sasl-plain-username")
}

func TestCutoverExecute_WithSaslPlainFlagAndCredentials_PassesValidation(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	cmd.SetArgs([]string{
		"--cutover-id", "test-cutover",
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
	assert.Contains(t, err.Error(), "cutover state file")
}

func TestCutoverExecute_MultipleAuthFlags_ReturnsError(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	cmd.SetArgs([]string{
		"--cutover-id", "test-cutover",
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
// --sasl-scram-mechanism flag tests
// ===========================================================================

func TestCutoverExecute_SaslScramMechanism_DefaultIsSHA512(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--cutover-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
	}))

	opts := parseCutoverExecutorOpts(cutover.CutoverState{}, cutover.CutoverConfig{})
	assert.Equal(t, "SHA512", opts.SaslScramMechanism, "default --sasl-scram-mechanism should be SHA512 for MSK compatibility")
}

func TestCutoverExecute_SaslScramMechanism_ExplicitSHA256(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--cutover-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
		"--sasl-scram-mechanism", "SHA256",
	}))

	opts := parseCutoverExecutorOpts(cutover.CutoverState{}, cutover.CutoverConfig{})
	assert.Equal(t, "SHA256", opts.SaslScramMechanism)
}

func TestCutoverExecute_SaslScramMechanism_BindFromEnvVar(t *testing.T) {
	resetAuthFlags()
	t.Setenv("SASL_SCRAM_MECHANISM", "SHA256")

	cmd := NewCutoverExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--cutover-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
	}))
	require.NoError(t, utils.BindEnvToFlags(cmd))

	opts := parseCutoverExecutorOpts(cutover.CutoverState{}, cutover.CutoverConfig{})
	assert.Equal(t, "SHA256", opts.SaslScramMechanism, "SASL_SCRAM_MECHANISM env var should override the default")
}

func TestCutoverExecute_SaslScramMechanism_InvalidValueRejected(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	cmd.SetArgs([]string{
		"--cutover-id", "test-cutover",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-sasl-scram",
		"--sasl-scram-username", "user",
		"--sasl-scram-password", "pass",
		"--sasl-scram-mechanism", "MD5",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --sasl-scram-mechanism")
}

// ===========================================================================
// SASL/SCRAM mechanism end-to-end test
// ===========================================================================

func TestCutoverExecute_SaslScramMechanism_ReachesKafkaClient(t *testing.T) {
	// Verify the mechanism value propagates from opts through createSourceOffset
	// into the Kafka client SASL configuration. We capture slog output to confirm
	// configureSASLTypeSCRAMAuthentication receives the correct mechanism.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	original := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(original)

	executor := NewCutoverExecutor(CutoverExecutorOpts{
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

func TestCutoverExecute_RolloutTimeout_DefaultIsZero(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--cutover-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	}))

	opts := parseCutoverExecutorOpts(cutover.CutoverState{}, cutover.CutoverConfig{})
	assert.Equal(t, time.Duration(0), opts.RolloutTimeout, "default --rollout-timeout should be 0 (no deadline)")
}

func TestCutoverExecute_RolloutTimeout_ExplicitValueParsed(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--cutover-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--rollout-timeout", "10m",
	}))

	opts := parseCutoverExecutorOpts(cutover.CutoverState{}, cutover.CutoverConfig{})
	assert.Equal(t, 10*time.Minute, opts.RolloutTimeout)
}

func TestCutoverExecute_RolloutTimeout_InvalidDurationFails(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverExecuteCmd()
	err := cmd.ParseFlags([]string{
		"--cutover-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
		"--rollout-timeout", "not-a-duration",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rollout-timeout")
}

func TestCutoverExecute_RolloutTimeout_BindFromEnvVar(t *testing.T) {
	resetAuthFlags()
	t.Setenv("ROLLOUT_TIMEOUT", "7m")

	cmd := NewCutoverExecuteCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--cutover-id", "test",
		"--lag-threshold", "1",
		"--cluster-api-key", "key",
		"--cluster-api-secret", "secret",
		"--use-unauthenticated-plaintext",
	}))
	require.NoError(t, utils.BindEnvToFlags(cmd))

	opts := parseCutoverExecutorOpts(cutover.CutoverState{}, cutover.CutoverConfig{})
	assert.Equal(t, 7*time.Minute, opts.RolloutTimeout, "ROLLOUT_TIMEOUT env var should populate the flag")
}
