package clusters

import (
	"errors"
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

// Mock implementation of ClustersScannerKafkaService
type mockKafkaService struct {
	scanKafkaResourcesFunc func(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error)
}

func (m *mockKafkaService) ScanKafkaResources(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error) {
	return m.scanKafkaResourcesFunc(clusterType)
}

func TestClustersScanner_getClusterFromDiscovery(t *testing.T) {
	tests := []struct {
		name        string
		scanner     *ClustersScanner
		region      string
		clusterArn  string
		wantCluster *types.DiscoveredCluster
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name: "found cluster in region",
			scanner: &ClustersScanner{
				Discovery: &types.Discovery{
					Regions: []types.DiscoveredRegion{
						{
							Name: "us-east-1",
							Clusters: []types.DiscoveredCluster{
								{
									Name: "test-cluster",
									Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
								},
							},
						},
					},
				},
			},
			region:     "us-east-1",
			clusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
			wantCluster: &types.DiscoveredCluster{
				Name: "test-cluster",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
			},
			wantErr: false,
		},
		{
			name: "no regions match",
			scanner: &ClustersScanner{
				Discovery: &types.Discovery{
					Regions: []types.DiscoveredRegion{
						{
							Name: "us-east-1",
							Clusters: []types.DiscoveredCluster{
								{
									Name: "test-cluster",
									Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
								},
							},
						},
					},
				},
			},
			region:      "us-west-2",
			clusterArn:  "arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster/abc-123",
			wantCluster: nil,
			wantErr:     true,
			wantErrMsg:  "cluster arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster/abc-123 not found in region us-west-2",
		},
		{
			name: "no clusters match",
			scanner: &ClustersScanner{
				Discovery: &types.Discovery{
					Regions: []types.DiscoveredRegion{
						{
							Name: "us-east-1",
							Clusters: []types.DiscoveredCluster{
								{
									Name: "test-cluster",
									Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
								},
							},
						},
					},
				},
			},
			region:      "us-east-1",
			clusterArn:  "arn:aws:kafka:us-east-1:123456789012:cluster/different-cluster/xyz-999",
			wantCluster: nil,
			wantErr:     true,
			wantErrMsg:  "cluster arn:aws:kafka:us-east-1:123456789012:cluster/different-cluster/xyz-999 not found in region us-east-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCluster, err := tt.scanner.getClusterFromDiscovery(tt.region, tt.clusterArn)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
				assert.Nil(t, gotCluster)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, gotCluster)
				assert.Equal(t, tt.wantCluster.Name, gotCluster.Name)
				assert.Equal(t, tt.wantCluster.Arn, gotCluster.Arn)

				// Verify that the returned pointer points to the actual cluster in discovery
				// This is important for mutation operations
				found := false
				for i, region := range tt.scanner.Discovery.Regions {
					if region.Name == tt.region {
						for j, cluster := range region.Clusters {
							if cluster.Arn == tt.clusterArn {
								assert.Same(t, &tt.scanner.Discovery.Regions[i].Clusters[j], gotCluster)
								found = true
								break
							}
						}
						break
					}
				}
				assert.True(t, found, "Expected cluster should have been found in the discovery state")
			}
		})
	}
}

func TestClustersScanner_scanKafkaResources(t *testing.T) {
	tests := []struct {
		name              string
		discoveredCluster *types.DiscoveredCluster
		kafkaService      ClustersScannerKafkaService
		wantErr           bool
		wantErrMsg        string
	}{
		{
			name: "successful scan",
			discoveredCluster: &types.DiscoveredCluster{
				Name: "test-cluster",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
				AWSClientInformation: types.AWSClientInformation{
					MskClusterConfig: kafkatypes.Cluster{
						ClusterType: kafkatypes.ClusterTypeProvisioned,
					},
				},
			},
			kafkaService: &mockKafkaService{
				scanKafkaResourcesFunc: func(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error) {
					return &types.KafkaAdminClientInformation{
						ClusterID: "test-cluster-id",
						Topics: &types.Topics{
							Summary: types.TopicSummary{Topics: 5},
						},
						Acls: []types.Acls{
							{Principal: "User:test", Operation: "READ"},
						},
					}, nil
				},
			},
			wantErr: false,
		},
		{
			name: "kafka service returns error",
			discoveredCluster: &types.DiscoveredCluster{
				Name: "test-cluster",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
				AWSClientInformation: types.AWSClientInformation{
					MskClusterConfig: kafkatypes.Cluster{
						ClusterType: kafkatypes.ClusterTypeServerless,
					},
				},
			},
			kafkaService: &mockKafkaService{
				scanKafkaResourcesFunc: func(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error) {
					return nil, errors.New("connection timeout")
				},
			},
			wantErr:    true,
			wantErrMsg: "❌ failed to scan Kafka resources: connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &ClustersScanner{}

			// Store original KafkaAdminClientInformation to verify mutation
			originalInfo := tt.discoveredCluster.KafkaAdminClientInformation

			err := cs.scanKafkaResources(tt.discoveredCluster, tt.kafkaService)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
				// Verify that the cluster information wasn't modified on error
				assert.Equal(t, originalInfo, tt.discoveredCluster.KafkaAdminClientInformation)
			} else {
				assert.NoError(t, err)
				// Verify that the cluster was updated with the returned information
				assert.Equal(t, "test-cluster-id", tt.discoveredCluster.KafkaAdminClientInformation.ClusterID)
				assert.Equal(t, 5, tt.discoveredCluster.KafkaAdminClientInformation.Topics.Summary.Topics)
				assert.Len(t, tt.discoveredCluster.KafkaAdminClientInformation.Acls, 1)
				assert.Equal(t, "User:test", tt.discoveredCluster.KafkaAdminClientInformation.Acls[0].Principal)
			}
		})
	}
}

func TestClustersScanner_scanCluster(t *testing.T) {
	tests := []struct {
		name         string
		scanner      *ClustersScanner
		region       string
		clusterEntry types.ClusterEntry
		wantErr      bool
		wantErrMsg   string
	}{
		{
			name: "getClusterFromDiscovery returns error",
			scanner: &ClustersScanner{
				Discovery: &types.Discovery{
					Regions: []types.DiscoveredRegion{
						{
							Name:     "us-east-1",
							Clusters: []types.DiscoveredCluster{},
						},
					},
				},
			},
			region: "us-east-1",
			clusterEntry: types.ClusterEntry{
				Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/nonexistent/abc-123",
			},
			wantErr:    true,
			wantErrMsg: "❌ failed to get cluster from discovery state: cluster arn:aws:kafka:us-east-1:123456789012:cluster/nonexistent/abc-123 not found in region us-east-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scanner.scanCluster(tt.region, tt.clusterEntry)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
