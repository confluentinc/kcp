package manifest_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/stretchr/testify/require"
)

func TestParse_ServiceAccountsAndACLs(t *testing.T) {
	y := []byte(`
apiVersion: kcp.confluent.io/v1alpha1
kind: Migration
spec:
  source: {type: msk, bootstrapServers: ["b:9098"], credentials: c.yaml}
  target: {type: confluent-cloud, clusterId: lkc-1, kafka: {restEndpoint: https://r:443}, clusterCredentials: t.yaml}
  serviceAccounts:
    autoCreate: true
    mapping: {"User:legacy": sa-abc123}
  acls:
    include: ["*"]
    exclude: ["User:ANONYMOUS"]
`)
	m, err := manifest.Parse(y)
	require.NoError(t, err)
	require.NotNil(t, m.Spec.ServiceAccounts)
	require.True(t, m.Spec.ServiceAccounts.AutoCreate)
	require.Equal(t, "sa-abc123", m.Spec.ServiceAccounts.Mapping["User:legacy"])
	require.Equal(t, []string{"*"}, m.Spec.ACLs.Include)
	require.Equal(t, []string{"User:ANONYMOUS"}, m.Spec.ACLs.Exclude)
}

func TestParse_ACLsIAM(t *testing.T) {
	y := []byte(`
apiVersion: kcp.confluent.io/v1alpha1
kind: Migration
metadata: { name: t }
spec:
  source: { type: msk, bootstrapServers: ["b:9098"], credentials: c.yaml }
  target: { type: confluent-cloud, clusterId: lkc-1, clusterCredentials: cc.yaml, cloudCredentials: cl.yaml, kafka: { restEndpoint: https://x:443 } }
  acls:
    include: ["*"]
    iam:
      clusterArn: arn:aws:kafka:us-east-1:123:cluster/m/abc-5
      principalArns: ["arn:aws:iam::123:role/R"]
      verifyEffectiveAccess: false
`)
	m, err := manifest.Parse(y)
	require.NoError(t, err)
	require.NotNil(t, m.Spec.ACLs.IAM)
	require.Equal(t, "arn:aws:kafka:us-east-1:123:cluster/m/abc-5", m.Spec.ACLs.IAM.ClusterArn)
	require.Equal(t, []string{"arn:aws:iam::123:role/R"}, m.Spec.ACLs.IAM.PrincipalArns)
	require.False(t, m.Spec.ACLs.IAM.DiscoverAllRoles)
	require.False(t, m.Spec.ACLs.IAM.VerifyEffectiveAccess)
}
