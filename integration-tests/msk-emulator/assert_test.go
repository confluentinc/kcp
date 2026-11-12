//go:build integration

package mskemulator

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	kctypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saslEnabled(ca *kafkatypes.ClientAuthentication) (iam, scram bool) {
	if ca == nil || ca.Sasl == nil {
		return false, false
	}
	if ca.Sasl.Iam != nil {
		iam = aws.ToBool(ca.Sasl.Iam.Enabled)
	}
	if ca.Sasl.Scram != nil {
		scram = aws.ToBool(ca.Sasl.Scram.Enabled)
	}
	return iam, scram
}

func assertNetworking(t *testing.T, got types.ClusterNetworking, want seededNetwork) {
	t.Helper()
	assert.Equal(t, want.VpcID, got.VpcId)
	assert.Equal(t, want.subnetIDs(), got.SubnetIds)
	assert.Equal(t, []string{want.SecurityGroupID}, got.SecurityGroups)
	for _, s := range got.Subnets {
		var seeded *seededSubnet
		for i := range want.Subnets {
			if want.Subnets[i].ID == s.SubnetId {
				seeded = &want.Subnets[i]
			}
		}
		if assert.NotNil(t, seeded, "subnet %s was not seeded", s.SubnetId) {
			assert.Equal(t, seeded.AZ, s.AvailabilityZone, "subnet %s AZ", s.SubnetId)
			assert.Equal(t, seeded.CIDR, s.CidrBlock, "subnet %s CIDR", s.SubnetId)
		}
	}
}

func brokerIDs(subnets []types.SubnetInfo) []int {
	ids := make([]int, 0, len(subnets))
	for _, s := range subnets {
		ids = append(ids, s.SubnetMskBrokerId)
	}
	return ids
}

func assertProvisioned(t *testing.T, got types.DiscoveredCluster, want seededCluster) {
	t.Helper()
	assert.Equal(t, want.Name, got.Name)
	assert.Equal(t, want.Region, got.Region)

	info := got.AWSClientInformation
	mc := info.MskClusterConfig
	assert.Equal(t, kafkatypes.ClusterTypeProvisioned, mc.ClusterType)
	require.NotNil(t, mc.Provisioned, "provisioned block")
	p := mc.Provisioned
	require.NotNil(t, p.CurrentBrokerSoftwareInfo)
	assert.Equal(t, want.KafkaVersion, aws.ToString(p.CurrentBrokerSoftwareInfo.KafkaVersion))
	assert.Equal(t, want.Brokers, aws.ToInt32(p.NumberOfBrokerNodes))
	require.NotNil(t, p.BrokerNodeGroupInfo)
	assert.Equal(t, want.InstanceType, aws.ToString(p.BrokerNodeGroupInfo.InstanceType))
	require.NotNil(t, p.BrokerNodeGroupInfo.StorageInfo)
	require.NotNil(t, p.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo)
	assert.Equal(t, want.VolumeGB, aws.ToInt32(p.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize))

	iam, scram := saslEnabled(p.ClientAuthentication)
	assert.Equal(t, want.IAM, iam, "IAM enabled")
	assert.Equal(t, want.SCRAM, scram, "SCRAM enabled")

	assertNetworking(t, info.ClusterNetworking, want.Network)
	assert.Len(t, info.ClusterNetworking.Subnets, int(want.Brokers), "one subnet entry per broker")
	wantIDs := make([]int, want.Brokers)
	for i := range wantIDs {
		wantIDs[i] = i + 1
	}
	assert.ElementsMatch(t, wantIDs, brokerIDs(info.ClusterNetworking.Subnets))
	assert.Len(t, info.Nodes, int(want.Brokers))

	bs := info.BootstrapBrokers
	assert.Equal(t, want.IAM, aws.ToString(bs.BootstrapBrokerStringSaslIam) != "", "IAM bootstrap string present")
	assert.Equal(t, want.SCRAM, aws.ToString(bs.BootstrapBrokerStringSaslScram) != "", "SCRAM bootstrap string present")
	if want.TLSInTransit {
		assert.NotEmpty(t, aws.ToString(bs.BootstrapBrokerStringTls), "TLS bootstrap string")
	}

	if want.ScramSecretArn != "" {
		assert.Equal(t, []string{want.ScramSecretArn}, info.ScramSecrets)
	} else {
		assert.Empty(t, info.ScramSecrets)
	}

	assert.NotEmpty(t, info.CompatibleVersions.CompatibleKafkaVersions)
	assert.NotNil(t, info.ClusterOperations, "cluster_operations must be [] not null")
	assert.Empty(t, info.ClusterOperations)
	assert.NotEmpty(t, got.ClusterMetrics.Results, "metrics results")
}

