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
	// A connector with missing (nil) required description fields must be skipped
	// with a warning rather than panicking and aborting discovery (R3).
	bad := kafkaconnecttypes.ConnectorSummary{
		ConnectorArn:  aws.String("arn:aws:kafkaconnect:us-east-1:123:connector/bad"),
		ConnectorName: aws.String("bad"),
		// KafkaClusterClientAuthentication, KafkaCluster, Capacity, CreationTime all nil.
	}
	connect := &stubMSKConnectService{
		listConnectorsFn:    listOneConnector(bad),
		describeConnectorFn: describeWithConfig(map[string]string{}),
	}
	msk, ec2svc, metrics := defaultStubs()
	cd := newTestClusterDiscovererWithConnect(msk, ec2svc, metrics, connect)

	connectors, err := cd.discoverMatchingConnectors(context.Background(), awsClientInfoWithIAMBrokers())
	require.NoError(t, err, "incomplete connector must be skipped, not abort discovery")
	assert.Empty(t, connectors)
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
