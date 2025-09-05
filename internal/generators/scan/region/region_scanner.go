package region

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/confluentinc/kcp/internal/client"
	mskConnect "github.com/confluentinc/kcp/internal/services/msk_connect"
	"github.com/confluentinc/kcp/internal/types"
)

// AuthenticationSummarizer defines the interface for summarizing cluster authentication
type AuthenticationSummarizer interface {
	SummariseAuthentication(cluster kafkatypes.Cluster) string
}

// DefaultAuthenticationSummarizer provides the default implementation
type DefaultAuthenticationSummarizer struct{}

func (d *DefaultAuthenticationSummarizer) SummariseAuthentication(cluster kafkatypes.Cluster) string {
	saslScramEnabled := false
	saslIamEnabled := false
	tlsEnabled := false
	unauthenticatedEnabled := false

	if cluster.ClusterType == kafkatypes.ClusterTypeServerless {
		if cluster.Serverless != nil &&
			cluster.Serverless.ClientAuthentication != nil &&
			cluster.Serverless.ClientAuthentication.Sasl != nil &&
			cluster.Serverless.ClientAuthentication.Sasl.Iam != nil &&
			cluster.Serverless.ClientAuthentication.Sasl.Iam.Enabled != nil {
			saslIamEnabled = *cluster.Serverless.ClientAuthentication.Sasl.Iam.Enabled
		}
	} else {
		if cluster.Provisioned != nil && cluster.Provisioned.ClientAuthentication != nil {
			// Check SASL/SCRAM
			if cluster.Provisioned.ClientAuthentication.Sasl != nil &&
				cluster.Provisioned.ClientAuthentication.Sasl.Scram != nil &&
				cluster.Provisioned.ClientAuthentication.Sasl.Scram.Enabled != nil {
				saslScramEnabled = *cluster.Provisioned.ClientAuthentication.Sasl.Scram.Enabled
			}

			// Check SASL/IAM
			if cluster.Provisioned.ClientAuthentication.Sasl != nil &&
				cluster.Provisioned.ClientAuthentication.Sasl.Iam != nil &&
				cluster.Provisioned.ClientAuthentication.Sasl.Iam.Enabled != nil {
				saslIamEnabled = *cluster.Provisioned.ClientAuthentication.Sasl.Iam.Enabled
			}

			// Check TLS
			if cluster.Provisioned.ClientAuthentication.Tls != nil &&
				cluster.Provisioned.ClientAuthentication.Tls.Enabled != nil {
				tlsEnabled = *cluster.Provisioned.ClientAuthentication.Tls.Enabled
			}

			// Check Unauthenticated
			if cluster.Provisioned.ClientAuthentication.Unauthenticated != nil &&
				cluster.Provisioned.ClientAuthentication.Unauthenticated.Enabled != nil {
				unauthenticatedEnabled = *cluster.Provisioned.ClientAuthentication.Unauthenticated.Enabled
			}
		}
	}

	authTypes := []string{}
	if saslScramEnabled {
		authTypes = append(authTypes, string(types.AuthTypeSASLSCRAM))
	}
	if saslIamEnabled {
		authTypes = append(authTypes, string(types.AuthTypeIAM))
	}
	if tlsEnabled {
		authTypes = append(authTypes, string(types.AuthTypeTLS))
	}
	if unauthenticatedEnabled {
		authTypes = append(authTypes, string(types.AuthTypeUnauthenticated))
	}

	if len(authTypes) == 0 {
		return string(types.AuthTypeUnauthenticated)
	}

	return strings.Join(authTypes, ",")
}

// RegionScannerMSKClient defines the MSK client methods used by RegionScanner
type RegionScannerMSKClient interface {
	ListClustersV2(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error)
	ListVpcConnections(ctx context.Context, params *kafka.ListVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListVpcConnectionsOutput, error)
	ListConfigurations(ctx context.Context, params *kafka.ListConfigurationsInput, optFns ...func(*kafka.Options)) (*kafka.ListConfigurationsOutput, error)
	DescribeConfigurationRevision(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error)
	ListKafkaVersions(ctx context.Context, params *kafka.ListKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.ListKafkaVersionsOutput, error)
	ListReplicators(ctx context.Context, params *kafka.ListReplicatorsInput, optFns ...func(*kafka.Options)) (*kafka.ListReplicatorsOutput, error)
	DescribeReplicator(ctx context.Context, params *kafka.DescribeReplicatorInput, optFns ...func(*kafka.Options)) (*kafka.DescribeReplicatorOutput, error)
}

type ScanRegionOpts struct {
	Region string
}

