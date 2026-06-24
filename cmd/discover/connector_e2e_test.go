package discover

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEndToEnd_MSKConnectorSecretNeverPersisted runs the real MSK Connect
// discovery seam with a secret-laden config, persists the result, and asserts no
// raw secret survives serialization to the state file (R11).
func TestEndToEnd_MSKConnectorSecretNeverPersisted(t *testing.T) {
	const dbSecret = "topsecret-msk-pw"
	const awsSecret = "AKIArawsecretkey"
	connect := &stubMSKConnectService{
		listConnectorsFn: listOneConnector(iamConnectorSummary("pg-sink")),
		describeConnectorFn: describeWithConfig(map[string]string{
			"database.password":     dbSecret,
			"aws.secret.access.key": awsSecret,
			"tasks.max":             "3",
		}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err)
	require.Len(t, connectors, 1)

	st := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{{
					Arn:                  testClusterArn,
					AWSClientInformation: types.AWSClientInformation{Connectors: connectors},
				}},
			}},
		},
	}
	stateFile := filepath.Join(t.TempDir(), "kcp-state.json")
	require.NoError(t, st.PersistStateFile(stateFile))

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	for _, secret := range []string{dbSecret, awsSecret} {
		assert.NotContainsf(t, string(data), secret, "raw secret %q must not be serialized", secret)
	}

	// Reload (escaping-agnostic) and confirm the secret values are the placeholder.
	reloaded, err := types.NewStateFromFile(stateFile)
	require.NoError(t, err)
	rc, err := reloaded.GetClusterByArn(testClusterArn)
	require.NoError(t, err)
	require.Len(t, rc.AWSClientInformation.Connectors, 1)
	cfg := rc.AWSClientInformation.Connectors[0].ConnectorConfiguration
	assert.Equal(t, redact.Placeholder, cfg["database.password"])
	assert.Equal(t, redact.Placeholder, cfg["aws.secret.access.key"])
	assert.Equal(t, "3", cfg["tasks.max"])
}

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
	cluster.KafkaAdminClientInformation.SetSelfManagedConnectors([]types.SelfManagedConnector{{Name: "sm-c", Config: smCfg}})

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

	// Self-managed connector access path (cluster.KafkaAdminClientInformation.SelfManagedConnectors).
	require.NotNil(t, rc.KafkaAdminClientInformation.SelfManagedConnectors)
	require.Len(t, rc.KafkaAdminClientInformation.SelfManagedConnectors.Connectors, 1)
	assert.Equal(t, redact.Placeholder, rc.KafkaAdminClientInformation.SelfManagedConnectors.Connectors[0].Config["database.password"])
}
