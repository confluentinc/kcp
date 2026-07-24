package utils

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestGetClusterDisplayName_MSK(t *testing.T) {
	displayName := GetClusterDisplayName(types.SourceTypeMSK, "arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123", "")
	assert.Equal(t, "my-cluster", displayName)
}

func TestGetClusterDisplayName_OSK(t *testing.T) {
	displayName := GetClusterDisplayName(types.SourceTypeOSK, "", "production-kafka")
	assert.Equal(t, "production-kafka", displayName)
}

func TestGetClusterDisplayName_MSK_MalformedArn(t *testing.T) {
	displayName := GetClusterDisplayName(types.SourceTypeMSK, "not-an-arn", "")
	assert.Equal(t, "unknown-cluster", displayName)
}

func TestGetClusterDisplayName_InvalidSourceType(t *testing.T) {
	displayName := GetClusterDisplayName("invalid", "arn", "id")
	assert.Equal(t, "unknown-cluster", displayName)
}

func TestInferSourceTypeFromClusterID(t *testing.T) {
	mskArn := "arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123"
	oskID := "production-kafka"

	stateWithBoth := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{Name: "us-east-1", Clusters: []types.DiscoveredCluster{{Arn: mskArn}}},
			},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{{ID: oskID}},
		},
	}

	tests := []struct {
		name      string
		state     *types.State
		clusterID string
		want      types.SourceType
		wantErr   bool
	}{
		{name: "MSK ARN match", state: stateWithBoth, clusterID: mskArn, want: types.SourceTypeMSK},
		{name: "OSK ID match", state: stateWithBoth, clusterID: oskID, want: types.SourceTypeOSK},
		{name: "unknown cluster", state: stateWithBoth, clusterID: "missing", wantErr: true},
		{name: "empty state", state: &types.State{}, clusterID: oskID, wantErr: true},
		{name: "nil state", state: nil, clusterID: oskID, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InferSourceTypeFromClusterID(tt.state, tt.clusterID)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.clusterID)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
