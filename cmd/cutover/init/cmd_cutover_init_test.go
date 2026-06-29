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

func TestCutoverInit_NoAuthFlag_ReturnsError(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverInitCmd()
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

func TestCutoverInit_WithSaslPlainFlag_RequiresCredentials(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverInitCmd()
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
		"--use-sasl-plain",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sasl-plain-username")
}

func TestCutoverInit_WithSaslPlainFlagAndCredentials_PassesValidation(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverInitCmd()
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
		"--use-sasl-plain",
		"--sasl-plain-username", "user",
		"--sasl-plain-password", "pass",
	})

	err := cmd.Execute()
	// Should fail later (missing YAML files), NOT on auth validation.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "at least one of the flags")
}

func TestCutoverInit_WithAuthFlag_PassesValidation(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverInitCmd()
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

func TestCutoverInit_PauseOffsetSyncFlag_Registered(t *testing.T) {
	resetAuthFlags()

	cmd := NewCutoverInitCmd()
	flag := cmd.Flags().Lookup("pause-consumer-offset-sync")
	require.NotNil(t, flag, "--pause-consumer-offset-sync flag must be registered")
	assert.Equal(t, "false", flag.DefValue, "default must be false (opt-in)")
}

// TestCutoverInit_PauseOffsetSync_SkipValidate_MutuallyExclusive verifies that
// --pause-consumer-offset-sync and --skip-validate cannot be combined. The
// restore bookend needs the init-time snapshot captured by the validation path;
// without it, restore has nothing to diff against and would silently leave the
// cluster link disabled after switchover.
func TestCutoverInit_PauseOffsetSync_SkipValidate_MutuallyExclusive(t *testing.T) {
	resetAuthFlags()
	pauseConsumerOffsetSync = false
	skipValidate = false

	cmd := NewCutoverInitCmd()
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
		"--skip-validate",
		"--pause-consumer-offset-sync",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skip-validate")
	assert.Contains(t, err.Error(), "pause-consumer-offset-sync")
}