type RegionScanner struct {
	region         string
	mskClient      RegionScannerMSKClient
	authSummarizer AuthenticationSummarizer
}

func NewRegionScanner(mskClient RegionScannerMSKClient, opts ScanRegionOpts) *RegionScanner {
	return &RegionScanner{
		region:         opts.Region,
		mskClient:      mskClient,
		authSummarizer: &DefaultAuthenticationSummarizer{},
	}
}

// NewRegionScannerWithAuthSummarizer creates a RegionScanner with a custom AuthenticationSummarizer (useful for testing)
func NewRegionScannerWithAuthSummarizer(region string, mskClient RegionScannerMSKClient, authSummarizer AuthenticationSummarizer) *RegionScanner {
	return &RegionScanner{
		region:         region,
		mskClient:      mskClient,
		authSummarizer: authSummarizer,
	}
}

func (rs *RegionScanner) Run() error {
	slog.Info("🚀 starting MSK environment scan", "region", rs.region)

	ctx := context.TODO()

	scanResult, err := rs.ScanRegion(ctx)
	if err != nil {
		return fmt.Errorf("❌ Failed to scan region: %v", err)
	}

	if err := scanResult.WriteAsJson(); err != nil {
		return fmt.Errorf("❌ Failed to generate json report: %v", err)
	}
	// Generate markdown report
	if err := scanResult.WriteAsMarkdown(false); err != nil {
		return fmt.Errorf("❌ Failed to generate markdown report: %v", err)
	}

	slog.Info("✅ region scan complete",
		"region", rs.region,
		"clusterCount", len(scanResult.Clusters),
		"filePath", scanResult.GetJsonPath(),
		"markdownPath", scanResult.GetMarkdownPath(),
	)

	return nil
}

