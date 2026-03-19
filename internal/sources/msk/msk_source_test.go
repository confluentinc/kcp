package msk_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/msk"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMSKSource_Type(t *testing.T) {
	source := msk.NewMSKSource()
	if source.Type() != types.SourceTypeMSK {
		t.Errorf("expected source type %s, got %s", types.SourceTypeMSK, source.Type())
	}
}

func TestMSKSource_GetClusters_BeforeLoad(t *testing.T) {
	source := msk.NewMSKSource()
	clusters := source.GetClusters()
	if clusters != nil {
		t.Error("expected nil clusters before loading credentials")
	}
}

func TestMSKSource_Scan_ErrorWhenCredentialsNotLoaded(t *testing.T) {
	source := msk.NewMSKSource()
	state := &types.State{
		MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{}},
	}

	_, err := source.Scan(context.Background(), sources.ScanOptions{State: state})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials not loaded")
}

func TestMSKSource_Scan_ErrorWhenStateNotProvided(t *testing.T) {
	source := msk.NewMSKSource()

	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "msk-credentials.yaml")
	require.NoError(t, os.WriteFile(credFile, []byte("regions: []\n"), 0644))
	require.NoError(t, source.LoadCredentials(credFile))

	_, err := source.Scan(context.Background(), sources.ScanOptions{State: nil})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "state is required")
}

func TestMSKSource_Scan_EmptyResultWhenNoRegions(t *testing.T) {
	source := msk.NewMSKSource()

	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "msk-credentials.yaml")
	require.NoError(t, os.WriteFile(credFile, []byte("regions: []\n"), 0644))
	require.NoError(t, source.LoadCredentials(credFile))

	state := &types.State{
		MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{}},
	}

	result, err := source.Scan(context.Background(), sources.ScanOptions{State: state})

	require.NoError(t, err)
	assert.Equal(t, types.SourceTypeMSK, result.SourceType)
	assert.Empty(t, result.Clusters)
}
