package msk_connectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMSKConnect is a hand-rolled mock of MSKConnectService.
type mockMSKConnect struct {
	listOutputs []*kafkaconnect.ListConnectorsOutput
	listErr     error
	describe    map[string]*kafkaconnect.DescribeConnectorOutput
	describeErr error
	listCalls   int
}

func (m *mockMSKConnect) ListConnectors(ctx context.Context, in *kafkaconnect.ListConnectorsInput, _ ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := m.listOutputs[m.listCalls]
	m.listCalls++
	return out, nil
}

func (m *mockMSKConnect) DescribeConnector(ctx context.Context, in *kafkaconnect.DescribeConnectorInput, _ ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	return m.describe[aws.ToString(in.ConnectorArn)], nil
}

// iamConnector builds a ListConnectors summary that uses IAM auth and the given
// bootstrap string, so it exercises the IAM broker-matching path.
func iamConnector(arn, name, bootstrap string) kafkaconnecttypes.ConnectorSummary {
	now := time.Now()
	return kafkaconnecttypes.ConnectorSummary{
		ConnectorArn:   aws.String(arn),
		ConnectorName:  aws.String(name),
		ConnectorState: kafkaconnecttypes.ConnectorStateRunning,
		CreationTime:   &now,
		Capacity:       &kafkaconnecttypes.CapacityDescription{},
		KafkaClusterClientAuthentication: &kafkaconnecttypes.KafkaClusterClientAuthenticationDescription{
			AuthenticationType: kafkaconnecttypes.KafkaClusterClientAuthenticationTypeIam,
		},
		KafkaCluster: &kafkaconnecttypes.KafkaClusterDescription{
			ApacheKafkaCluster: &kafkaconnecttypes.ApacheKafkaClusterDescription{
				BootstrapServers: aws.String(bootstrap),
			},
		},
	}
}

// iamClusterInfo returns AWSClientInformation whose IAM bootstrap brokers are the
// given address, so GetAllBootstrapBrokersForAuthType(AuthTypeIAM) resolves to it.
func iamClusterInfo(bootstrap string) *types.AWSClientInformation {
	return &types.AWSClientInformation{
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
			BootstrapBrokerStringPublicSaslIam: aws.String(bootstrap),
		},
	}
}

func TestMatchConnectorsForCluster_MatchesAndRedacts(t *testing.T) {
	bootstrap := "b-1.example:9098,b-2.example:9098"
	svc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{
				iamConnector("arn:c1", "match-me", bootstrap),
				iamConnector("arn:c2", "other-cluster", "b-9.other:9098"),
			},
		}},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{
			"arn:c1": {
				ConnectorConfiguration: map[string]string{
					"connector.class":   "io.example.MyConnector",
					"database.password": "supersecret",
				},
			},
		},
	}

	s := &MSKConnectorsScanner{}
	got := s.matchConnectorsForCluster(context.Background(), svc, iamClusterInfo(bootstrap))

	require.Len(t, got, 1)
	assert.Equal(t, "match-me", got[0].ConnectorName)
	assert.Equal(t, "io.example.MyConnector", got[0].ConnectorConfiguration["connector.class"])
	assert.Equal(t, redact.Placeholder, got[0].ConnectorConfiguration["database.password"])
}

func TestMatchConnectorsForCluster_ListErrorIsNonFatal(t *testing.T) {
	svc := &mockMSKConnect{listErr: errors.New("access denied")}
	s := &MSKConnectorsScanner{}
	got := s.matchConnectorsForCluster(context.Background(), svc, iamClusterInfo("b-1.example:9098"))
	assert.Empty(t, got)
}

func TestRun_RegionScope_PopulatesAndPersists(t *testing.T) {
	bootstrap := "b-1.example:9098"
	arn := "arn:aws:kafka:us-east-1:123456789012:cluster/c1/abc"

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{{
					Arn:                  arn,
					Region:               "us-east-1",
					AWSClientInformation: *iamClusterInfo(bootstrap),
				}},
			}},
		},
	}
	path := writeTempState(t, state)

	svc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:c1", "match-me", bootstrap)},
		}},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{
			"arn:c1": {ConnectorConfiguration: map[string]string{"connector.class": "io.example.C"}},
		},
	}

	s := NewMSKConnectorsScanner(MSKConnectorsScannerOpts{
		StateFile: path,
		State:     state,
		Regions:   []string{"us-east-1"},
	})
	s.newService = func(region string) (MSKConnectService, error) { return svc, nil }

	require.NoError(t, s.Run())

	// In-memory state updated.
	require.Len(t, state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.Connectors, 1)
	assert.Equal(t, "match-me", state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.Connectors[0].ConnectorName)

	// Persisted to disk.
	reloaded, err := types.NewStateFromFile(path)
	require.NoError(t, err)
	require.Len(t, reloaded.MSKSources.Regions[0].Clusters[0].AWSClientInformation.Connectors, 1)
}

