package metrics

import (
	"context"
	"fmt"
	"log/slog"
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

	brokerType := getBrokerType(instanceType)

	metricsMetadata := types.MetricMetadata{
		ClusterType:          string(cluster.ClusterType),
		BrokerAzDistribution: *brokerAZDistribution,
		NumberOfBrokerNodes:  numberOfBrokerNodes,
		KafkaVersion:         kafkaVersion,
		EnhancedMonitoring:   enhancedMonitoring,
		StartDate:            timeWindow.StartTime,
		EndDate:              timeWindow.EndTime,
		Period:               timeWindow.Period,

		FollowerFetching: followerFetching,
		InstanceType:     instanceType,
		TieredStorage:    tieredStorage,
		BrokerType:       brokerType,
	}

	brokerQueries := ms.buildBrokerMetricQueries(*cluster.ClusterName, timeWindow.Period)
	brokerQueryResult, err := ms.executeMetricQuery(ctx, brokerQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}
	clientConnectionQueries := ms.buildClientConnectionQueries(*cluster.ClusterName, timeWindow.Period)
	clientConnectionQueryResult, err := ms.executeMetricQuery(ctx, clientConnectionQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	clusterQueries := ms.buildClusterMetricQueries(*cluster.ClusterName, timeWindow.Period)
	clusterQueryResult, err := ms.executeMetricQuery(ctx, clusterQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	// for express brokers there is no storage info
	if brokerType == types.BrokerTypeExpress {
		return &types.ClusterMetrics{
			MetricMetadata: metricsMetadata,
			Results:        append(brokerQueryResult.MetricDataResults, clusterQueryResult.MetricDataResults...),
		}, nil
	}

	clusterVolumeSizeGB := int(*cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
	localStorageQuery := ms.buildLocalStorageUsageQuery(*cluster.ClusterName, timeWindow.Period, clusterVolumeSizeGB)
	storageQueryResult, err := ms.executeMetricQuery(ctx, localStorageQuery, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	remoteStorageQuery := ms.buildRemoteStorageUsageQuery(*cluster.ClusterName, timeWindow.Period)
	remoteStorageQueryResult, err := ms.executeMetricQuery(ctx, remoteStorageQuery, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	// Combine broker and cluster metric results
	combinedResults := append(brokerQueryResult.MetricDataResults, clusterQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, clientConnectionQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, storageQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, remoteStorageQueryResult.MetricDataResults...)

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
		ClusterType: string(cluster.ClusterType),
		StartDate:   timeWindow.StartTime,
		EndDate:     timeWindow.EndTime,
		Period:      timeWindow.Period,
	}


	// Build metric queries for all topics with aggregation
	queries := ms.buildServerlessMetricQueries(*cluster.ClusterName, timeWindow.Period)

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
func (ms *MetricService) buildBrokerMetricQueries(clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	metricStatMap := map[string]string{
		"BytesInPerSec":      "Average",
		"BytesOutPerSec":     "Average",
		"MessagesInPerSec":   "Average",
		"PartitionCount":     "Maximum",
	}

	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', '%s', %d)"

	var queries []cloudwatchtypes.MetricDataQuery
	for metricName, metricStat := range metricStatMap {
		searchID := fmt.Sprintf("m_%s", strings.ToLower(metricName))
		sumID := fmt.Sprintf("sum_%s", strings.ToLower(metricName))
		searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, metricStat, period)
		queries = append(queries,
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(searchID),
				Expression: aws.String(searchExpr),
				ReturnData: aws.Bool(false),
			},
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(sumID),
				Expression: aws.String(fmt.Sprintf("SUM(%s)", searchID)),
				Label:      aws.String(fmt.Sprintf("%s", metricName)),
				ReturnData: aws.Bool(true),
			},
		)
	}
	return queries
}

func (ms *MetricService) buildClientConnectionQueries(clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {

	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\",\"Client Authentication\"} MetricName=\"ClientConnectionCount\" \"Cluster Name\"=\"%s\"', '%s', %d)"

	searchExprMax := fmt.Sprintf(
		searchTemplate,
		clusterName, "Maximum", period,
	)
	searchExprAvg := fmt.Sprintf(
		searchTemplate,
		clusterName, "Average", period,
	)
	return []cloudwatchtypes.MetricDataQuery{
		{
			Id:         aws.String("max_all"),
			Expression: aws.String(searchExprMax),
			ReturnData: aws.Bool(false),
		},
		{
			Id:         aws.String("sum_max"),
			Expression: aws.String("SUM(max_all)"),
			Label:      aws.String("ClientConnectionCount (Maximum)"),
			ReturnData: aws.Bool(true),
		},
		{
			Id:         aws.String("avg_all"),
			Expression: aws.String(searchExprAvg),
			ReturnData: aws.Bool(false),
		},
		{
			Id:         aws.String("sum_avg"),
			Expression: aws.String("SUM(avg_all)"),
			Label:      aws.String("ClientConnectionCount (Average)"),
			ReturnData: aws.Bool(true),
		},
	}
}

func (ms *MetricService) buildLocalStorageUsageQuery(clusterName string, period int32, volumeSizeGB int) []cloudwatchtypes.MetricDataQuery {
	metricName := "KafkaDataLogsDiskUsed"
	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', 'Maximum', %d)"
	searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, period)
	return []cloudwatchtypes.MetricDataQuery{
		{
			Id:         aws.String("m_local_disk"),
			Expression: aws.String(searchExpr),
			ReturnData: aws.Bool(false),
		},
		{
			Id:         aws.String("e_local_gb"),
			Expression: aws.String(fmt.Sprintf("((m_local_disk / 100) * %d)", volumeSizeGB)),
			ReturnData: aws.Bool(false),
		},
		{
			Id:         aws.String("e_total_local_storage_usage_gb"),
			Expression: aws.String("SUM(e_local_gb)"),
			Label:      aws.String("TotalLocalStorageUsage(GB)"),
			ReturnData: aws.Bool(true),
		},
	}
}

func (ms *MetricService) buildRemoteStorageUsageQuery(clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	metricName := "RemoteLogSizeBytes"
	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', 'Maximum', %d)"
	searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, period)
	const bytesPerGB = 1073741824
	return []cloudwatchtypes.MetricDataQuery{
		{
			Id:         aws.String("m_remote"),
			Expression: aws.String(searchExpr),
			ReturnData: aws.Bool(false),
		},
		{
			Id:         aws.String("e_remote_gb"),
			Expression: aws.String(fmt.Sprintf("(m_remote / %d)", bytesPerGB)),
			ReturnData: aws.Bool(false),
		},
		{
			Id:         aws.String("e_total_remote_storage_usage_gb"),
			Expression: aws.String("SUM(e_remote_gb)"),
			Label:      aws.String("TotalRemoteStorageUsage(GB)"),
			ReturnData: aws.Bool(true),
		},
	}
}

func (ms *MetricService) buildClusterMetricQueries(clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {

	metricName := "GlobalPartitionCount"

	var queries []cloudwatchtypes.MetricDataQuery
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
			Stat:   aws.String("Maximum"),
		},
		ReturnData: aws.Bool(true),
	})
	return queries
}

func (ms *MetricService) buildServerlessMetricQueries(clusterName string, period int32) []cloudwatchtypes.MetricDataQuery {
	metrics := []string{
		"BytesInPerSec",
		"BytesOutPerSec",
		"MessagesInPerSec",
	}

	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Topic\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', 'Average', %d)"

	var queries []cloudwatchtypes.MetricDataQuery
	for _, metricName := range metrics {
		searchID := fmt.Sprintf("m_%s", strings.ToLower(metricName))
		sumID := fmt.Sprintf("sum_%s", strings.ToLower(metricName))
		searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, period)
		queries = append(queries,
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(searchID),
				Expression: aws.String(searchExpr),
				ReturnData: aws.Bool(false),
			},
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(sumID),
				Expression: aws.String(fmt.Sprintf("SUM(%s)", searchID)),
				Label:      aws.String(fmt.Sprintf("%s", metricName)),
				ReturnData: aws.Bool(true),
			},
		)
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

func getBrokerType(instanceType string) types.BrokerType {
	if strings.HasPrefix(instanceType, "express.") {
		return types.BrokerTypeExpress
	}
	return types.BrokerTypeStandard
}
