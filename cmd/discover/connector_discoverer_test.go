package discover

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testConnectorBootstrap = "b-1.test.kafka.us-east-1.amazonaws.com:9098,b-2.test.kafka.us-east-1.amazonaws.com:9098"

// awsClientInfoWithIAMBrokers returns an AWSClientInformation whose IAM bootstrap
// brokers match testConnectorBootstrap, so an IAM connector is recognised as
// belonging to the cluster.
func awsClientInfoWithIAMBrokers() *types.AWSClientInformation {
	return &types.AWSClientInformation{
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
			BootstrapBrokerStringSaslIam: aws.String(testConnectorBootstrap),
		},
	}
}

// iamConnectorSummary builds an MSK Connect connector summary (IAM auth) whose
// bootstrap servers match the cluster by default.
func iamConnectorSummary(name string) kafkaconnecttypes.ConnectorSummary {
	return kafkaconnecttypes.ConnectorSummary{
		ConnectorArn:   aws.String("arn:aws:kafkaconnect:us-east-1:123:connector/" + name),
		ConnectorName:  aws.String(name),
		ConnectorState: kafkaconnecttypes.ConnectorStateRunning,
		CreationTime:   aws.Time(time.Now()),
		Capacity:       &kafkaconnecttypes.CapacityDescription{},
		KafkaCluster: &kafkaconnecttypes.KafkaClusterDescription{
			ApacheKafkaCluster: &kafkaconnecttypes.ApacheKafkaClusterDescription{
				BootstrapServers: aws.String(testConnectorBootstrap),
			},
		},
		KafkaClusterClientAuthentication: &kafkaconnecttypes.KafkaClusterClientAuthenticationDescription{
			AuthenticationType: kafkaconnecttypes.KafkaClusterClientAuthenticationTypeIam,
		},
	}
}

func listOneConnector(c kafkaconnecttypes.ConnectorSummary) func(context.Context, *kafkaconnect.ListConnectorsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
	return func(context.Context, *kafkaconnect.ListConnectorsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
		return &kafkaconnect.ListConnectorsOutput{Connectors: []kafkaconnecttypes.ConnectorSummary{c}}, nil
	}
}

func describeWithConfig(cfg map[string]string) func(context.Context, *kafkaconnect.DescribeConnectorInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
	return func(context.Context, *kafkaconnect.DescribeConnectorInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
		return &kafkaconnect.DescribeConnectorOutput{ConnectorConfiguration: cfg}, nil
	}
}