func (rs *RegionScanner) ScanRegion(ctx context.Context) (*types.RegionScanResult, error) {
	maxResults := int32(100)
	result := &types.RegionScanResult{
		Timestamp: time.Now(),
		Region:    rs.region,
	}

	clusters, err := rs.listClusters(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	result.Clusters = clusters

	vpcConnections, err := rs.scanVpcConnections(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	result.VpcConnections = vpcConnections

	configurations, err := rs.scanConfigurations(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	result.Configurations = configurations

	kafkaVersions, err := rs.scanKafkaVersions(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	result.KafkaVersions = kafkaVersions

	replicators, err := rs.scanReplicators(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	result.Replicators = replicators

	connectors, err := rs.scanConnectors(ctx)
	if err != nil {
		return nil, err
	}
	result.Connectors = connectors

	return result, nil
}

func (rs *RegionScanner) listClusters(ctx context.Context, maxResults int32) ([]types.ClusterSummary, error) {
	slog.Info("🔍 scanning for MSK clusters", "region", rs.region)

	var clusters []types.ClusterSummary
	var nextToken *string

	for {
		listClustersOutput, err := rs.mskClient.ListClustersV2(ctx, &kafka.ListClustersV2Input{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("❌ Failed to list clusters: %v", err)
		}

		for _, cluster := range listClustersOutput.ClusterInfoList {

			auth := rs.authSummarizer.SummariseAuthentication(cluster)

			publicAccess := false
			if cluster.ClusterType == kafkatypes.ClusterTypeProvisioned &&
				cluster.Provisioned != nil &&
				cluster.Provisioned.BrokerNodeGroupInfo != nil &&
				cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo != nil &&
				cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess != nil &&
				cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess.Type != nil {
				publicAccess = aws.ToString(cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess.Type) != "DISABLED"
			}

			clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(cluster)
			clusterSummary := types.ClusterSummary{
				ClusterName:                     aws.ToString(cluster.ClusterName),
				ClusterARN:                      aws.ToString(cluster.ClusterArn),
				Status:                          aws.ToString((*string)(&cluster.State)),
				Type:                            aws.ToString((*string)(&cluster.ClusterType)),
				Authentication:                  auth,
				PublicAccess:                    publicAccess,
				ClientBrokerEncryptionInTransit: clientBrokerEncryptionInTransit,
			}
			clusters = append(clusters, clusterSummary)
		}

		if listClustersOutput.NextToken == nil {
			break
		}
		nextToken = listClustersOutput.NextToken
	}

	slog.Info("✨ found clusters", "count", len(clusters))
	return clusters, nil
}

func (rs *RegionScanner) scanVpcConnections(ctx context.Context, maxResults int32) ([]kafkatypes.VpcConnection, error) {
	slog.Info("🔍 scanning for VpcConnections", "region", rs.region)

	var connections []kafkatypes.VpcConnection
	var nextToken *string

	for {
		output, err := rs.mskClient.ListVpcConnections(ctx, &kafka.ListVpcConnectionsInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("error listing vpc connections: %v", err)
		}
		connections = append(connections, output.VpcConnections...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	slog.Info("✨ found vpcConnections", "count", len(connections))
	return connections, nil
}

func (rs *RegionScanner) scanConfigurations(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error) {
	slog.Info("🔍 scanning for configurations", "region", rs.region)
	var configurations []kafka.DescribeConfigurationRevisionOutput
	var nextToken *string

	for {
		output, err := rs.mskClient.ListConfigurations(ctx, &kafka.ListConfigurationsInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("error listing configurations: %v", err)
		}

		for _, configuration := range output.Configurations {
			revision, err := rs.mskClient.DescribeConfigurationRevision(context.Background(), &kafka.DescribeConfigurationRevisionInput{
				Arn:      configuration.Arn,
				Revision: configuration.LatestRevision.Revision,
			})
			if err != nil {
				return nil, fmt.Errorf("error describing configuration revision: %v", err)
			}
			configurations = append(configurations, *revision)
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	slog.Info("✨ found configurations", "count", len(configurations))
	return configurations, nil
}

func (rs *RegionScanner) scanKafkaVersions(ctx context.Context, maxResults int32) ([]kafkatypes.KafkaVersion, error) {
	slog.Info("🔍 scanning for kafka versions", "region", rs.region)
	var versions []kafkatypes.KafkaVersion
	var nextToken *string

	for {
		output, err := rs.mskClient.ListKafkaVersions(ctx, &kafka.ListKafkaVersionsInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("error listing kafka versions: %v", err)
		}
		if len(output.KafkaVersions) > 0 {
			versions = append(versions, output.KafkaVersions...)
		}
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	slog.Info("✨ found kafka versions", "count", len(versions))
	return versions, nil
}

func (rs *RegionScanner) scanReplicators(ctx context.Context, maxResults int32) ([]kafka.DescribeReplicatorOutput, error) {
	slog.Info("🔍 scanning for replicators", "region", rs.region)
	var replicators []kafka.DescribeReplicatorOutput
	var nextToken *string

	for {
		output, err := rs.mskClient.ListReplicators(ctx, &kafka.ListReplicatorsInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("error listing replicators: %v", err)
		}

		for _, replicator := range output.Replicators {
			describeReplicator, err := rs.mskClient.DescribeReplicator(context.Background(), &kafka.DescribeReplicatorInput{
				ReplicatorArn: replicator.ReplicatorArn,
			})
			if err != nil {
				return nil, fmt.Errorf("error describing replicator: %v", err)
			}
			replicators = append(replicators, *describeReplicator)
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	slog.Info("✨ found replicators", "count", len(replicators))
	return replicators, nil
}

func (rs *RegionScanner) scanConnectors(ctx context.Context) ([]types.ConnectorSummary, error) {
	slog.Info("🔍 scanning for connectors", "region", rs.region)
	var connectors []types.ConnectorSummary

	mskConnectClient, err := client.NewMSKConnectClient(rs.region)
	if err != nil {
		slog.Error("❌ Failed to create msk connect client", "region", rs.region, "error", err)
		return nil, fmt.Errorf("❌ Failed to create msk connect client: %w", err)
	}

	mskConnectService := mskConnect.NewMSKConnectService(mskConnectClient)
	mskConnectResult, err := mskConnectService.ListConnectors(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list connectors: %w", err)
	}

	for _, connector := range mskConnectResult.Connectors {
		describeConnector, err := mskConnectService.DescribeConnector(ctx, connector.ConnectorArn)
		if err != nil {
			return nil, fmt.Errorf("❌ Failed to describe connector: %w", err)
		}
		connectors = append(connectors, types.ConnectorSummary{
			ConnectorArn:                     aws.ToString(connector.ConnectorArn),
			ConnectorName:                    aws.ToString(connector.ConnectorName),
			ConnectorState:                   string(connector.ConnectorState),
			CreationTime:                     connector.CreationTime.Format(time.RFC3339),
			KafkaCluster:                     *connector.KafkaCluster.ApacheKafkaCluster,
			KafkaClusterClientAuthentication: *connector.KafkaClusterClientAuthentication,
			Capacity:                         *connector.Capacity,
			Plugins:                          describeConnector.Plugins,
			ConnectorConfiguration:           describeConnector.ConnectorConfiguration,
		})
	}

	return connectors, nil
}
