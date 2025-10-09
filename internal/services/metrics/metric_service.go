package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

type MetricService struct {
	client *cloudwatch.Client
}

func NewMetricService(client *cloudwatch.Client) *MetricService {
	return &MetricService{client: client}
}

// ProcessProvisionedCluster processes metrics for provisioned aggregated across all brokers in a cluster
func (ms *MetricService) ProcessProvisionedCluster(ctx context.Context, cluster kafkatypes.Cluster, followerFetching bool, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error) {
	slog.Info("🏗️ processing provisioned cluster", "cluster", *cluster.ClusterName, "startDate", timeWindow.StartTime, "endDate", timeWindow.EndTime)

	if cluster.Provisioned == nil {
		return nil, fmt.Errorf("cluster %s has no provisioned configuration", aws.ToString(cluster.ClusterName))
	}

	brokerAZDistribution := aws.String(string(cluster.Provisioned.BrokerNodeGroupInfo.BrokerAZDistribution))
	kafkaVersion := aws.ToString(cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	enhancedMonitoring := string(cluster.Provisioned.EnhancedMonitoring)
	numberOfBrokerNodes := int(*cluster.Provisioned.NumberOfBrokerNodes)
	instanceType := aws.ToString(cluster.Provisioned.BrokerNodeGroupInfo.InstanceType)
	tieredStorage := cluster.Provisioned.StorageMode == kafkatypes.StorageModeTiered

	metricsMetadata := types.MetricMetadata{
		ClusterType:          string(cluster.ClusterType),
		BrokerAzDistribution: *brokerAZDistribution,
		KafkaVersion:         kafkaVersion,
		EnhancedMonitoring:   enhancedMonitoring,
		StartWindowDate:      timeWindow.StartTime.Format(time.RFC3339),
		EndWindowDate:        timeWindow.EndTime.Format(time.RFC3339),
		Period:               timeWindow.Period,

		FollowerFetching: followerFetching,
		InstanceType:     instanceType,
		TieredStorage:    tieredStorage,
	}

	brokerQueries := ms.buildBrokerMetricQueries(numberOfBrokerNodes, *cluster.ClusterName, timeWindow.Period)
	brokerQueryResult, err := ms.executeMetricQuery(ctx, brokerQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	clusterQueries := ms.buildClusterMetricQueries(*cluster.ClusterName, timeWindow.Period)
	clusterQueryResult, err := ms.executeMetricQuery(ctx, clusterQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	clusterVolumeSizeGB := int(*cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
	storageQuery := ms.buildStorageUsageQuery(numberOfBrokerNodes, *cluster.ClusterName, timeWindow.Period, clusterVolumeSizeGB)
	storageQueryResult, err := ms.executeMetricQuery(ctx, storageQuery, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	// Combine broker and cluster metric results
	combinedResults := append(brokerQueryResult.MetricDataResults, clusterQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, storageQueryResult.MetricDataResults...)

	clusterMetrics := types.ClusterMetrics{
		MetricMetadata: metricsMetadata,
		Results:        combinedResults,
	}

	return &clusterMetrics, nil
}

// ProcessServerlessCluster processes metrics for serverless aggregated across all topics in a cluster
func (ms *MetricService) ProcessServerlessCluster(ctx context.Context, cluster kafkatypes.Cluster, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error) {
	slog.Info("☁️ processing serverless cluster with topic aggregation", "cluster", *cluster.ClusterName, "startDate", timeWindow.StartTime, "endDate", timeWindow.EndTime)

	if cluster.Serverless == nil {
		return nil, fmt.Errorf("cluster %s has no serverless configuration", aws.ToString(cluster.ClusterName))
	}

	metricsMetadata := types.MetricMetadata{
		ClusterType:     string(cluster.ClusterType),
		StartWindowDate: timeWindow.StartTime.Format(time.RFC3339),
		EndWindowDate:   timeWindow.EndTime.Format(time.RFC3339),
		Period:          timeWindow.Period,
	}

	// Get all topics for this cluster
	topics, err := ms.getTopicsForCluster(ctx, *cluster.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to get topics for cluster: %w", err)
	}

	if len(topics) == 0 {
		slog.Info("No topics found for serverless cluster", "cluster", *cluster.ClusterName)
		return &types.ClusterMetrics{
			MetricMetadata: metricsMetadata,
			Results:        []cloudwatchtypes.MetricDataResult{},
		}, nil
	}

	// Build metric queries for all topics with aggregation
	queries := ms.buildServerlessMetricQueries(topics, *cluster.ClusterName, timeWindow.Period)

	// Execute the metric query
	queryResult, err := ms.executeMetricQuery(ctx, queries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, fmt.Errorf("failed to execute serverless metric queries: %w", err)
	}

	clusterMetrics := types.ClusterMetrics{
		MetricMetadata: metricsMetadata,
		Results:        queryResult.MetricDataResults,
	}

	return &clusterMetrics, nil
}

// Private Helper Functions - Query Building

func (ms *MetricService) buildBrokerMetricQueries(brokers int, clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	var metrics = []string{
		"BytesInPerSec",
		"BytesOutPerSec",
		"MessagesInPerSec",
		"RemoteLogSizeBytes",
		"PartitionCount",
		"ClientConnectionCount",
	}

	var queries []cloudwatchtypes.MetricDataQuery

	for metricIndex, metricName := range metrics {
		var metricIDs []string

		// brokerIDs are 1-indexed
		for brokerID := 1; brokerID <= brokers; brokerID++ {
			metricID := fmt.Sprintf("m_%s_%d", strings.ToLower(metricName), brokerID)
			metricIDs = append(metricIDs, metricID)

			queries = append(queries, cloudwatchtypes.MetricDataQuery{
				Id: aws.String(metricID),
				MetricStat: &cloudwatchtypes.MetricStat{
					Metric: &cloudwatchtypes.Metric{
						Namespace:  aws.String("AWS/Kafka"),
						MetricName: aws.String(metricName),
						Dimensions: []cloudwatchtypes.Dimension{
							{
								Name:  aws.String("Cluster Name"),
								Value: aws.String(clusterName),
							},
							{
								Name:  aws.String("Broker ID"),
								Value: aws.String(strconv.Itoa(brokerID)),
							},
						},
					},
					Period: aws.Int32(period),
					Stat:   aws.String("Average"),
				},
				ReturnData: aws.Bool(false),
			})
		}

		expressionID := fmt.Sprintf("e%d", metricIndex+1)

		queries = append(queries, cloudwatchtypes.MetricDataQuery{
			Id:         aws.String(expressionID),
			Expression: aws.String(fmt.Sprintf("SUM([%s])", strings.Join(metricIDs, ", "))),
			Label:      aws.String(fmt.Sprintf("Cluster Aggregate - %s", metricName)),
			ReturnData: aws.Bool(true),
		})
	}

	return queries
}

func (ms *MetricService) buildStorageUsageQuery(brokers int, clusterName string, period int32, volumeSizeGB int) []cloudwatchtypes.MetricDataQuery {
	var queries []cloudwatchtypes.MetricDataQuery
	var metricIDs []string

	// Create individual queries for KafkaDataLogsDiskUsed for each broker
	for brokerID := 1; brokerID <= brokers; brokerID++ {
		metricID := fmt.Sprintf("m_%d", brokerID)
		metricIDs = append(metricIDs, metricID)

		queries = append(queries, cloudwatchtypes.MetricDataQuery{
			Id: aws.String(metricID),
			MetricStat: &cloudwatchtypes.MetricStat{
				Metric: &cloudwatchtypes.Metric{
					Namespace:  aws.String("AWS/Kafka"),
					MetricName: aws.String("KafkaDataLogsDiskUsed"),
					Dimensions: []cloudwatchtypes.Dimension{
						{
							Name:  aws.String("Cluster Name"),
							Value: aws.String(clusterName),
						},
						{
							Name:  aws.String("Broker ID"),
							Value: aws.String(strconv.Itoa(brokerID)),
						},
					},
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Average"),
			},
			ReturnData: aws.Bool(false),
		})
	}

	// Create expression to calculate total local storage usage in GB
	// Formula: ((m_1 / 100) * volumeSizeGB) + ((m_2 / 100) * volumeSizeGB) + ((m_3 / 100) * volumeSizeGB)
	var expressionParts []string
	for _, metricID := range metricIDs {
		expressionParts = append(expressionParts, fmt.Sprintf("((%s / 100) * %d)", metricID, volumeSizeGB))
	}

	expression := strings.Join(expressionParts, " + ")

	queries = append(queries, cloudwatchtypes.MetricDataQuery{
		Id:         aws.String("e_total_local_storage_usage_gb"),
		Expression: aws.String(expression),
		Label:      aws.String("Cluster Aggregate - Total Local Storage Usage GB"),
		ReturnData: aws.Bool(true),
	})

	return queries
}

func (ms *MetricService) buildClusterMetricQueries(clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	var metrics = []string{
		"GlobalPartitionCount",
	}

	var queries []cloudwatchtypes.MetricDataQuery

	for _, metricName := range metrics {
		metricID := fmt.Sprintf("m_%s", strings.ToLower(metricName))
		queries = append(queries, cloudwatchtypes.MetricDataQuery{
			Id: aws.String(metricID),
			MetricStat: &cloudwatchtypes.MetricStat{
				Metric: &cloudwatchtypes.Metric{
					Namespace:  aws.String("AWS/Kafka"),
					MetricName: aws.String(metricName),
					Dimensions: []cloudwatchtypes.Dimension{
						{
							Name:  aws.String("Cluster Name"),
							Value: aws.String(clusterName),
						},
					},
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Average"),
			},
			ReturnData: aws.Bool(true),
		})
	}

	return queries
}

func (ms *MetricService) buildServerlessMetricQueries(topics []string, clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	var metrics = []string{
		"BytesInPerSec",
		"BytesOutPerSec",
		"MessagesInPerSec",
	}

	var queries []cloudwatchtypes.MetricDataQuery

	for metricIndex, metricName := range metrics {
		var metricIDs []string

		// Create individual queries for each topic
		for topicIndex, topic := range topics {
			metricID := fmt.Sprintf("m_%s_topic_%d", strings.ToLower(metricName), topicIndex)
			metricIDs = append(metricIDs, metricID)

			queries = append(queries, cloudwatchtypes.MetricDataQuery{
				Id: aws.String(metricID),
				MetricStat: &cloudwatchtypes.MetricStat{
					Metric: &cloudwatchtypes.Metric{
						Namespace:  aws.String("AWS/Kafka"),
						MetricName: aws.String(metricName),
						Dimensions: []cloudwatchtypes.Dimension{
							{
								Name:  aws.String("Cluster Name"),
								Value: aws.String(clusterName),
							},
							{
								Name:  aws.String("Topic"),
								Value: aws.String(topic),
							},
						},
					},
					Period: aws.Int32(period),
					Stat:   aws.String("Average"),
				},
				ReturnData: aws.Bool(false),
			})
		}

		// Create aggregation expression that sums all topic metrics for this metric type
		expressionID := fmt.Sprintf("e%d", metricIndex+1)
		queries = append(queries, cloudwatchtypes.MetricDataQuery{
			Id:         aws.String(expressionID),
			Expression: aws.String(fmt.Sprintf("SUM([%s])", strings.Join(metricIDs, ", "))),
			Label:      aws.String(fmt.Sprintf("Cluster Aggregate - %s", metricName)),
			ReturnData: aws.Bool(true),
		})
	}

	return queries
}

// Private Helper Functions - Query Execution

func (ms *MetricService) executeMetricQuery(ctx context.Context, queries []cloudwatchtypes.MetricDataQuery, startTime, endTime time.Time) (*cloudwatch.GetMetricDataOutput, error) {
	input := &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(startTime),
		EndTime:           aws.Time(endTime),
	}

	var allResults []cloudwatchtypes.MetricDataResult
	var nextToken *string

	for {
		input.NextToken = nextToken
		result, err := ms.client.GetMetricData(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to get metric data: %w", err)
		}

		allResults = append(allResults, result.MetricDataResults...)

		if result.NextToken == nil {
			break
		}
		nextToken = result.NextToken
	}

	// Return a consolidated result with all metric data
	return &cloudwatch.GetMetricDataOutput{
		MetricDataResults: allResults,
	}, nil
}

// Private Helper Functions - Topic Discovery

func (ms *MetricService) getTopicsForCluster(ctx context.Context, clusterName string) ([]string, error) {
	topics := make(map[string]struct{})

	// Use BytesInPerSec as a representative metric to find all topics
	listMetricsInput := &cloudwatch.ListMetricsInput{
		Namespace:  aws.String("AWS/Kafka"),
		MetricName: aws.String("BytesInPerSec"),
		Dimensions: []cloudwatchtypes.DimensionFilter{
			{
				Name:  aws.String("Cluster Name"),
				Value: aws.String(clusterName),
			},
		},
	}

	var nextToken *string
	for {
		listMetricsInput.NextToken = nextToken
		output, err := ms.client.ListMetrics(ctx, listMetricsInput)
		if err != nil {
			return nil, fmt.Errorf("failed to list metrics: %w", err)
		}

		for _, metric := range output.Metrics {
			for _, dim := range metric.Dimensions {
				if *dim.Name == "Topic" {
					topics[*dim.Value] = struct{}{}
					break
				}
			}
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	topicList := make([]string, 0, len(topics))
	for topic := range topics {
		topicList = append(topicList, topic)
	}
	return topicList, nil
}