func TestMatchConnectorsForCluster_PaginatesAllPages(t *testing.T) {
	// ListConnectors returns two pages; discovery must follow NextToken and
	// collect connectors from every page, not just the first (R3).
	bootstrap := "b-1.example:9098"
	svc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{
			{
				Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:page1", "conn-page1", bootstrap)},
				NextToken:  aws.String("page-2-token"),
			},
			{
				Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:page2", "conn-page2", bootstrap)},
			},
		},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{
			"arn:page1": {ConnectorConfiguration: map[string]string{"tasks.max": "3"}},
			"arn:page2": {ConnectorConfiguration: map[string]string{"tasks.max": "3"}},
		},
	}

	s := &MSKConnectorsScanner{}
	got := s.matchConnectorsForCluster(context.Background(), svc, iamClusterInfo(bootstrap))

	require.Len(t, got, 2, "connectors from both pages must be collected")
	names := []string{got[0].ConnectorName, got[1].ConnectorName}
	assert.ElementsMatch(t, []string{"conn-page1", "conn-page2"}, names)
	assert.Equal(t, 2, svc.listCalls, "must page through both ListConnectors calls")
}

func TestMatchConnectorsForCluster_SkipsConnectorWithIncompleteSummary(t *testing.T) {
	// A connector with missing (nil) required summary fields must be skipped with a
	// warning rather than panicking and aborting discovery (R3). The nil-field guard
	// runs on summary fields before any DescribeConnector call, so describe is never
	// attempted for such a connector.
	bad := kafkaconnecttypes.ConnectorSummary{
		ConnectorArn:  aws.String("arn:bad"),
		ConnectorName: aws.String("bad"),
		// KafkaClusterClientAuthentication, KafkaCluster, Capacity, CreationTime all nil.
	}
	svc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{bad},
		}},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{},
	}

	s := &MSKConnectorsScanner{}
	got := s.matchConnectorsForCluster(context.Background(), svc, iamClusterInfo("b-1.example:9098"))

	assert.Empty(t, got, "connector with incomplete summary must be skipped")
}

func TestMatchConnectorsForCluster_SkipsUnsupportedAuthType(t *testing.T) {
	// A connector whose auth type is "None" with no encryption-in-transit info
	// cannot be mapped to a cluster AuthType, so it must be skipped rather than
	// erroring out the whole scan.
	now := time.Now()
	unsupported := kafkaconnecttypes.ConnectorSummary{
		ConnectorArn:   aws.String("arn:unsupported"),
		ConnectorName:  aws.String("unsupported-auth"),
		ConnectorState: kafkaconnecttypes.ConnectorStateRunning,
		CreationTime:   &now,
		Capacity:       &kafkaconnecttypes.CapacityDescription{},
		KafkaClusterClientAuthentication: &kafkaconnecttypes.KafkaClusterClientAuthenticationDescription{
			AuthenticationType: kafkaconnecttypes.KafkaClusterClientAuthenticationTypeNone,
		},
		KafkaCluster: &kafkaconnecttypes.KafkaClusterDescription{
			ApacheKafkaCluster: &kafkaconnecttypes.ApacheKafkaClusterDescription{
				BootstrapServers: aws.String("b-1.example:9098"),
			},
		},
		// KafkaClusterEncryptionInTransit is nil, so connectorAuthType errors.
	}
	svc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{unsupported},
		}},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{},
	}

	s := &MSKConnectorsScanner{}
	got := s.matchConnectorsForCluster(context.Background(), svc, iamClusterInfo("b-1.example:9098"))

	assert.Empty(t, got, "connector with unsupported auth type must be skipped")
}

func TestMatchConnectorsForCluster_DescribeConnectorFailureIsSkipped(t *testing.T) {
	// A matching connector whose DescribeConnector call fails must be skipped
	// (non-fatal) rather than aborting the whole scan.
	bootstrap := "b-1.example:9098"
	svc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:c1", "match-me", bootstrap)},
		}},
		describeErr: errors.New("describe failed"),
	}

	s := &MSKConnectorsScanner{}
	got := s.matchConnectorsForCluster(context.Background(), svc, iamClusterInfo(bootstrap))

	assert.Empty(t, got, "connector must be skipped when DescribeConnector fails")
}

