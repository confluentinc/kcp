package msk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

type MSKService struct {
	client *kafka.Client
}

func NewMSKService(client *kafka.Client) *MSKService {
	return &MSKService{client: client}
}

func (ms *MSKService) GetBootstrapBrokers(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error) {
	brokers, err := ms.client.GetBootstrapBrokers(ctx, &kafka.GetBootstrapBrokersInput{
		ClusterArn: &clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to get bootstrap brokers: %v", err)
	}
	return brokers, nil
}

func (ms *MSKService) IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (bool, error) {
	if cluster.Provisioned == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision == nil {
		return false, nil
	}

	configurationArn := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn
	configurationRevision := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision

	describeConfigurationRevisionInput := &kafka.DescribeConfigurationRevisionInput{
		Arn:      configurationArn,
		Revision: configurationRevision,
	}

	revision, err := ms.client.DescribeConfigurationRevision(ctx, describeConfigurationRevisionInput)
	if err != nil {
		return false, fmt.Errorf("failed to describe configuration revision: %v", err)
	}

	serverProperties := revision.ServerProperties

	// ServerProperties is already a []byte containing plain text configuration
	// First try to use it directly as plain text
	propertiesText := string(serverProperties)

	if strings.Contains(propertiesText, "replica.selector.class=org.apache.kafka.common.replica.RackAwareReplicaSelector") {
		return true, nil
	}
	return false, nil
}

func (ms *MSKService) GetCompatibleKafkaVersions(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	versions, err := ms.client.GetCompatibleKafkaVersions(ctx, &kafka.GetCompatibleKafkaVersionsInput{
		ClusterArn: &clusterArn,
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

func (ms *MSKService) GetClusterPolicy(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error) {
	policy, err := ms.client.GetClusterPolicy(ctx, &kafka.GetClusterPolicyInput{
		ClusterArn: &clusterArn,
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

func (ms *MSKService) DescribeClusterV2(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error) {
	cluster, err := ms.client.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{
		ClusterArn: &clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}
	return cluster, nil
}

func (ms *MSKService) ListClientVpcConnections(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClientVpcConnection, error) {
	var connections []kafkatypes.ClientVpcConnection
	var nextToken *string

	for {
		output, err := ms.client.ListClientVpcConnections(ctx, &kafka.ListClientVpcConnectionsInput{
			MaxResults: &maxResults,
			ClusterArn: &clusterArn,
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

func (ms *MSKService) ListClusterOperationsV2(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClusterOperationV2Summary, error) {
	var operations []kafkatypes.ClusterOperationV2Summary
	var nextToken *string

	for {
		output, err := ms.client.ListClusterOperationsV2(ctx, &kafka.ListClusterOperationsV2Input{
			MaxResults: &maxResults,
			ClusterArn: &clusterArn,
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

func (ms *MSKService) ListNodes(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.NodeInfo, error) {
	var nodes []kafkatypes.NodeInfo
	var nextToken *string

	for {
		output, err := ms.client.ListNodes(ctx, &kafka.ListNodesInput{
			MaxResults: &maxResults,
			ClusterArn: &clusterArn,
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

func (ms *MSKService) ListScramSecrets(ctx context.Context, clusterArn string, maxResults int32) ([]string, error) {
	var secrets []string
	var nextToken *string

	for {
		output, err := ms.client.ListScramSecrets(ctx, &kafka.ListScramSecretsInput{
			MaxResults: &maxResults,
			ClusterArn: &clusterArn,
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

func (ms *MSKService) GetConfigurations(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error) {
	var configurations []kafka.DescribeConfigurationRevisionOutput
	var nextToken *string

	for {
		output, err := ms.client.ListConfigurations(ctx, &kafka.ListConfigurationsInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("error listing configurations: %v", err)
		}

		for _, configuration := range output.Configurations {
			revision, err := ms.client.DescribeConfigurationRevision(ctx, &kafka.DescribeConfigurationRevisionInput{
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

func (ms *MSKService) ListTopics(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.TopicInfo, error) {
	slog.Info("listing topics")

	var topics []kafkatypes.TopicInfo
	var nextToken *string

	for {
		output, err := ms.client.ListTopics(ctx, &kafka.ListTopicsInput{
			ClusterArn: &clusterArn,
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})

		if err != nil {
			return nil, fmt.Errorf("failed to list topics through the AWS API: %v", err)
		}

		topics = append(topics, output.Topics...)

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	slog.Info("found topics", "count", len(topics))
	return topics, nil
}

func (ms *MSKService) DescribeTopic(ctx context.Context, clusterArn string, topicName string) (*kafka.DescribeTopicOutput, error) {
	output, err := ms.client.DescribeTopic(ctx, &kafka.DescribeTopicInput{
		ClusterArn: &clusterArn,
		TopicName:  &topicName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe topic %s: %v", topicName, err)
	}

	return output, nil
}

func (ms *MSKService) GetTopicsWithConfigs(ctx context.Context, clusterArn string) ([]types.TopicDetails, error) {
	const numWorkers = 25
	slog.Info("scanning topics via AWS API", "clusterArn", clusterArn)

	topicList, err := ms.ListTopics(ctx, clusterArn, 100)
	if err != nil {
		return nil, err
	}

	topicChan := make(chan kafkatypes.TopicInfo, len(topicList))
	resultChan := make(chan types.TopicDetails, len(topicList))

	var wg sync.WaitGroup
	var progressCount int

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for topicInfo := range topicChan {
				if topicInfo.TopicName == nil {
					continue
				}

				topicDesc, err := ms.DescribeTopic(ctx, clusterArn, *topicInfo.TopicName)
				if err != nil {
					slog.Warn("failed to describe topic", "topicName", *topicInfo.TopicName, "error", err)
					continue
				}

				configurations, err := decodeTopicConfigs(topicDesc.Configs)
				if err != nil {
					slog.Warn("failed to decode topic configuration", "topicName", *topicInfo.TopicName, "error", err)
					configurations = make(map[string]*string)
				}

				partitionCount := 0
				if topicDesc.PartitionCount != nil {
					partitionCount = int(*topicDesc.PartitionCount)
				}

				replicationFactor := 0
				if topicDesc.ReplicationFactor != nil {
					replicationFactor = int(*topicDesc.ReplicationFactor)
				}

				resultChan <- types.TopicDetails{
					Name:              *topicInfo.TopicName,
					Partitions:        partitionCount,
					ReplicationFactor: replicationFactor,
					Configurations:    configurations,
				}

				progressCount++
				if progressCount%50 == 0 {
					slog.Info("topic processing progress", "processed", progressCount, "total", len(topicList))
				}
			}
		}()
	}

	for _, topic := range topicList {
		topicChan <- topic
	}
	close(topicChan)

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var topicDetails []types.TopicDetails
	for result := range resultChan {
		topicDetails = append(topicDetails, result)
	}

	slog.Info("retrieved topic details via AWS API", "count", len(topicDetails))
	return topicDetails, nil
}

// The topic configs are encoded in base64 when returned by the `DescribeTopic` API.
func decodeTopicConfigs(encodedConfigs *string) (map[string]*string, error) {
	if encodedConfigs == nil || *encodedConfigs == "" {
		return make(map[string]*string), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(*encodedConfigs)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 configs: %w", err)
	}

	var configMap map[string]string
	if err := json.Unmarshal(decoded, &configMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal configs JSON: %w", err)
	}

	configurations := make(map[string]*string)
	for key, value := range configMap {
		v := value
		configurations[key] = &v
	}

	return configurations, nil
}
