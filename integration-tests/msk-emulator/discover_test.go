//go:build integration

package mskemulator

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverMultiRegionSingleRun(t *testing.T) {
	dir := t.TempDir()
	runKCP(t, dir, "discover", "--region", "us-east-1,eu-west-3", "--skip-costs")

	st := loadState(t, dir)
	assert.ElementsMatch(t, []string{"kcp-it-iam-scram", "kcp-it-serverless"}, clusterNames(findRegion(t, st, "us-east-1")))
	assert.ElementsMatch(t, []string{"kcp-it-scram-only"}, clusterNames(findRegion(t, st, "eu-west-3")))

	assertProvisioned(t, findCluster(t, st, "us-east-1", "kcp-it-iam-scram"), fx.IAMScram)
	assertServerless(t, findCluster(t, st, "us-east-1", "kcp-it-serverless"), fx.Serverless)
	assertProvisioned(t, findCluster(t, st, "eu-west-3", "kcp-it-scram-only"), fx.ScramOnly)

	var cfgArns []string
	for _, c := range findRegion(t, st, "us-east-1").Configurations {
		cfgArns = append(cfgArns, aws.ToString(c.Arn))
	}
	assert.Contains(t, cfgArns, fx.ConfigurationArn)

	creds := loadCreds(t, dir)
	for _, c := range []seededCluster{fx.IAMScram, fx.Serverless, fx.ScramOnly} {
		assertCredAuth(t, findCredCluster(t, creds, c.Region, c.Name), c)
	}
}

func TestConnectorsAndUserCredentialsSurviveRediscover(t *testing.T) {
	dir := t.TempDir()
	runKCP(t, dir, "discover", "--region", "us-east-1,eu-west-3", "--skip-costs")
	runKCP(t, dir, "scan", "msk-connectors", "--state-file", "kcp-state.json", "--region", "us-east-1")

	st := loadState(t, dir)
	conns := findCluster(t, st, "us-east-1", "kcp-it-iam-scram").AWSClientInformation.Connectors
	require.Len(t, conns, 1)
	assertConnector(t, conns[0], fx.Connector)
	assert.Empty(t, findCluster(t, st, "us-east-1", "kcp-it-serverless").AWSClientInformation.Connectors)

	creds := loadCreds(t, dir)
	for i, r := range creds.Regions {
		for j, c := range r.Clusters {
			if c.Name == "kcp-it-scram-only" {
				creds.Regions[i].Clusters[j].AuthMethod.SASLScram.Username = "alice"
				creds.Regions[i].Clusters[j].AuthMethod.SASLScram.Password = "alice-pw"
			}
		}
	}
	require.NoError(t, creds.WriteToFile(filepath.Join(dir, "msk-credentials.yaml")))

	runKCP(t, dir, "discover", "--region", "us-east-1", "--skip-costs")
	runKCP(t, dir, "discover", "--region", "eu-west-3", "--skip-costs")

	st = loadState(t, dir)
	conns = findCluster(t, st, "us-east-1", "kcp-it-iam-scram").AWSClientInformation.Connectors
	require.Len(t, conns, 1, "connectors must survive a re-run of discover")
	assertConnector(t, conns[0], fx.Connector)

	scram := findCredCluster(t, loadCreds(t, dir), "eu-west-3", "kcp-it-scram-only").AuthMethod.SASLScram
	require.NotNil(t, scram)
	assert.True(t, scram.Use)
	assert.Equal(t, "alice", scram.Username)
	assert.Equal(t, "alice-pw", scram.Password)
}