func TestRun_ClusterArnScope_OnlyTargetsNamedCluster(t *testing.T) {
	bootstrap := "b-1.example:9098"
	arn1 := "arn:aws:kafka:us-east-1:123456789012:cluster/c1/abc"
	arn2 := "arn:aws:kafka:us-east-1:123456789012:cluster/c2/def"

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{
					{Arn: arn1, Region: "us-east-1", AWSClientInformation: *iamClusterInfo(bootstrap)},
					{Arn: arn2, Region: "us-east-1", AWSClientInformation: *iamClusterInfo("b-9.other:9098")},
				},
			}},
		},
	}
	path := writeTempState(t, state)

	svc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:c1", "match-me", bootstrap)},
		}},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{
			"arn:c1": {ConnectorConfiguration: map[string]string{"connector.class": "io.example.C"}},
		},
	}

	s := NewMSKConnectorsScanner(MSKConnectorsScannerOpts{
		StateFile:   path,
		State:       state,
		Regions:     []string{"us-east-1"},
		ClusterArns: []string{arn1},
	})
	s.newService = func(region string) (MSKConnectService, error) { return svc, nil }

	require.NoError(t, s.Run())

	assert.Len(t, state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.Connectors, 1) // arn1 populated
	assert.Empty(t, state.MSKSources.Regions[0].Clusters[1].AWSClientInformation.Connectors)  // arn2 untouched
}

// fakeCollector returns canned raw connector metrics, or err if set.
type fakeCollector struct {
	gotNames []string
	out      *types.ClusterMetrics
	err      error
}

func (f *fakeCollector) CollectConnectorMetrics(_ context.Context, names []string, _ types.CloudWatchTimeWindow, _ string) (*types.ClusterMetrics, error) {
	f.gotNames = names
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func TestRun_CloudWatchMetrics_PopulatesConnectorMetrics(t *testing.T) {
	bootstrap := "b-1.example:9098"
	arn := "arn:aws:kafka:us-east-1:123456789012:cluster/c1/abc"
	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{{
					Arn: arn, Region: "us-east-1", AWSClientInformation: *iamClusterInfo(bootstrap),
				}},
			}},
		},
	}
	path := writeTempState(t, state)

	connectSvc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:c1", "match-me", bootstrap)},
		}},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{
			"arn:c1": {ConnectorConfiguration: map[string]string{"connector.class": "io.example.C"}},
		},
	}
	ts := time.Now().UTC()
	v := 1.0
	collector := &fakeCollector{out: &types.ClusterMetrics{
		MetricMetadata: types.MetricMetadata{Period: 300, StartDate: ts, EndDate: ts.Add(time.Hour)},
		Results: []cloudwatchtypes.MetricDataResult{{
			Label: aws.String("BytesInPerSec (match-me)"), Timestamps: []time.Time{ts}, Values: []float64{v},
		}},
	}}

	s := NewMSKConnectorsScanner(MSKConnectorsScannerOpts{
		StateFile: path, State: state, Regions: []string{"us-east-1"},
		MetricsGranularity: "1d",
	})
	s.newService = func(string) (MSKConnectService, error) { return connectSvc, nil }
	s.newMetricsCollector = func(string) (ConnectorMetricsCollector, error) { return collector, nil }

	require.NoError(t, s.Run())

	cm := state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.ConnectorMetrics
	require.NotNil(t, cm)
	assert.Equal(t, types.MetricBackendCloudWatch, cm.Metadata.MetricsSource)
	require.NotEmpty(t, cm.Metrics)
	assert.Equal(t, "BytesInPerSec (match-me)", cm.Metrics[0].Label)
	assert.NotEmpty(t, cm.Aggregates)
	assert.Equal(t, []string{"match-me"}, collector.gotNames) // collected for the matched connector
}

