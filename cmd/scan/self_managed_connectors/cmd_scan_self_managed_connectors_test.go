package self_managed_connectors

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// runMetricsPreRun parses args onto a fresh command (which also resets the
// flag-bound package vars to their defaults) and runs only the PreRunE
// validation hook — never RunE — so we can assert flag validation in isolation.
func runMetricsPreRun(t *testing.T, args []string) error {
	t.Helper()
	cmd := NewScanSelfManagedConnectorsCmd()
	require.NoError(t, cmd.ParseFlags(args), "flags should parse")
	return preRunScanSelfManagedConnectors(cmd, nil)
}

// --- happy-path guards: valid combinations must NOT be rejected ---

func TestPreRun_Metrics_JolokiaValid(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "jolokia", "--metrics-duration", "5m", "--credentials-file", "creds.yaml",
	})
	require.NoError(t, err)
}

func TestPreRun_Metrics_PrometheusValid(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "prometheus", "--metrics-range", "7d", "--credentials-file", "creds.yaml",
	})
	require.NoError(t, err)
}

func TestPreRun_NoMetrics_Unchanged(t *testing.T) {
	// R1: omitting --metrics leaves validation unchanged (no creds file required).
	err := runMetricsPreRun(t, []string{"--use-unauthenticated"})
	require.NoError(t, err)
}

// --- abuse / error paths (R4): bad combinations must fail fast ---

func TestPreRun_Jolokia_RequiresDuration(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "jolokia", "--credentials-file", "creds.yaml",
	})
	require.ErrorContains(t, err, "--metrics-duration is required")
}

func TestPreRun_Jolokia_DurationMustExceedInterval(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "jolokia", "--metrics-duration", "5s", "--metrics-interval", "10s",
		"--credentials-file", "creds.yaml",
	})
	require.ErrorContains(t, err, "must be greater than")
}

func TestPreRun_Prometheus_RequiresRange(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "prometheus", "--credentials-file", "creds.yaml",
	})
	require.ErrorContains(t, err, "--metrics-range is required")
}

func TestPreRun_Jolokia_RejectsRange(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "jolokia", "--metrics-duration", "5m", "--metrics-range", "7d",
		"--credentials-file", "creds.yaml",
	})
	require.ErrorContains(t, err, "--metrics-range cannot be used with --metrics jolokia")
}

func TestPreRun_Prometheus_RejectsDuration(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "prometheus", "--metrics-range", "7d", "--metrics-duration", "5m",
		"--credentials-file", "creds.yaml",
	})
	require.ErrorContains(t, err, "--metrics-duration cannot be used with --metrics prometheus")
}

func TestPreRun_Prometheus_RejectsInterval(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "prometheus", "--metrics-range", "7d", "--metrics-interval", "30s",
		"--credentials-file", "creds.yaml",
	})
	require.ErrorContains(t, err, "--metrics-interval cannot be used with --metrics prometheus")
}

func TestPreRun_InvalidMetricsValue(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "bogus", "--credentials-file", "creds.yaml",
	})
	require.ErrorContains(t, err, "must be 'jolokia' or 'prometheus'")
}

func TestPreRun_Metrics_RequiresCredentialsFile(t *testing.T) {
	err := runMetricsPreRun(t, []string{
		"--metrics", "jolokia", "--metrics-duration", "5m",
	})
	require.ErrorContains(t, err, "--credentials-file is required")
}

// --- opts plumbing: credential resolution into scanner opts ---

const jolokiaCredsSection = `    jolokia:
      endpoints:
        - http://broker:8778/jolokia
`

const prometheusCredsSection = `    prometheus:
      url: http://prom:9090
`