func assertServerless(t *testing.T, got types.DiscoveredCluster, want seededCluster) {
	t.Helper()
	assert.Equal(t, want.Name, got.Name)
	assert.Equal(t, want.Region, got.Region)

	info := got.AWSClientInformation
	assert.Equal(t, kafkatypes.ClusterTypeServerless, info.MskClusterConfig.ClusterType)
	assert.NotNil(t, info.Nodes, "nodes must be [] not null")
	assert.Empty(t, info.Nodes)
	assert.NotNil(t, info.ScramSecrets, "ScramSecrets must be [] not null")
	assert.Empty(t, info.ScramSecrets)
	assert.NotNil(t, info.CompatibleVersions.CompatibleKafkaVersions, "compatible versions must be [] not null")
	assert.Empty(t, info.CompatibleVersions.CompatibleKafkaVersions)

	assertNetworking(t, info.ClusterNetworking, want.Network)
	assert.Len(t, info.ClusterNetworking.Subnets, len(want.Network.Subnets), "one subnet entry per seeded subnet")
	for _, s := range info.ClusterNetworking.Subnets {
		assert.Equal(t, 0, s.SubnetMskBrokerId, "serverless subnet %s broker ID", s.SubnetId)
	}

	bs := info.BootstrapBrokers
	assert.NotEmpty(t, aws.ToString(bs.BootstrapBrokerStringSaslIam), "IAM bootstrap string")
	assert.Empty(t, aws.ToString(bs.BootstrapBrokerStringSaslScram), "SCRAM bootstrap string")
	assert.Empty(t, aws.ToString(bs.BootstrapBrokerStringTls), "TLS bootstrap string")
}

func assertCredAuth(t *testing.T, got types.ClusterAuth, want seededCluster) {
	t.Helper()
	assert.Equal(t, want.Arn, got.Arn)
	am := got.AuthMethod
	assert.Equal(t, want.IAM, am.IAM != nil, "iam entry present")
	assert.Equal(t, want.SCRAM, am.SASLScram != nil, "sasl_scram entry present")
	switch {
	case want.IAM && want.SCRAM && am.IAM != nil && am.SASLScram != nil:
		assert.True(t, am.IAM.Use, "IAM is the selected method")
		assert.False(t, am.SASLScram.Use, "SCRAM is not selected when IAM is available")
	case want.IAM && am.IAM != nil:
		assert.True(t, am.IAM.Use)
	case want.SCRAM && am.SASLScram != nil:
		assert.True(t, am.SASLScram.Use)
	}
	assert.Nil(t, am.TLS)
	assert.Nil(t, am.SASLPlain)
	assert.Nil(t, am.UnauthenticatedTLS)
	assert.Nil(t, am.UnauthenticatedPlaintext)
}

func assertConnector(t *testing.T, got types.ConnectorSummary, want seededConnector) {
	t.Helper()
	assert.Equal(t, want.Name, got.ConnectorName)
	assert.Equal(t, want.Arn, got.ConnectorArn)
	assert.Equal(t, "RUNNING", got.ConnectorState)
	assert.Equal(t, kctypes.KafkaClusterClientAuthenticationTypeIam, got.KafkaClusterClientAuthentication.AuthenticationType)
	if assert.NotNil(t, got.Capacity.ProvisionedCapacity, "provisioned capacity") {
		assert.Equal(t, want.MCU, got.Capacity.ProvisionedCapacity.McuCount)
		assert.Equal(t, want.Workers, got.Capacity.ProvisionedCapacity.WorkerCount)
	}
	if assert.NotEmpty(t, got.Plugins) && assert.NotNil(t, got.Plugins[0].CustomPlugin) {
		assert.Equal(t, want.PluginArn, aws.ToString(got.Plugins[0].CustomPlugin.CustomPluginArn))
	}
	assert.Equal(t, want.Bootstrap, aws.ToString(got.KafkaCluster.BootstrapServers))

	for k, v := range want.Config {
		if k == "aws.secret.access.key" {
			assert.Equal(t, redact.Placeholder, got.ConnectorConfiguration[k], "%s must be redacted", k)
			continue
		}
		assert.Equal(t, v, got.ConnectorConfiguration[k], "connector config %s", k)
	}
}
