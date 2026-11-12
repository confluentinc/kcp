//go:build integration

package mskemulator

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
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