// writeOSKCredsFile writes a minimal valid apache-kafka-credentials.yaml for one
// cluster, appending an optional metrics section (jolokia/prometheus).
func writeOSKCredsFile(t *testing.T, id, extra string) string {
	t.Helper()
	body := fmt.Sprintf(`clusters:
  - id: %s
    bootstrap_servers:
      - broker:9092
    auth_method:
      unauthenticated_plaintext:
        use: true
%s`, id, extra)
	path := filepath.Join(t.TempDir(), "apache-kafka-credentials.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func writeStateFileWithCluster(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kcp-state.json")
	require.NoError(t, stateWithCluster().PersistStateFile(path))
	return path
}

// resetCmdVars rebuilds the command, which re-binds and resets every
// flag-bound package var to its registered default. The TestParseOpts_* tests
// mutate these shared package vars directly, so they must run sequentially — do
// not add t.Parallel() to any test in this file.
func resetCmdVars() { NewScanSelfManagedConnectorsCmd() }

func TestParseOpts_NoMetrics_NilCreds(t *testing.T) {
	resetCmdVars()
	stateFile = writeStateFileWithCluster(t)
	connectRestURL = "http://localhost:8083"
	clusterID = testArn
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	require.NoError(t, err)
	require.Empty(t, opts.MetricsSource, "no --metrics ⇒ empty source")
	require.Nil(t, opts.MetricsClusterCreds, "no --metrics ⇒ no creds resolved")
}

func TestParseOpts_Jolokia_ResolvesClusterCreds(t *testing.T) {
	resetCmdVars()
	stateFile = writeStateFileWithCluster(t)
	connectRestURL = "http://localhost:8083"
	clusterID = testArn
	useUnauthenticated = true
	metricsSource = "jolokia"
	metricsDuration = "5m"
	credentialsFile = writeOSKCredsFile(t, testArn, jolokiaCredsSection)

	opts, err := parseScanSelfManagedConnectorsOpts()
	require.NoError(t, err)
	require.Equal(t, "jolokia", opts.MetricsSource)
	require.Equal(t, "5m", opts.MetricsDuration)
	require.Equal(t, "10s", opts.MetricsInterval, "interval defaults to 10s")
	require.NotNil(t, opts.MetricsClusterCreds)
	require.Equal(t, testArn, opts.MetricsClusterCreds.ID)
	require.True(t, opts.MetricsClusterCreds.HasJolokiaConfig())
}

func TestParseOpts_Prometheus_ResolvesClusterCreds(t *testing.T) {
	resetCmdVars()
	stateFile = writeStateFileWithCluster(t)
	connectRestURL = "http://localhost:8083"
	clusterID = testArn
	useUnauthenticated = true
	metricsSource = "prometheus"
	metricsRange = "7d"
	credentialsFile = writeOSKCredsFile(t, testArn, prometheusCredsSection)

	opts, err := parseScanSelfManagedConnectorsOpts()
	require.NoError(t, err)
	require.Equal(t, "prometheus", opts.MetricsSource)
	require.Equal(t, "7d", opts.MetricsRange)
	require.NotNil(t, opts.MetricsClusterCreds)
	require.True(t, opts.MetricsClusterCreds.HasPrometheusConfig())
}

// Abuse (R11): a creds file with no entry matching --cluster-arn must error
// clearly, and the error must not leak any credential value.
func TestParseOpts_NoMatchingCluster_ErrorsWithoutSecret(t *testing.T) {
	resetCmdVars()
	stateFile = writeStateFileWithCluster(t)
	connectRestURL = "http://localhost:8083"
	clusterID = testArn
	useUnauthenticated = true
	metricsSource = "jolokia"
	metricsDuration = "5m"

	const secret = "topsecret-jolokia-pw"
	credsSection := fmt.Sprintf(`    jolokia:
      endpoints:
        - http://broker:8778/jolokia
      auth:
        username: monitor
        password: %s
`, secret)
	credentialsFile = writeOSKCredsFile(t, "a-different-cluster-id", credsSection)

	_, err := parseScanSelfManagedConnectorsOpts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no matching cluster entry")
	require.NotContains(t, err.Error(), secret, "credential value must not leak into error (R11)")
}
