package clusters

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// withSourceTypeGlobals sets the package-level flag globals used by
// preRunScanClusters and restores them after the test, so cases don't leak.
func withSourceTypeGlobals(t *testing.T, source, creds, metrics string) {
	t.Helper()
	prevSource, prevCreds, prevMetrics := sourceType, credentialsFile, metricsSource
	t.Cleanup(func() {
		sourceType, credentialsFile, metricsSource = prevSource, prevCreds, prevMetrics
	})
	sourceType, credentialsFile, metricsSource = source, creds, metrics
}

// TestPreRunScanClusters_AcceptsApacheKafka asserts the post-rename flag value
// is accepted by validation.
func TestPreRunScanClusters_AcceptsApacheKafka(t *testing.T) {
	withSourceTypeGlobals(t, "apache-kafka", "apache-kafka-credentials.yaml", "")

	err := preRunScanClusters(&cobra.Command{}, nil)

	require.NoError(t, err, "--source-type apache-kafka should be accepted")
}

// TestPreRunScanClusters_RejectsRetiredOSK asserts the retired flag value is
// cleanly rejected (hard break — no fallthrough), with a descriptive error.
func TestPreRunScanClusters_RejectsRetiredOSK(t *testing.T) {
	withSourceTypeGlobals(t, "osk", "apache-kafka-credentials.yaml", "")

	err := preRunScanClusters(&cobra.Command{}, nil)

	require.Error(t, err, "retired --source-type osk must be rejected")
	require.Contains(t, err.Error(), "apache-kafka", "error should name the valid value")
}

// TestPreRunScanClusters_RejectsGarbage guards that unknown values stay rejected.
func TestPreRunScanClusters_RejectsGarbage(t *testing.T) {
	withSourceTypeGlobals(t, "definitely-not-a-source", "creds.yaml", "")

	err := preRunScanClusters(&cobra.Command{}, nil)

	require.Error(t, err, "unknown --source-type must be rejected")
}
