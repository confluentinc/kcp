package msk

import (
	"context"
	"errors"
	"log/slog"

	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

type MSKService struct {
	client *kafka.Client
}

func NewMSKService(client *kafka.Client) *MSKService {
	return &MSKService{client: client}
}

func (ms *MSKService) DescribeCluster(ctx context.Context, clusterArn *string) (*kafkatypes.Cluster, error) {
	cluster, err := ms.client.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}
	return cluster.ClusterInfo, nil
}

func (ms *MSKService) GetBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error) {
	brokers, err := ms.client.GetBootstrapBrokers(ctx, &kafka.GetBootstrapBrokersInput{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to get bootstrap brokers: %v", err)
	}
	return brokers, nil
}

func (ms *MSKService) IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (*bool, error) {

	if cluster.Provisioned == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision == nil {
		return nil, nil
	}

	configurationArn := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn
	configurationRevision := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision

	describeConfigurationRevisionInput := &kafka.DescribeConfigurationRevisionInput{
		Arn:      configurationArn,
		Revision: configurationRevision,
	}

	revision, err := ms.client.DescribeConfigurationRevision(ctx, describeConfigurationRevisionInput)
	if err != nil {
		return nil, fmt.Errorf("failed to describe configuration revision: %v", err)
	}

	serverProperties := revision.ServerProperties

	// ServerProperties is already a []byte containing plain text configuration
	// First try to use it directly as plain text
	propertiesText := string(serverProperties)

	if strings.Contains(propertiesText, "replica.selector.class=org.apache.kafka.common.replica.RackAwareReplicaSelector") {
		return aws.Bool(true), nil
	}
	return aws.Bool(false), nil
}

func (ms *MSKService) GetCompatibleKafkaVersions(ctx context.Context, clusterArn *string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	versions, err := ms.client.GetCompatibleKafkaVersions(ctx, &kafka.GetCompatibleKafkaVersionsInput{
		ClusterArn: clusterArn,
	})
	if err != nil {
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Compatible versions not supported for MSK Serverless clusters, skipping compatible versions scan")
			return &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []kafkatypes.CompatibleKafkaVersion{},
			}, nil
		}
		return nil, fmt.Errorf("❌ Failed to get compatible versions: %v", err)
	}
	return versions, nil
}

func (ms *MSKService) GetClusterPolicy(ctx context.Context, clusterArn *string) (*kafka.GetClusterPolicyOutput, error) {
	policy, err := ms.client.GetClusterPolicy(ctx, &kafka.GetClusterPolicyInput{
		ClusterArn: clusterArn,
	})
	if err == nil {
		return policy, nil
	}

	var notFoundErr *kafkatypes.NotFoundException
	if errors.As(err, &notFoundErr) {
		return new(kafka.GetClusterPolicyOutput), nil
	}
	return nil, err
}

func (ms *MSKService) DescribeClusterV2(ctx context.Context, clusterArn *string) (*kafka.DescribeClusterV2Output, error) {
	cluster, err := ms.client.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}
	return cluster, nil
}

func (ms *MSKService) ListClientVpcConnections(ctx context.Context, clusterArn *string) ([]kafkatypes.ClientVpcConnection, error) {

	var connections []kafkatypes.ClientVpcConnection
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListClientVpcConnections(ctx, &kafka.ListClientVpcConnectionsInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters") {
				slog.Warn("⚠️ VPC connectivity not supported for MSK Serverless clusters in this region, skipping VPC connections scan")
				return []kafkatypes.ClientVpcConnection{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing client vpc connections: %v", err)
		}
		connections = append(connections, output.ClientVpcConnections...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return connections, nil
}

func (ms *MSKService) ListClusterOperationsV2(ctx context.Context, clusterArn *string) ([]kafkatypes.ClusterOperationV2Summary, error) {
	var operations []kafkatypes.ClusterOperationV2Summary
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListClusterOperationsV2(ctx, &kafka.ListClusterOperationsV2Input{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("❌ Failed listing operations: %v", err)
		}
		operations = append(operations, output.ClusterOperationInfoList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return operations, nil
}

func (ms *MSKService) ListNodes(ctx context.Context, clusterArn *string) ([]kafkatypes.NodeInfo, error) {
	var nodes []kafkatypes.NodeInfo
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListNodes(ctx, &kafka.ListNodesInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
				slog.Warn("⚠️ Node listing not supported for MSK Serverless clusters, skipping Nodes scan")
				return []kafkatypes.NodeInfo{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing nodes: %v", err)
		}
		nodes = append(nodes, output.NodeInfoList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return nodes, nil
}

func (ms *MSKService) ListScramSecrets(ctx context.Context, clusterArn *string) ([]string, error) {
	var secrets []string
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListScramSecrets(ctx, &kafka.ListScramSecretsInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
				slog.Warn("⚠️ Scram secret listing not supported for MSK Serverless clusters, skipping scram secrets scan")
				return []string{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing secrets: %v", err)
		}
		secrets = append(secrets, output.SecretArnList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return secrets, nil
}

func (ms *MSKService) ListClusters(ctx context.Context, maxResults int32) ([]kafkatypes.Cluster, error) {
	slog.Info("🔍 scanning for MSK clusters", "region", ms.client.Options().Region)

	var nextToken *string

	var clusterInfoList []kafkatypes.Cluster

	for {
		listClustersOutput, err := ms.client.ListClustersV2(ctx, &kafka.ListClustersV2Input{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("❌ Failed to list clusters: %v", err)
		}

		clusterInfoList = append(clusterInfoList, listClustersOutput.ClusterInfoList...)

		if listClustersOutput.NextToken == nil {
			break
		}
		nextToken = listClustersOutput.NextToken
	}

	slog.Info("✨ found clusters", "count", len(clusterInfoList))

	return clusterInfoList, nil
}