func TestDiscoverSeparateRegionRunsAppend(t *testing.T) {
	dir := t.TempDir()
	runKCP(t, dir, "discover", "--region", "us-east-1", "--skip-costs")
	runKCP(t, dir, "discover", "--region", "eu-west-3", "--skip-costs")

	st := loadState(t, dir)
	assert.ElementsMatch(t, []string{"kcp-it-iam-scram", "kcp-it-serverless"}, clusterNames(findRegion(t, st, "us-east-1")))
	assert.ElementsMatch(t, []string{"kcp-it-scram-only"}, clusterNames(findRegion(t, st, "eu-west-3")))
	assertProvisioned(t, findCluster(t, st, "us-east-1", "kcp-it-iam-scram"), fx.IAMScram)
	assertProvisioned(t, findCluster(t, st, "eu-west-3", "kcp-it-scram-only"), fx.ScramOnly)

	creds := loadCreds(t, dir)
	assertCredAuth(t, findCredCluster(t, creds, "us-east-1", "kcp-it-iam-scram"), fx.IAMScram)
	assertCredAuth(t, findCredCluster(t, creds, "eu-west-3", "kcp-it-scram-only"), fx.ScramOnly)
}

func TestDiscoverTargetedClusterArnAppends(t *testing.T) {
	dir := t.TempDir()
	runKCP(t, dir, "discover", "--region", "us-east-1", "--skip-costs")
	before := findRegion(t, loadState(t, dir), "us-east-1")

	ctx := context.Background()
	net, err := seedNetwork(ctx, "ap-northeast-1", "10.30", 3)
	require.NoError(t, err)
	targeted, err := seedProvisioned(ctx, seededCluster{
		Name: "kcp-it-targeted", Region: "ap-northeast-1", KafkaVersion: "3.6.0",
		InstanceType: "kafka.m5.large", Brokers: 3, VolumeGB: 100, IAM: true, Network: net,
	}, "")
	require.NoError(t, err)

	runKCP(t, dir, "discover", "--cluster-arn", targeted.Arn, "--skip-costs")

	st := loadState(t, dir)
	assertProvisioned(t, findCluster(t, st, "ap-northeast-1", "kcp-it-targeted"), targeted)
	assert.ElementsMatch(t, clusterNames(before), clusterNames(findRegion(t, st, "us-east-1")))
	for _, b := range before.Clusters {
		a := findCluster(t, st, "us-east-1", b.Name)
		assert.Equal(t, b.Arn, a.Arn)
		assert.Equal(t, b.AWSClientInformation.ClusterNetworking, a.AWSClientInformation.ClusterNetworking)
		assert.Equal(t, len(b.AWSClientInformation.Nodes), len(a.AWSClientInformation.Nodes))
	}

	creds := loadCreds(t, dir)
	assertCredAuth(t, findCredCluster(t, creds, "ap-northeast-1", "kcp-it-targeted"), targeted)
	assertCredAuth(t, findCredCluster(t, creds, "us-east-1", "kcp-it-iam-scram"), fx.IAMScram)
}

func TestDiscoverDropsDeletedCluster(t *testing.T) {
	ctx := context.Background()
	net, err := seedNetwork(ctx, "ap-south-1", "10.40", 3)
	require.NoError(t, err)
	doomed, err := seedProvisioned(ctx, seededCluster{
		Name: "kcp-it-delete-me", Region: "ap-south-1", KafkaVersion: "3.6.0",
		InstanceType: "kafka.m5.large", Brokers: 3, VolumeGB: 100, IAM: true, Network: net,
	}, "")
	require.NoError(t, err)

	dir := t.TempDir()
	runKCP(t, dir, "discover", "--region", "ap-south-1", "--skip-costs")
	findCluster(t, loadState(t, dir), "ap-south-1", "kcp-it-delete-me")
	findCredCluster(t, loadCreds(t, dir), "ap-south-1", "kcp-it-delete-me")

	require.NoError(t, deleteClusterAndWait(ctx, "ap-south-1", doomed.Arn))
	runKCP(t, dir, "discover", "--region", "ap-south-1", "--skip-costs")

	assert.Empty(t, findRegion(t, loadState(t, dir), "ap-south-1").Clusters, "a deleted cluster is dropped from the state file")
	creds := loadCreds(t, dir)
	idx := slices.IndexFunc(creds.Regions, func(r types.RegionAuth) bool { return r.Name == "ap-south-1" })
	require.NotEqual(t, -1, idx, "the region entry stays in msk-credentials.yaml")
	assert.Empty(t, creds.Regions[idx].Clusters, "a deleted cluster is dropped from msk-credentials.yaml")
}
