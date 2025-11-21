package self_managed_connectors

import (
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

// MockConnectClient is a mock implementation of ConnectAPIClient for testing
type MockConnectClient struct {
	ListConnectorsFunc     func() ([]string, error)
	GetConnectorConfigFunc func(name string) (map[string]any, error)
	GetConnectorStatusFunc func(name string) (map[string]any, error)
}

func (m *MockConnectClient) ListConnectors() ([]string, error) {
	if m.ListConnectorsFunc != nil {
		return m.ListConnectorsFunc()
	}
	return []string{}, nil
}

func (m *MockConnectClient) GetConnectorConfig(name string) (map[string]any, error) {
	if m.GetConnectorConfigFunc != nil {
		return m.GetConnectorConfigFunc(name)
	}
	return map[string]any{}, nil
}

func (m *MockConnectClient) GetConnectorStatus(name string) (map[string]any, error) {
	if m.GetConnectorStatusFunc != nil {
		return m.GetConnectorStatusFunc(name)
	}
	return map[string]any{}, nil
}

func TestSelfManagedConnectorsScanner_Run_NoSelfManagedConnectors(t *testing.T) {
	var listedConnectors []string

	mockClient := &MockConnectClient{
		ListConnectorsFunc: func() ([]string, error) {
			listedConnectors = []string{}
			return listedConnectors, nil
		},
	}

	state := &types.State{
		Regions: []types.DiscoveredRegion{
			{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{
					{
						Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							ClusterID: "test-id",
						},
					},
				},
			},
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		StateFile:  "/tmp/test-state.json",
		State:      state,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
		client:     mockClient,
	}

	err := scanner.Run()

	assert.NoError(t, err)
	assert.Empty(t, listedConnectors, "should have received empty connector list")

	cluster := state.Regions[0].Clusters[0]
	assert.Nil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors,
		"SelfManagedConnectors should remain nil when no connectors exist")
}

func TestSelfManagedConnectorsScanner_Run_WithSelfManagedConnectors(t *testing.T) {
	mockClient := &MockConnectClient{
		ListConnectorsFunc: func() ([]string, error) {
			return []string{"connector-1", "connector-2"}, nil
		},
		GetConnectorConfigFunc: func(name string) (map[string]any, error) {
			configs := map[string]map[string]any{
				"connector-1": {
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
					"tasks.max":       "1",
				},
				"connector-2": {
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
					"tasks.max":       "2",
				},
			}
			return configs[name], nil
		},
		GetConnectorStatusFunc: func(name string) (map[string]any, error) {
			statuses := map[string]map[string]any{
				"connector-1": {
					"connector": map[string]any{
						"state": "RUNNING",
					},
				},
				"connector-2": {
					"connector": map[string]any{
						"state": "PAUSED",
					},
				},
			}
			return statuses[name], nil
		},
	}

	state := &types.State{
		Regions: []types.DiscoveredRegion{
			{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{
					{
						Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							ClusterID: "test-cluster-id",
						},
					},
				},
			},
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		StateFile:  "/tmp/test-state.json",
		State:      state,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
		client:     mockClient,
	}

	err := scanner.Run()
	assert.NoError(t, err)

	// Verify self-managed connectors were added to state
	cluster := state.Regions[0].Clusters[0]
	assert.NotNil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Equal(t, 2, len(cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors))

	connector1 := cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors[0]
	assert.Equal(t, "connector-1", connector1.Name)
	assert.Equal(t, "RUNNING", connector1.State)
	assert.Equal(t, "io.confluent.kafka.connect.datagen.DatagenConnector", connector1.Config["connector.class"])

	connector2 := cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors[1]
	assert.Equal(t, "connector-2", connector2.Name)
	assert.Equal(t, "PAUSED", connector2.State)
	assert.Equal(t, "io.confluent.kafka.connect.datagen.DatagenConnector", connector2.Config["connector.class"])
}