func TestRun_MetricsCollectorError_IsNonFatal(t *testing.T) {
	// A metrics-collector failure must not abort the scan (R3): connectors are
	// already persisted by the time metrics collection runs, so Run() should
	// still succeed and leave ConnectorMetrics nil for that cluster.
	bootstrap := "b-1.example:9098"
	arn := "arn:aws:kafka:us-east-1:123456789012:cluster/c1/abc"
	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{{
				Name: "us-east-1",
				Clusters: []types.DiscoveredCluster{{
					Arn: arn, Region: "us-east-1", AWSClientInformation: *iamClusterInfo(bootstrap),
				}},
			}},
		},
	}
	path := writeTempState(t, state)

	connectSvc := &mockMSKConnect{
		listOutputs: []*kafkaconnect.ListConnectorsOutput{{
			Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:c1", "match-me", bootstrap)},
		}},
		describe: map[string]*kafkaconnect.DescribeConnectorOutput{
			"arn:c1": {ConnectorConfiguration: map[string]string{"connector.class": "io.example.C"}},
		},
	}
	collector := &fakeCollector{err: errors.New("cloudwatch unavailable")}

	s := NewMSKConnectorsScanner(MSKConnectorsScannerOpts{
		StateFile: path, State: state, Regions: []string{"us-east-1"},
		MetricsGranularity: "1d",
	})
	s.newService = func(string) (MSKConnectService, error) { return connectSvc, nil }
	s.newMetricsCollector = func(string) (ConnectorMetricsCollector, error) { return collector, nil }

	require.NoError(t, s.Run(), "a metrics-collector error must not abort the scan")

	// Connectors are persisted despite the metrics failure.
	require.Len(t, state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.Connectors, 1)
	assert.Equal(t, "match-me", state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.Connectors[0].ConnectorName)
	assert.Nil(t, state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.ConnectorMetrics)

	// Persisted to disk too.
	reloaded, err := types.NewStateFromFile(path)
	require.NoError(t, err)
	require.Len(t, reloaded.MSKSources.Regions[0].Clusters[0].AWSClientInformation.Connectors, 1)
	assert.Nil(t, reloaded.MSKSources.Regions[0].Clusters[0].AWSClientInformation.ConnectorMetrics)
}

func TestRun_NoMetricsFlag_LeavesConnectorMetricsNil(t *testing.T) {
	bootstrap := "b-1.example:9098"
	arn := "arn:aws:kafka:us-east-1:123456789012:cluster/c1/abc"
	state := &types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{
		Name:     "us-east-1",
		Clusters: []types.DiscoveredCluster{{Arn: arn, Region: "us-east-1", AWSClientInformation: *iamClusterInfo(bootstrap)}},
	}}}}
	path := writeTempState(t, state)
	connectSvc := &mockMSKConnect{listOutputs: []*kafkaconnect.ListConnectorsOutput{{
		Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnector("arn:c1", "match-me", bootstrap)},
	}}, describe: map[string]*kafkaconnect.DescribeConnectorOutput{"arn:c1": {ConnectorConfiguration: map[string]string{}}}}

	s := NewMSKConnectorsScanner(MSKConnectorsScannerOpts{StateFile: path, State: state, Regions: []string{"us-east-1"}})
	s.newService = func(string) (MSKConnectService, error) { return connectSvc, nil }

	require.NoError(t, s.Run())
	assert.Nil(t, state.MSKSources.Regions[0].Clusters[0].AWSClientInformation.ConnectorMetrics)
}

func TestBootstrapMatches_ExactHostPortOnly(t *testing.T) {
	tests := []struct {
		name               string
		connectorBootstrap string
		brokerAddresses    []string
		want               bool
	}{
		{
			name:               "exact single match",
			connectorBootstrap: "b-1.example.com:9098",
			brokerAddresses:    []string{"b-1.example.com:9098"},
			want:               true,
		},
		{
			name:               "match among multiple comma-separated hosts with surrounding spaces",
			connectorBootstrap: "b-1.example.com:9098, b-2.example.com:9098",
			brokerAddresses:    []string{"b-2.example.com:9098"},
			want:               true,
		},
		{
			name:               "substring/suffix attack must not match",
			connectorBootstrap: "b-1.example.com.attacker.example:9098",
			brokerAddresses:    []string{"b-1.example.com:9098"},
			want:               false,
		},
		{
			name:               "no overlap",
			connectorBootstrap: "b-1.example.com:9098",
			brokerAddresses:    []string{"b-9.other.example:9098"},
			want:               false,
		},
		{
			name:               "empty connector bootstrap",
			connectorBootstrap: "",
			brokerAddresses:    []string{"b-1.example.com:9098"},
			want:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bootstrapMatches(tt.connectorBootstrap, tt.brokerAddresses)
			assert.Equal(t, tt.want, got)
		})
	}
}
