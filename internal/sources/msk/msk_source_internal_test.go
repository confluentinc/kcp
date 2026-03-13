package msk

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestMSKSource_findClusterInState(t *testing.T) {
	tests := []struct {
		name        string
		source      *MSKSource
		state       *types.State
		region      string
		clusterArn  string
		wantCluster *types.DiscoveredCluster
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name:   "found cluster in region",
			source: &MSKSource{},
			state: &types.State{
				MSKSources: &types.MSKSourcesState{
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
			name:   "no regions match",
			source: &MSKSource{},
			state: &types.State{
				MSKSources: &types.MSKSourcesState{
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
			name:   "no clusters match",
			source: &MSKSource{},
			state: &types.State{
				MSKSources: &types.MSKSourcesState{
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
			gotCluster, err := tt.source.findClusterInState(tt.state, tt.region, tt.clusterArn)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
				assert.Nil(t, gotCluster)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, gotCluster)
				assert.Equal(t, tt.wantCluster.Name, gotCluster.Name)
				assert.Equal(t, tt.wantCluster.Arn, gotCluster.Arn)

				// Verify the returned pointer points into the actual state (important for future mutation)
				found := false
				for i, region := range tt.state.MSKSources.Regions {
					if region.Name == tt.region {
						for j, cluster := range region.Clusters {
							if cluster.Arn == tt.clusterArn {
								assert.Same(t, &tt.state.MSKSources.Regions[i].Clusters[j], gotCluster)
								found = true
								break
							}
						}
						break
					}
				}
				assert.True(t, found, "expected cluster pointer to reference element in state")
			}
		})
	}
}

func TestMSKSource_scanCluster(t *testing.T) {
	tests := []struct {
		name        string
		source      *MSKSource
		state       *types.State
		region      string
		clusterAuth types.ClusterAuth
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name:   "getClusterFromDiscovery returns error",
			source: &MSKSource{},
			state: &types.State{
				MSKSources: &types.MSKSourcesState{
					Regions: []types.DiscoveredRegion{
						{
							Name:     "us-east-1",
							Clusters: []types.DiscoveredCluster{},
						},
					},
				},
			},
			region: "us-east-1",
			clusterAuth: types.ClusterAuth{
				Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/nonexistent/abc-123",
			},
			wantErr:    true,
			wantErrMsg: "failed to get cluster from discovery state: cluster arn:aws:kafka:us-east-1:123456789012:cluster/nonexistent/abc-123 not found in region us-east-1",
		},
		{
			name:   "GetSelectedAuthType returns error",
			source: &MSKSource{},
			state: &types.State{
				MSKSources: &types.MSKSourcesState{
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
			region: "us-east-1",
			clusterAuth: types.ClusterAuth{
				Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
				// No auth method configured
			},
			wantErr:    true,
			wantErrMsg: "failed to determine auth type for cluster: arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123 in region: us-east-1: no authentication method enabled for cluster",
		},
		{
			name:   "GetBootstrapBrokersForAuthType returns error",
			source: &MSKSource{},
			state: &types.State{
				MSKSources: &types.MSKSourcesState{
					Regions: []types.DiscoveredRegion{
						{
							Name: "us-east-1",
							Clusters: []types.DiscoveredCluster{
								{
									Name: "test-cluster",
									Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
									AWSClientInformation: types.AWSClientInformation{
										BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
											// Empty — will cause GetBootstrapBrokersForAuthType to fail
										},
									},
								},
							},
						},
					},
				},
			},
			region: "us-east-1",
			clusterAuth: types.ClusterAuth{
				Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
				AuthMethod: types.AuthMethodConfig{
					IAM: &types.IAMConfig{Use: true},
				},
			},
			wantErr:    true,
			wantErrMsg: "failed to get broker addresses for cluster: arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123 in region: us-east-1: No SASL/IAM brokers found in the cluster",
		},
		{
			name:   "createKafkaAdmin returns error",
			source: &MSKSource{},
			state: &types.State{
				MSKSources: &types.MSKSourcesState{
					Regions: []types.DiscoveredRegion{
						{
							Name: "us-east-1",
							Clusters: []types.DiscoveredCluster{
								{
									Name: "test-cluster",
									Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
									AWSClientInformation: types.AWSClientInformation{
										BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
											BootstrapBrokerStringSaslScram: aws.String("broker1:9092,broker2:9092"),
										},
										MskClusterConfig: kafkatypes.Cluster{
											ClusterType: kafkatypes.ClusterTypeProvisioned,
											Provisioned: &kafkatypes.Provisioned{
												CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
													KafkaVersion: aws.String("2.8.1"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			region: "us-east-1",
			clusterAuth: types.ClusterAuth{
				Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{
						Use:      true,
						Username: "", // Empty username causes NewKafkaAdmin to fail
						Password: "",
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "failed to create Kafka admin: failed to create Kafka admin: Failed to create admin client: authType=SASL/SCRAM brokerAddresses=[broker1:9092 broker2:9092] error=kafka: invalid configuration (Net.SASL.User must not be empty when SASL is enabled)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := sources.ScanOptions{State: tt.state}
			_, err := tt.source.scanCluster(tt.region, tt.clusterAuth, opts)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