func TestSelfManagedConnectorsScanner_Run_ListSelfManagedConnectorsError(t *testing.T) {
	mockClient := &MockConnectClient{
		ListConnectorsFunc: func() ([]string, error) {
			return nil, errors.New("connection refused")
		},
	}

	state := &types.State{
		Regions: []types.DiscoveredRegion{
			{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{
					{
						Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
					},
				},
			},
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		StateFile:  "/tmp/test-state.json",
		State:      state,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
		client:     mockClient,
	}

	err := scanner.Run()
	if err != nil {
		t.Logf("Expected error occurred: %v", err)
	}

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list connectors")
}

func TestSelfManagedConnectorsScanner_Run_NilClient(t *testing.T) {
	scanner := &SelfManagedConnectorsScanner{
		StateFile:  "/tmp/test-state.json",
		State:      &types.State{},
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
		client:     nil,
	}

	err := scanner.Run()
	if err != nil {
		t.Logf("Expected error occurred: %v", err)
	}

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Connect API client not initialized")
}

func TestSelfManagedConnectorsScanner_GetConnectorDetails_Success(t *testing.T) {
	mockClient := &MockConnectClient{
		GetConnectorConfigFunc: func(name string) (map[string]any, error) {
			return map[string]any{
				"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
				"tasks.max":       "3",
			}, nil
		},
		GetConnectorStatusFunc: func(name string) (map[string]any, error) {
			return map[string]any{
				"connector": map[string]any{
					"state": "RUNNING",
				},
			}, nil
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		client: mockClient,
	}

	connector, err := scanner.getConnectorDetails("test-connector")
	assert.NoError(t, err)
	assert.Equal(t, "test-connector", connector.Name)
	assert.Equal(t, "RUNNING", connector.State)
	assert.Equal(t, "io.confluent.kafka.connect.datagen.DatagenConnector", connector.Config["connector.class"])
}

func TestSelfManagedConnectorsScanner_GetConnectorDetails_ConfigError(t *testing.T) {
	mockClient := &MockConnectClient{
		GetConnectorConfigFunc: func(name string) (map[string]any, error) {
			return nil, errors.New("connector not found")
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		client: mockClient,
	}

	_, err := scanner.getConnectorDetails("test-connector")
	if err != nil {
		t.Logf("Expected error occurred: %v", err)
	}

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get config")
}

func TestSelfManagedConnectorsScanner_GetConnectorDetails_StatusError(t *testing.T) {
	mockClient := &MockConnectClient{
		GetConnectorConfigFunc: func(name string) (map[string]any, error) {
			return map[string]any{
				"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
			}, nil
		},
		GetConnectorStatusFunc: func(name string) (map[string]any, error) {
			return nil, errors.New("status endpoint unavailable")
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		client: mockClient,
	}

	// Should succeed even if status fails (status is optional)
	connector, err := scanner.getConnectorDetails("test-connector")
	if err != nil {
		t.Logf("Expected error occurred: %v", err)
	}

	assert.NoError(t, err)
	assert.Equal(t, "test-connector", connector.Name)
	assert.Equal(t, "", connector.State) // No state since status failed
}

func TestSelfManagedConnectorsScanner_UpdateStateWithConnectors_Success(t *testing.T) {
	state := &types.State{
		Regions: []types.DiscoveredRegion{
			{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{
					{
						Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							ClusterID: "cluster-id",
						},
					},
				},
			},
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		State:      state,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
	}

	connectors := []types.SelfManagedConnector{
		{
			Name:  "connector-1",
			State: "RUNNING",
			Config: map[string]any{
				"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
			},
		},
	}

	err := scanner.updateStateWithConnectors(connectors)
	assert.NoError(t, err)

	// Verify update
	cluster := state.Regions[0].Clusters[0]
	assert.NotNil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Equal(t, 1, len(cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors))
}

func TestSelfManagedConnectorsScanner_UpdateStateWithConnectors_ClusterNotFound(t *testing.T) {
	state := &types.State{
		Regions: []types.DiscoveredRegion{
			{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{
					{
						Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
					},
				},
			},
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		State:      state,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/non-existent-cluster/xyz-789",
	}

	connectors := []types.SelfManagedConnector{
		{
			Name: "connector-1",
		},
	}

	err := scanner.updateStateWithConnectors(connectors)
	if err != nil {
		t.Logf("Expected error occurred: %v", err)
	}

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in state file")
}

func TestSelfManagedConnectorsScanner_Run_PartialFailure(t *testing.T) {
	callCount := 0
	mockClient := &MockConnectClient{
		ListConnectorsFunc: func() ([]string, error) {
			return []string{"good-connector", "bad-connector", "another-good-connector"}, nil
		},
		GetConnectorConfigFunc: func(name string) (map[string]any, error) {
			if name == "bad-connector" {
				return nil, errors.New("connector configuration error")
			}
			callCount++
			return map[string]any{
				"connector.class": "test.Connector",
				"name":            name,
			}, nil
		},
		GetConnectorStatusFunc: func(name string) (map[string]any, error) {
			return map[string]any{
				"connector": map[string]any{
					"state": "RUNNING",
				},
			}, nil
		},
	}

	state := &types.State{
		Regions: []types.DiscoveredRegion{
			{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{
					{
						Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							ClusterID: "test-id",
						},
					},
				},
			},
		},
	}

	scanner := &SelfManagedConnectorsScanner{
		StateFile:  "/tmp/test-state.json",
		State:      state,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
		client:     mockClient,
	}

	err := scanner.Run()
	assert.NoError(t, err) // Should succeed despite partial failure

	// Verify only good connectors were added
	cluster := state.Regions[0].Clusters[0]
	assert.NotNil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Equal(t, 2, len(cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors))
}