func TestDiscoverMatchingConnectors_RedactsConfigBeforeReturn(t *testing.T) {
	connect := &stubMSKConnectService{
		listConnectorsFn:    listOneConnector(iamConnectorSummary("pg-sink")),
		describeConnectorFn: describeWithConfig(map[string]string{"database.password": "hunter2", "tasks.max": "3"}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err)
	require.Len(t, connectors, 1)

	cfg := connectors[0].ConnectorConfiguration
	assert.Equal(t, redact.Placeholder, cfg["database.password"], "secret must be redacted before return")
	assert.Equal(t, "3", cfg["tasks.max"], "benign config preserved")
}

func TestDiscoverMatchingConnectors_PaginatesAllPages(t *testing.T) {
	// ListConnectors returns two pages; discovery must follow NextToken and
	// collect connectors from every page, not just the first (R3).
	page1 := iamConnectorSummary("conn-page1")
	page2 := iamConnectorSummary("conn-page2")
	connect := &stubMSKConnectService{
		listConnectorsFn: func(_ context.Context, params *kafkaconnect.ListConnectorsInput, _ ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
			if params.NextToken == nil {
				return &kafkaconnect.ListConnectorsOutput{
					Connectors: []kafkaconnecttypes.ConnectorSummary{page1},
					NextToken:  aws.String("page-2-token"),
				}, nil
			}
			return &kafkaconnect.ListConnectorsOutput{
				Connectors: []kafkaconnecttypes.ConnectorSummary{page2},
			}, nil
		},
		describeConnectorFn: describeWithConfig(map[string]string{"tasks.max": "3"}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err)
	require.Len(t, connectors, 2, "connectors from both pages must be collected")
	names := []string{connectors[0].ConnectorName, connectors[1].ConnectorName}
	assert.ElementsMatch(t, []string{"conn-page1", "conn-page2"}, names)
}

func TestDiscoverMatchingConnectors_SinglePageTerminates(t *testing.T) {
	// A lone page (NextToken=nil) terminates after exactly one call — no extra
	// fetch, no infinite loop.
	calls := 0
	connect := &stubMSKConnectService{
		listConnectorsFn: func(_ context.Context, _ *kafkaconnect.ListConnectorsInput, _ ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
			calls++
			return &kafkaconnect.ListConnectorsOutput{Connectors: []kafkaconnecttypes.ConnectorSummary{iamConnectorSummary("only")}}, nil
		},
		describeConnectorFn: describeWithConfig(map[string]string{"tasks.max": "3"}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err)
	require.Len(t, connectors, 1)
	assert.Equal(t, 1, calls, "single page must result in exactly one ListConnectors call")
}

func TestDiscoverMatchingConnectors_AccessDeniedIsNonFatal(t *testing.T) {
	connect := &stubMSKConnectService{
		listConnectorsFn: func(context.Context, *kafkaconnect.ListConnectorsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
			return nil, errors.New("AccessDeniedException: not authorized to perform kafkaconnect:ListConnectors")
		},
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err, "access-denied on ListConnectors must be non-fatal")
	assert.Empty(t, connectors)
}

func TestDiscoverMatchingConnectors_MidPaginationErrorReturnsCollectedSoFar(t *testing.T) {
	// Page 1 succeeds, page 2 errors. The error is non-fatal and the connectors
	// already collected from page 1 must be preserved (not discarded), so a
	// connector on an un-fetched page keeps its prior redacted copy in state via
	// the merge-on-rerun seam.
	page1 := iamConnectorSummary("conn-page1")
	connect := &stubMSKConnectService{
		listConnectorsFn: func(_ context.Context, params *kafkaconnect.ListConnectorsInput, _ ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
			if params.NextToken == nil {
				return &kafkaconnect.ListConnectorsOutput{
					Connectors: []kafkaconnecttypes.ConnectorSummary{page1},
					NextToken:  aws.String("page-2-token"),
				}, nil
			}
			return nil, errors.New("AccessDeniedException on page 2")
		},
		describeConnectorFn: describeWithConfig(map[string]string{"tasks.max": "3"}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err, "mid-pagination error must be non-fatal")
	require.Len(t, connectors, 1, "connectors collected before the error must be preserved")
	assert.Equal(t, "conn-page1", connectors[0].ConnectorName)
}

func TestDiscoverMatchingConnectors_NonMatchingExcluded(t *testing.T) {
	connector := iamConnectorSummary("other-cluster-sink")
	connector.KafkaCluster.ApacheKafkaCluster.BootstrapServers = aws.String("b-9.other.kafka.us-east-1.amazonaws.com:9098")
	connect := &stubMSKConnectService{
		listConnectorsFn:    listOneConnector(connector),
		describeConnectorFn: describeWithConfig(map[string]string{}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err)
	assert.Empty(t, connectors, "connector whose bootstrap doesn't match the cluster is excluded")
}

func TestDiscoverMatchingConnectors_SkipsConnectorWithNilFields(t *testing.T) {
	// A connector with missing (nil) required summary fields must be skipped with a
	// warning rather than panicking and aborting discovery (R3). The nil-field guard
	// runs on summary fields before any DescribeConnector call, so describe is never
	// attempted for such a connector.
	bad := kafkaconnecttypes.ConnectorSummary{
		ConnectorArn:  aws.String("arn:aws:kafkaconnect:us-east-1:123:connector/bad"),
		ConnectorName: aws.String("bad"),
		// KafkaClusterClientAuthentication, KafkaCluster, Capacity, CreationTime all nil.
	}
	describeCalls := 0
	connect := &stubMSKConnectService{
		listConnectorsFn: listOneConnector(bad),
		describeConnectorFn: func(context.Context, *kafkaconnect.DescribeConnectorInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
			describeCalls++
			return &kafkaconnect.DescribeConnectorOutput{}, nil
		},
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err, "incomplete connector must be skipped, not abort discovery")
	assert.Empty(t, connectors)
	assert.Equal(t, 0, describeCalls, "DescribeConnector must not be called for a connector skipped by the summary guard")
}

func TestDiscoverMatchingConnectors_DoesNotDescribeNonMatching(t *testing.T) {
	// DescribeConnector is the most throttling-prone MSK Connect call and must only
	// fire for connectors that actually belong to this cluster. A non-matching
	// connector is filtered on its ListConnectors summary alone, so describe is never
	// attempted — guarding against a regression that slides the call back above the
	// bootstrap-match filter and reintroduces C×K describe calls.
	connector := iamConnectorSummary("other-cluster-sink")
	connector.KafkaCluster.ApacheKafkaCluster.BootstrapServers = aws.String("b-9.other.kafka.us-east-1.amazonaws.com:9098")

	describeCalls := 0
	connect := &stubMSKConnectService{
		listConnectorsFn: listOneConnector(connector),
		describeConnectorFn: func(context.Context, *kafkaconnect.DescribeConnectorInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
			describeCalls++
			return &kafkaconnect.DescribeConnectorOutput{ConnectorConfiguration: map[string]string{}}, nil
		},
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err)
	assert.Empty(t, connectors, "non-matching connector is excluded")
	assert.Equal(t, 0, describeCalls, "DescribeConnector must not be called for a connector that fails the bootstrap-match filter")
}

func TestDiscoverMatchingConnectors_DoesNotLogRawSecret(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	connect := &stubMSKConnectService{
		listConnectorsFn:    listOneConnector(iamConnectorSummary("pg-sink")),
		describeConnectorFn: describeWithConfig(map[string]string{"database.password": "hunter2"}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	_, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err)
	assert.NotContains(t, logBuf.String(), "hunter2", "raw secret must never be logged")
}
