package migration_infra

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	serverlessClusterArn  = "arn:aws:kafka:us-east-1:123456789012:cluster/sl-cluster/000-1"
	provisionedClusterArn = "arn:aws:kafka:us-east-1:123456789012:cluster/prov-cluster/111-2"
)

// resetMigrationInfraFlags zeroes every package-level flag variable
// parseMSKMigrationInfraOpts reads. The flags are cobra-bound package
// globals (cmd_create_asset_migration_infra.go), so tests calling the
// parser directly must reset them between cases to avoid bleed-through.
func resetMigrationInfraFlags(t *testing.T) {
	t.Helper()

	stateFile = ""
	ccType = ""
	migrationInfraType = ""
	clusterLinkName = "test-link"

	sourceType = "msk"
	clusterId = ""
	oskVpcId = ""
	oskRegion = ""
	sourceSaslScramMechanism = ""

	existingInternetGateway = false
	existingPrivateLinkVpceId = "vpce-test"
	outputDir = ""

	targetEnvironmentId = "env-test"
	targetClusterId = "lkc-test"
	targetRestEndpoint = "https://lkc-test.us-east-1.aws.confluent.cloud:443"
	targetBootstrapEndpoint = "lkc-test.us-east-1.aws.confluent.cloud:9092"

	extOutboundSubnetId = ""
	extOutboundSecurityGroupId = ""

	jumpClusterInstanceType = ""
	jumpClusterBrokerStorage = 0
	jumpClusterBrokerSubnetCidr = nil
	jumpClusterSetupHostSubnetCidr = net.IPNet{}

	jumpClusterIamAuthRoleName = "kcp-iam-migration"
	targetClusterType = ""
}

// mustParseCIDRs parses a list of CIDR strings into []net.IPNet, matching
// the shape pflag.IPNetSliceVar produces from --jump-cluster-broker-subnet-cidr.
func mustParseCIDRs(t *testing.T, cidrs ...string) []net.IPNet {
	t.Helper()

	result := make([]net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, ipNet, err := net.ParseCIDR(c)
		require.NoError(t, err, "invalid test CIDR %q", c)
		result = append(result, *ipNet)
	}
	return result
}

// buildServerlessTestCluster returns an MSK Serverless DiscoveredCluster
// fixture: no Provisioned config, cluster_networking populated the way the
// discover fix populates it (VpcId/SubnetIds/SecurityGroups from
// Serverless.VpcConfigs), and only a SASL/IAM bootstrap string - matching
// the live demo-cluster used to validate this feature.
func buildServerlessTestCluster() types.DiscoveredCluster {
	return types.DiscoveredCluster{
		Name:   "sl-cluster",
		Arn:    serverlessClusterArn,
		Region: "us-east-1",
		AWSClientInformation: types.AWSClientInformation{
			MskClusterConfig: kafkatypes.Cluster{
				ClusterName: aws.String("sl-cluster"),
				ClusterArn:  aws.String(serverlessClusterArn),
				ClusterType: kafkatypes.ClusterTypeServerless,
				Serverless: &kafkatypes.Serverless{
					VpcConfigs: []kafkatypes.VpcConfig{
						{
							SubnetIds:        []string{"subnet-sl-1", "subnet-sl-2"},
							SecurityGroupIds: []string{"sg-sl-1"},
						},
					},
				},
			},
			ClusterNetworking: types.ClusterNetworking{
				VpcId:          "vpc-serverless-1",
				SubnetIds:      []string{"subnet-sl-1", "subnet-sl-2"},
				SecurityGroups: []string{"sg-sl-1"},
				Subnets: []types.SubnetInfo{
					{SubnetId: "subnet-sl-1", AvailabilityZone: "us-east-1a", CidrBlock: "10.0.1.0/24"},
					{SubnetId: "subnet-sl-2", AvailabilityZone: "us-east-1b", CidrBlock: "10.0.2.0/24"},
				},
			},
			BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("boot-sl.c1.kafka-serverless.us-east-1.amazonaws.com:9098"),
			},
		},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			ClusterID: "slcluster1",
		},
	}
}

// buildProvisionedTestCluster returns a Provisioned MSK DiscoveredCluster
// fixture with 3 broker nodes, for the existing (unchanged) CIDR-count and
// defaulting behaviour.
func buildProvisionedTestCluster() types.DiscoveredCluster {
	return types.DiscoveredCluster{
		Name:   "prov-cluster",
		Arn:    provisionedClusterArn,
		Region: "us-east-1",
		ClusterMetrics: types.ClusterMetrics{
			MetricMetadata: types.MetricMetadata{
				NumberOfBrokerNodes: 3,
			},
		},
		AWSClientInformation: types.AWSClientInformation{
			MskClusterConfig: kafkatypes.Cluster{
				ClusterName: aws.String("prov-cluster"),
				ClusterArn:  aws.String(provisionedClusterArn),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
						InstanceType:   aws.String("kafka.m5.large"),
						ClientSubnets:  []string{"subnet-p-1", "subnet-p-2", "subnet-p-3"},
						SecurityGroups: []string{"sg-p-1"},
						StorageInfo: &kafkatypes.StorageInfo{
							EbsStorageInfo: &kafkatypes.EBSStorageInfo{
								VolumeSize: aws.Int32(100),
							},
						},
					},
				},
			},
			ClusterNetworking: types.ClusterNetworking{
				VpcId:     "vpc-provisioned-1",
				SubnetIds: []string{"subnet-p-1", "subnet-p-2", "subnet-p-3"},
			},
			BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("boot-prov.kafka.us-east-1.amazonaws.com:9098"),
			},
		},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			ClusterID: "provcluster1",
		},
	}
}

