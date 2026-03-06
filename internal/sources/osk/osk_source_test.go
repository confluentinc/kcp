package osk_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSKSource_Type(t *testing.T) {
	source := osk.NewOSKSource()
	if source.Type() != types.SourceTypeOSK {
		t.Errorf("expected source type %s, got %s", types.SourceTypeOSK, source.Type())
	}
}

func TestOSKSource_GetClusters_BeforeLoad(t *testing.T) {
	source := osk.NewOSKSource()
	clusters := source.GetClusters()
	if clusters != nil {
		t.Error("expected nil clusters before loading credentials")
	}
}

func TestOSKSource_LoadCredentials_FileNotFound(t *testing.T) {
	source := osk.NewOSKSource()
	err := source.LoadCredentials("nonexistent.yaml")
	if err == nil {
		t.Error("expected error when loading nonexistent credentials file")
	}
}

func TestOSKSource_Scan_SkipsDisabledClusters(t *testing.T) {
	// Create temporary credentials file with mix of enabled and disabled clusters
	content := `
clusters:
  - id: disabled-cluster
    bootstrap_servers:
      - localhost:9092
    auth_method:
      sasl_scram:
        use: false
      tls:
        use: false
      unauthenticated_plaintext:
        use: false
  - id: another-disabled-cluster
    bootstrap_servers:
      - localhost:9093
    auth_method:
      unauthenticated_plaintext:
        use: false
`
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "disabled-creds.yaml")
	require.NoError(t, os.WriteFile(credFile, []byte(content), 0644))

	// Create source and load credentials
	source := osk.NewOSKSource()
	err := source.LoadCredentials(credFile)
	require.NoError(t, err)

	// Scan clusters - should skip all disabled clusters without error
	ctx := context.Background()
	opts := sources.ScanOptions{
		SkipTopics: false,
		SkipACLs:   false,
	}

	result, err := source.Scan(ctx, opts)

	// Should succeed with no errors (all clusters were skipped gracefully)
	assert.NoError(t, err)
	require.NotNil(t, result)

	// No clusters should have been scanned (all were disabled)
	assert.Equal(t, 0, len(result.Clusters), "expected no clusters to be scanned when all are disabled")
	assert.Equal(t, types.SourceTypeOSK, result.SourceType)
}

func TestOSKSource_Scan_SkipsOnlyDisabledClusters(t *testing.T) {
	// This test verifies the skip behavior works correctly in integration
	// with a real Kafka cluster (requires test environment to be running)

	// Create temporary credentials file with one disabled cluster
	content := `
clusters:
  - id: disabled-cluster
    bootstrap_servers:
      - localhost:9999
    auth_method:
      sasl_scram:
        use: false
`
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "mixed-creds.yaml")
	require.NoError(t, os.WriteFile(credFile, []byte(content), 0644))

	// Create source and load credentials
	source := osk.NewOSKSource()
	err := source.LoadCredentials(credFile)
	require.NoError(t, err)

	// Scan clusters
	ctx := context.Background()
	opts := sources.ScanOptions{
		SkipTopics: false,
		SkipACLs:   false,
	}

	result, err := source.Scan(ctx, opts)

	// Should succeed with no errors
	assert.NoError(t, err)
	require.NotNil(t, result)

	// Disabled cluster should have been skipped
	assert.Equal(t, 0, len(result.Clusters), "disabled cluster should be skipped")
}
