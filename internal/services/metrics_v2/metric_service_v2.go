package metrics_v2

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

const (
	// testing using daily periond with 7 days of data
	DailyPeriodSeconds int32 = 60 * 60 * 24 // 60 seconds * 60 minutes * 24 hours
	// we will want to use monthly period with 12 months of data
	MonthlyPeriodSeconds int32 = 60 * 60 * 24 * 30 // 60 seconds * 60 minutes * 24 hours * 30 days

	// debugging period
	TwoHoursPeriodSeconds int32 = 60 * 60 * 2 // 60 seconds * 60 minutes * 2 hours
)

var Metrics = []string{
	"BytesInPerSec",
	"BytesOutPerSec",
	"MessagesInPerSec",
	"KafkaDataLogsDiskUsed",
	"RemoteLogSizeBytes",
}

type MetricServiceV2 struct {
	client *cloudwatch.Client
}

func NewMetricServiceV2(client *cloudwatch.Client) *MetricServiceV2 {
	return &MetricServiceV2{client: client}
}

// ProcessProvisionedCluster processes metrics for provisioned aggregated across all brokers in a cluster
func (ms *MetricServiceV2) ProcessProvisionedCluster(ctx context.Context, cluster kafkatypes.Cluster, startTime time.Time, endTime time.Time) (*types.ClusterMetricsV2, error) {
	period := DailyPeriodSeconds
	slog.Info("🏗️ processing provisioned cluster", "cluster", *cluster.ClusterName, "startDate", startTime, "endDate", endTime)
	authentication, err := utils.StructToMap(cluster.Provisioned.ClientAuthentication)
	if err != nil {
		return nil, fmt.Errorf("failed to convert provisioned client authentication to map: %w", err)
	}
	if authentication == nil {
		return nil, fmt.Errorf("provisioned client authentication is nil")
	}

	globalMetrics, err := ms.getGlobalMetrics(ctx, *cluster.ClusterName, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to get global metrics: %v", err)
	}

	// todo how to use this?
	_ = globalMetrics

	brokerAZDistribution := aws.String(string(cluster.Provisioned.BrokerNodeGroupInfo.BrokerAZDistribution))
	kafkaVersion := aws.ToString(cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	enhancedMonitoring := string(cluster.Provisioned.EnhancedMonitoring)
	numberOfBrokerNodes := int(*cluster.Provisioned.NumberOfBrokerNodes)

	metricsMetadata := types.MetricMetadata{
		ClusterType:          string(cluster.ClusterType),
		BrokerAzDistribution: *brokerAZDistribution,
		KafkaVersion:         kafkaVersion,
		EnhancedMonitoring:   enhancedMonitoring,
		StartWindowDate:      startTime.Format(time.RFC3339),
		EndWindowDate:        endTime.Format(time.RFC3339),
		Period:               period,
	}

	queries := ms.buildMetricQueries(numberOfBrokerNodes, *cluster.ClusterName, period)

	queryResult, err := ms.executeMetricQuery(ctx, queries, startTime, endTime)
	if err != nil {
		return nil, err
	}

	clusterMetrics := types.ClusterMetricsV2{
		MetricMetadata: metricsMetadata,
		Results:        queryResult.MetricDataResults,
	}

	return &clusterMetrics, nil
}

// ProcessServerlessCluster processes metrics for serverless aggregated across all topics in a cluster
func (ms *MetricServiceV2) ProcessServerlessCluster(ctx context.Context, cluster kafkatypes.Cluster, startTime time.Time, endTime time.Time) (*types.ClusterMetricsV2, error) {
	period := TwoHoursPeriodSeconds
	// temp to access data
	endTime = endTime.AddDate(0, 0, 1)
	slog.Info("☁️ processing serverless cluster with topic aggregation", "cluster", *cluster.ClusterName, "startDate", startTime, "endDate", endTime)

	authentication, err := utils.StructToMap(cluster.Serverless.ClientAuthentication)
	if err != nil {
		return nil, fmt.Errorf("failed to convert serverless client authentication to map: %w", err)
	}
	if authentication == nil {
		return nil, fmt.Errorf("serverless client authentication is nil")
	}

	globalMetrics, err := ms.getGlobalMetrics(ctx, *cluster.ClusterName, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to get global metrics: %v", err)
	}

	// TODO: do we want this?
	_ = globalMetrics

	metricsMetadata := types.MetricMetadata{
		ClusterType:     string(cluster.ClusterType),
		StartWindowDate: startTime.Format(time.RFC3339),
		EndWindowDate:   endTime.Format(time.RFC3339),
		Period:          period,
	}

	// Get all topics for this cluster
	topics, err := ms.getTopicsForCluster(ctx, *cluster.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to get topics for cluster: %w", err)
	}

	if len(topics) == 0 {
		slog.Info("No topics found for serverless cluster", "cluster", *cluster.ClusterName)
		return &types.ClusterMetricsV2{
			MetricMetadata: metricsMetadata,
			Results:        []cloudwatchtypes.MetricDataResult{},
		}, nil
	}

	// Build metric queries for all topics with aggregation
	queries := ms.buildServerlessMetricQueries(topics, *cluster.ClusterName, period)

	// Execute the metric query
	queryResult, err := ms.executeMetricQuery(ctx, queries, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to execute serverless metric queries: %w", err)
	}

	clusterMetrics := types.ClusterMetricsV2{
		MetricMetadata: metricsMetadata,
		Results:        queryResult.MetricDataResults,
	}

	return &clusterMetrics, nil
}

// Private Helper Functions - Query Building

func (ms *MetricServiceV2) buildMetricQueries(brokers int, clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	var queries []cloudwatchtypes.MetricDataQuery

	for metricIndex, metricName := range Metrics {
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

func (ms *MetricServiceV2) buildServerlessMetricQueries(topics []string, clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	var queries []cloudwatchtypes.MetricDataQuery

	for metricIndex, metricName := range Metrics {
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

func (ms *MetricServiceV2) executeMetricQuery(ctx context.Context, queries []cloudwatchtypes.MetricDataQuery, startTime, endTime time.Time) (*cloudwatch.GetMetricDataOutput, error) {
	input := &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(startTime),
		EndTime:           aws.Time(endTime),
	}

	result, err := ms.client.GetMetricData(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get metric data: %w", err)
	}

	return result, nil
}

// Private Helper Functions - Topic Discovery

func (ms *MetricServiceV2) getTopicsForCluster(ctx context.Context, clusterName string) ([]string, error) {
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

	slog.Info("Found topics for serverless cluster", "cluster", clusterName, "topicCount", len(topicList))
	for _, topic := range topicList {
		slog.Info("Topic", "topic", topic)
	}
	return topicList, nil
}

// Private Helper Functions - Global Metrics

func (ms *MetricServiceV2) getGlobalMetrics(ctx context.Context, clusterName string, startTime time.Time, endTime time.Time) (*types.GlobalMetrics, error) {
	globalMetrics := types.GlobalMetrics{}

	globalMetricAssignments := []struct {
		metricName  string
		targetField *float64
	}{
		{"GlobalPartitionCount", &globalMetrics.GlobalPartitionCountMax},
		{"GlobalTopicCount", &globalMetrics.GlobalTopicCountMax},
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(globalMetricAssignments))

	for _, assignment := range globalMetricAssignments {
		wg.Add(1)
		go func(assignment struct {
			metricName  string
			targetField *float64
		}) {
			defer wg.Done()
			metricValue, err := ms.getGlobalMetric(ctx, clusterName, assignment.metricName, startTime, endTime)
			if err != nil {
				errChan <- fmt.Errorf("failed to get metric %s: %v", assignment.metricName, err)
				return
			}
			*assignment.targetField = metricValue
		}(assignment)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	return &globalMetrics, nil
}

func (ms *MetricServiceV2) getGlobalMetric(ctx context.Context, clusterName string, metricName string, startTime time.Time, endTime time.Time) (float64, error) {
	slog.Info("📊 getting global metric", "cluster", clusterName, "metric", metricName)
	metricRequest := ms.buildCloudWatchInputGlobalMetrics(clusterName, metricName, []cloudwatchtypes.Statistic{cloudwatchtypes.StatisticMaximum}, startTime, endTime)
	response, err := ms.client.GetMetricStatistics(ctx, metricRequest)
	if err != nil {
		return 0, fmt.Errorf("failed to get metric statistics: %v", err)
	}
	if len(response.Datapoints) == 0 {
		slog.Info("🔍 No data points found for global metric", "cluster", clusterName, "metric", metricName)
		return 0, nil
	}
	slog.Info("🔍 got global metric", "cluster", clusterName, "metric", metricName, "data", response.Datapoints[0].Maximum)
	return *response.Datapoints[0].Maximum, nil
}

// Private Helper Functions - CloudWatch Helpers

func (ms *MetricServiceV2) buildCloudWatchInputGlobalMetrics(clusterName, metricName string, statistics []cloudwatchtypes.Statistic, startTime time.Time, endTime time.Time) *cloudwatch.GetMetricStatisticsInput {
	// Use monthly period for consistent monthly data points
	period := int32(DailyPeriodSeconds)

	dimensions := []cloudwatchtypes.Dimension{
		{
			Name:  aws.String("Cluster Name"),
			Value: aws.String(clusterName),
		},
	}

	return &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/Kafka"),
		MetricName: aws.String(metricName),
		Dimensions: dimensions,
		StartTime:  aws.Time(startTime),
		EndTime:    aws.Time(endTime),
		Period:     aws.Int32(period),
		Statistics: statistics,
	}
}