// writeTestStateFile marshals a state file containing both fixture clusters
// to a temp file and returns its path.
func writeTestStateFile(t *testing.T) string {
	t.Helper()

	state := types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						buildServerlessTestCluster(),
						buildProvisionedTestCluster(),
					},
				},
			},
		},
	}

	data, err := json.Marshal(state)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "kcp-state.json")
	require.NoError(t, os.WriteFile(path, data, 0644))
	return path
}

// TestParseMSKMigrationInfraOpts_ServerlessTypeGate covers change 2: MSK
// Serverless clusters only support --type 5, because Serverless offers
// SASL/IAM authentication only.
func TestParseMSKMigrationInfraOpts_ServerlessTypeGate(t *testing.T) {
	statePath := writeTestStateFile(t)

	for _, typ := range []string{"1", "2", "3", "4"} {
		t.Run("type "+typ, func(t *testing.T) {
			resetMigrationInfraFlags(t)
			stateFile = statePath
			clusterId = serverlessClusterArn
			migrationInfraType = typ

			_, err := parseMSKMigrationInfraOpts()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "only --type 5")
		})
	}
}

// TestParseMSKMigrationInfraOpts_ServerlessSizingRequired covers change 3:
// --jump-cluster-instance-type / --jump-cluster-broker-storage cannot be
// defaulted for Serverless (no BrokerNodeGroupInfo to default from), so they
// must error cleanly rather than nil-deref.
func TestParseMSKMigrationInfraOpts_ServerlessSizingRequired(t *testing.T) {
	statePath := writeTestStateFile(t)

	t.Run("missing instance type", func(t *testing.T) {
		resetMigrationInfraFlags(t)
		stateFile = statePath
		clusterId = serverlessClusterArn
		migrationInfraType = "5"
		jumpClusterBrokerStorage = 100
		jumpClusterBrokerSubnetCidr = mustParseCIDRs(t, "10.0.54.0/24", "10.0.55.0/24", "10.0.56.0/24")

		_, err := parseMSKMigrationInfraOpts()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "--jump-cluster-instance-type is required")
	})

	t.Run("missing broker storage", func(t *testing.T) {
		resetMigrationInfraFlags(t)
		stateFile = statePath
		clusterId = serverlessClusterArn
		migrationInfraType = "5"
		jumpClusterInstanceType = "m5.large"
		jumpClusterBrokerSubnetCidr = mustParseCIDRs(t, "10.0.54.0/24", "10.0.55.0/24", "10.0.56.0/24")

		_, err := parseMSKMigrationInfraOpts()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "--jump-cluster-broker-storage is required")
	})
}

// TestParseMSKMigrationInfraOpts_ServerlessSuccess proves --type 5 succeeds
// end-to-end for a Serverless cluster once both sizing flags are supplied,
// with a broker subnet CIDR count that has no relationship to a (nonexistent)
// Serverless broker count.
func TestParseMSKMigrationInfraOpts_ServerlessSuccess(t *testing.T) {
	resetMigrationInfraFlags(t)
	stateFile = writeTestStateFile(t)
	clusterId = serverlessClusterArn
	migrationInfraType = "5"
	jumpClusterInstanceType = "m5.large"
	jumpClusterBrokerStorage = 100
	jumpClusterBrokerSubnetCidr = mustParseCIDRs(t, "10.0.54.0/24", "10.0.55.0/24", "10.0.56.0/24")
	jumpClusterSetupHostSubnetCidr = mustParseCIDRs(t, "10.0.53.0/28")[0]

	opts, err := parseMSKMigrationInfraOpts()
	require.NoError(t, err)

	req := opts.MigrationWizardRequest
	assert.Equal(t, "vpc-serverless-1", req.VpcId)
	assert.Equal(t, "boot-sl.c1.kafka-serverless.us-east-1.amazonaws.com:9098", req.SourceSaslIamBootstrapServers)
	assert.Equal(t, "iam", req.JumpClusterAuthType)
	assert.Equal(t, "m5.large", req.JumpClusterInstanceType)
	assert.Equal(t, 100, req.JumpClusterBrokerStorage)
	assert.Len(t, req.JumpClusterBrokerSubnetCidr, 3)
	assert.Equal(t, types.JumpClusterIam, opts.MigrationType)
}

// TestParseMSKMigrationInfraOpts_ProvisionedCidrCountUnchanged proves the
// existing Provisioned behaviour (CIDR count must equal broker count) is
// untouched by the Serverless branching.
func TestParseMSKMigrationInfraOpts_ProvisionedCidrCountUnchanged(t *testing.T) {
	resetMigrationInfraFlags(t)
	stateFile = writeTestStateFile(t)
	clusterId = provisionedClusterArn
	migrationInfraType = "5"
	jumpClusterBrokerSubnetCidr = mustParseCIDRs(t, "10.0.54.0/24") // 1 CIDR, but the fixture has 3 broker nodes
	jumpClusterSetupHostSubnetCidr = mustParseCIDRs(t, "10.0.53.0/28")[0]

	_, err := parseMSKMigrationInfraOpts()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match the number of broker nodes")
}
