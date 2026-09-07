package discover

import (
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEndToEnd_ConsumerReadsRedactedConnectorsAfterRoundTrip verifies that, after
// both connector shapes are persisted and the state is reloaded, the
// migrate-connectors access paths surface the (redacted) connectors unchanged (R15).
func TestEndToEnd_ConsumerReadsRedactedConnectorsAfterRoundTrip(t *testing.T) {
	mskCfg, _ := redact.RedactStringMap(map[string]string{"connection.password": "p", "connector.class": "io.x"})
	smCfg, _ := redact.RedactAnyMap(map[string]any{"database.password": "q", "topics": "orders"})

	st := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{{
					Arn: testClusterArn,
					AWSClientInformation: types.AWSClientInformation{
						Connectors: []types.ConnectorSummary{{ConnectorName: "msk-c", ConnectorConfiguration: mskCfg}},
					},
				}},
			}},
		},
	}
	cluster, err := st.GetClusterByArn(testClusterArn)
	require.NoError(t, err)
	cluster.KafkaAdminClientInformation.SetConnectCluster("http://connect:8083", []types.Connector{{Name: "sm-c", Config: smCfg}})

	stateFile := filepath.Join(t.TempDir(), "kcp-state.json")
	require.NoError(t, st.PersistStateFile(stateFile))

	// Reload exactly as the migrate-connectors consumer does.
	reloaded, err := types.NewStateFromFile(stateFile)
	require.NoError(t, err)
	rc, err := reloaded.GetClusterByArn(testClusterArn)
	require.NoError(t, err)

	// MSK connector access path (cluster.AWSClientInformation.Connectors).
	require.Len(t, rc.AWSClientInformation.Connectors, 1)
	assert.Equal(t, redact.Placeholder, rc.AWSClientInformation.Connectors[0].ConnectorConfiguration["connection.password"])

	// Self-managed connector access path (cluster.KafkaAdminClientInformation.ConnectClusters).
	require.Len(t, rc.KafkaAdminClientInformation.ConnectClusters, 1)
	require.Len(t, rc.KafkaAdminClientInformation.ConnectClusters[0].Connectors, 1)
	assert.Equal(t, redact.Placeholder, rc.KafkaAdminClientInformation.ConnectClusters[0].Connectors[0].Config["database.password"])
}
