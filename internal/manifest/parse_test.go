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
	require.Equal(t, "lkc-xxxxx", m.Spec.Target.Cluster)
	require.NotNil(t, m.Spec.ClusterLink)
	require.Equal(t, "source-to-cc", m.Spec.ClusterLink.Name)
	require.NotNil(t, m.Spec.Topics)
	require.Equal(t, "mirror", m.Spec.Topics.Mode)
	require.Equal(t, []string{"*"}, m.Spec.Topics.Include)
	require.NotNil(t, m.Spec.ACLs)
	require.Equal(t, []string{"*"}, m.Spec.ACLs.Include)
	require.NotNil(t, m.Spec.Schemas)
	require.Equal(t, "src.", m.Spec.Topics.Prefix)
	require.Equal(t, map[string]string{"consumer.offset.sync.enable": "true"}, m.Spec.ClusterLink.Configs)
}

func TestParse_Malformed(t *testing.T) {
	_, err := Parse(readFixture(t, "malformed.yaml"))
	require.Error(t, err)
}

func TestParse_UnknownFieldRejected(t *testing.T) {
	_, err := Parse(readFixture(t, "unknown_field.yaml"))
	require.Error(t, err)
}
