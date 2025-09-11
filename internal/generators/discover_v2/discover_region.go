package discover_v2

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

type RegionDiscovererMSKService interface {
	ListClustersNEW(ctx context.Context, maxResults int32) ([]kafkatypes.Cluster, error)
	GetConfigurationsNEW(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error)
}

type RegionDiscoverer struct {
	region            string
	mskService        RegionDiscovererMSKService
	clusterDiscoverer ClusterDiscoverer
}

func NewRegionDiscoverer(mskService RegionDiscovererMSKService, clusterDiscoverer ClusterDiscoverer) *RegionDiscoverer {
	return &RegionDiscoverer{
		mskService:        mskService,
		clusterDiscoverer: clusterDiscoverer,
	}
}

func (rd *RegionDiscoverer) Discover(ctx context.Context, region string) (*types.DiscoveredRegion, error) {
	slog.Info("🔍 discovering region", "region", region)
	result := types.DiscoveredRegion{
		Name: region,
	}

	maxResults := int32(250)

	configurations, err := rd.mskService.GetConfigurationsNEW(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	result.Configurations = configurations

	clusters, err := rd.discoverClusters(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	result.Clusters = clusters

	// do costs also

	return &result, nil
}

func (rd *RegionDiscoverer) discoverClusters(ctx context.Context, maxResults int32) ([]types.DiscoveredCluster, error) {
	slog.Info("🔍 discovering clusters")

	clusters, err := rd.mskService.ListClustersNEW(ctx, maxResults)
	if err != nil {
		return nil, err
	}

	discoveredClusters := []types.DiscoveredCluster{}

	for _, cluster := range clusters {
		discoveredCluster, err := rd.clusterDiscoverer.Discover(ctx, aws.ToString(cluster.ClusterArn))
		if err != nil {
			slog.Error("failed to discover cluster", "cluster", aws.ToString(cluster.ClusterArn), "error", err)
			continue
		}
		discoveredClusters = append(discoveredClusters, *discoveredCluster)
	}

	return discoveredClusters, nil

}

// func (rd *RegionDiscoverer) listClusters(ctx context.Context, maxResults int32) ([]types.ClusterSummary, error) {
// 	// slog.Info("🔍 scanning for MSK clusters", "region", rd.region)

// 	var clusters []types.ClusterSummary
// 	// var nextToken *string

// 	// for {
// 	// 	listClustersOutput, err := rd.mskClient.ListClustersV2(ctx, &kafka.ListClustersV2Input{
// 	// 		MaxResults: &maxResults,
// 	// 		NextToken:  nextToken,
// 	// 	})
// 	// 	if err != nil {
// 	// 		return nil, fmt.Errorf("❌ Failed to list clusters: %v", err)
// 	// 	}

// 	// 	for _, cluster := range listClustersOutput.ClusterInfoList {
// 	// 		auth := rd.authSummarizer.SummariseAuthentication(cluster)

// 	// 		publicAccess := false
// 	// 		if cluster.ClusterType == kafkatypes.ClusterTypeProvisioned &&
// 	// 			cluster.Provisioned != nil &&
// 	// 			cluster.Provisioned.BrokerNodeGroupInfo != nil &&
// 	// 			cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo != nil &&
// 	// 			cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess != nil &&
// 	// 			cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess.Type != nil {
// 	// 			publicAccess = aws.ToString(cluster.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess.Type) != "DISABLED"
// 	// 		}

// 	// 		clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(cluster)
// 	// 		clusterSummary := types.ClusterSummary{
// 	// 			ClusterName:                     aws.ToString(cluster.ClusterName),
// 	// 			ClusterARN:                      aws.ToString(cluster.ClusterArn),
// 	// 			Status:                          aws.ToString((*string)(&cluster.State)),
// 	// 			Type:                            aws.ToString((*string)(&cluster.ClusterType)),
// 	// 			Authentication:                  auth,
// 	// 			PublicAccess:                    publicAccess,
// 	// 			ClientBrokerEncryptionInTransit: clientBrokerEncryptionInTransit,
// 	// 		}
// 	// 		clusters = append(clusters, clusterSummary)
// 	// 	}

// 	// 	if listClustersOutput.NextToken == nil {
// 	// 		break
// 	// 	}
// 	// 	nextToken = listClustersOutput.NextToken
// 	// }

// 	// slog.Info("✨ found clusters", "count", len(clusters))
// 	return clusters, nil
// }

// func (rd *RegionDiscoverer) scanVpcConnections(ctx context.Context, maxResults int32) ([]kafkatypes.VpcConnection, error) {
// 	slog.Info("🔍 scanning for VpcConnections", "region", rd.region)

// 	var connections []kafkatypes.VpcConnection
// 	var nextToken *string

// 	for {
// 		output, err := rd.mskService.ListVpcConnections(ctx, &kafka.ListVpcConnectionsInput{
// 			MaxResults: &maxResults,
// 			NextToken:  nextToken,
// 		})
// 		if err != nil {
// 			return nil, fmt.Errorf("error listing vpc connections: %v", err)
// 		}
// 		connections = append(connections, output.VpcConnections...)
// 		if output.NextToken == nil {
// 			break
// 		}
// 		nextToken = output.NextToken
// 	}

// 	slog.Info("✨ found vpcConnections", "count", len(connections))
// 	return connections, nil
// }

// func (rd *RegionDiscoverer) scanConfigurations(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error) {
// 	slog.Info("🔍 scanning for configurations", "region", rd.region)
// 	var configurations []kafka.DescribeConfigurationRevisionOutput
// 	var nextToken *string

// 	for {
// 		output, err := rd.mskService.ListConfigurations(ctx, &kafka.ListConfigurationsInput{
// 			MaxResults: &maxResults,
// 			NextToken:  nextToken,
// 		})
// 		if err != nil {
// 			return nil, fmt.Errorf("error listing configurations: %v", err)
// 		}

// 		for _, configuration := range output.Configurations {
// 			revision, err := rd.mskService.DescribeConfigurationRevision(context.Background(), &kafka.DescribeConfigurationRevisionInput{
// 				Arn:      configuration.Arn,
// 				Revision: configuration.LatestRevision.Revision,
// 			})
// 			if err != nil {
// 				return nil, fmt.Errorf("error describing configuration revision: %v", err)
// 			}
// 			configurations = append(configurations, *revision)
// 		}

// 		if output.NextToken == nil {
// 			break
// 		}
// 		nextToken = output.NextToken
// 	}

// 	slog.Info("✨ found configurations", "count", len(configurations))
// 	return configurations, nil
// }

// func (rd *RegionDiscoverer) scanKafkaVersions(ctx context.Context, maxResults int32) ([]kafkatypes.KafkaVersion, error) {
// 	slog.Info("🔍 scanning for kafka versions", "region", rd.region)
// 	var versions []kafkatypes.KafkaVersion
// 	var nextToken *string

// 	for {
// 		output, err := rd.mskService.ListKafkaVersions(ctx, &kafka.ListKafkaVersionsInput{
// 			MaxResults: &maxResults,
// 			NextToken:  nextToken,
// 		})
// 		if err != nil {
// 			return nil, fmt.Errorf("error listing kafka versions: %v", err)
// 		}
// 		if len(output.KafkaVersions) > 0 {
// 			versions = append(versions, output.KafkaVersions...)
// 		}
// 		if output.NextToken == nil {
// 			break
// 		}
// 		nextToken = output.NextToken
// 	}

// 	slog.Info("✨ found kafka versions", "count", len(versions))
// 	return versions, nil
// }

// func (rd *RegionDiscoverer) scanReplicators(ctx context.Context, maxResults int32) ([]kafka.DescribeReplicatorOutput, error) {
// 	slog.Info("🔍 scanning for replicators", "region", rd.region)
// 	var replicators []kafka.DescribeReplicatorOutput
// 	var nextToken *string

// 	for {
// 		output, err := rd.mskService.ListReplicators(ctx, &kafka.ListReplicatorsInput{
// 			MaxResults: &maxResults,
// 			NextToken:  nextToken,
// 		})
// 		if err != nil {
// 			return nil, fmt.Errorf("error listing replicators: %v", err)
// 		}

// 		for _, replicator := range output.Replicators {
// 			describeReplicator, err := rd.mskService.DescribeReplicator(context.Background(), &kafka.DescribeReplicatorInput{
// 				ReplicatorArn: replicator.ReplicatorArn,
// 			})
// 			if err != nil {
// 				return nil, fmt.Errorf("error describing replicator: %v", err)
// 			}
// 			replicators = append(replicators, *describeReplicator)
// 		}

// 		if output.NextToken == nil {
// 			break
// 		}
// 		nextToken = output.NextToken
// 	}

// 	slog.Info("✨ found replicators", "count", len(replicators))
// 	return replicators, nil
// }
