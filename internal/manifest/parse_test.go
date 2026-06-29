package manifest

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

func TestParse_Valid(t *testing.T) {
	m, err := Parse(readFixture(t, "valid_cc.yaml"))
	require.NoError(t, err)
	require.Equal(t, "kcp.confluent.io/v1alpha1", m.APIVersion)
	require.Equal(t, "Migration", m.Kind)
	require.Equal(t, "example-migration", m.Metadata.Name)
	require.Equal(t, "apache-kafka", m.Spec.Source.Type)
	require.Equal(t, "confluent-cloud", m.Spec.Target.Type)
	require.Equal(t, "lkc-xxxxx", m.Spec.Target.ClusterID)
	require.NotNil(t, m.Spec.ClusterLink)
	require.Equal(t, "source-to-cc", m.Spec.ClusterLink.Name)
	require.NotNil(t, m.Spec.Topics)
	require.Equal(t, "mirror", m.Spec.Topics.Mode)
	require.Equal(t, []string{"*"}, m.Spec.Topics.Include)
	require.NotNil(t, m.Spec.ACLs)
	require.Equal(t, []string{"*"}, m.Spec.ACLs.Include)
	require.NotNil(t, m.Spec.Schemas)
	require.Equal(t, "src.", m.Spec.ClusterLink.Prefix)
	// Offset-sync is expressed via the typed field (not the configs escape-hatch,
	// which would be rejected by Validate as a managed key).
	require.Empty(t, m.Spec.ClusterLink.Configs)
	require.NotNil(t, m.Spec.ClusterLink.ConsumerOffsetSync)
	require.NotNil(t, m.Spec.ClusterLink.ConsumerOffsetSync.Enable)
	require.True(t, *m.Spec.ClusterLink.ConsumerOffsetSync.Enable)
}

func TestParse_Malformed(t *testing.T) {
	_, err := Parse(readFixture(t, "malformed.yaml"))
	require.Error(t, err)
}

func TestParse_UnknownFieldRejected(t *testing.T) {
	_, err := Parse(readFixture(t, "unknown_field.yaml"))
	require.Error(t, err)
}

func TestParse_ClusterLinkConfigFields(t *testing.T) {
	src := `apiVersion: kcp.confluent.io/v1alpha1
kind: Migration
metadata:
  name: m
spec:
  source:
    type: apache-kafka
    bootstrapServers: ["b:9092"]
    credentials: ./s.yaml
  target:
    type: confluent-cloud
    clusterId: lkc-1
    credentials: ./t.yaml
  clusterLink:
    name: l
    source:
      bootstrapServers: ["b:9092"]
      credentials: ./s.yaml
    prefix: "clusterA."
    consumerOffsetSync:
      enable: false
      intervalMs: 1000
      groupFilters:
        - name: "*"
          patternType: LITERAL
          filterType: INCLUDE
    topicConfigSync:
      intervalMs: 5000
`
	m, err := Parse([]byte(src))
	require.NoError(t, err)
	require.Equal(t, []string{"b:9092"}, m.Spec.Source.BootstrapServers)
	cl := m.Spec.ClusterLink
	require.NotNil(t, cl.Source)
	require.Equal(t, []string{"b:9092"}, cl.Source.BootstrapServers)
	require.Equal(t, "./s.yaml", cl.Source.Credentials)
	require.Equal(t, "clusterA.", cl.Prefix)
	require.NotNil(t, cl.ConsumerOffsetSync)
	require.NotNil(t, cl.ConsumerOffsetSync.Enable)
	require.False(t, *cl.ConsumerOffsetSync.Enable)
	require.Equal(t, 1000, cl.ConsumerOffsetSync.IntervalMs)
	require.Len(t, cl.ConsumerOffsetSync.GroupFilters, 1)
	require.Equal(t, "LITERAL", cl.ConsumerOffsetSync.GroupFilters[0].PatternType)
	require.NotNil(t, cl.TopicConfigSync)
	require.Equal(t, 5000, cl.TopicConfigSync.IntervalMs)
}
