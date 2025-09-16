package metrics_v2

import (
	"context"
	"encoding/json"
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
	DailyPeriodSeconds = 60 * 60 * 24 // 60 seconds * 60 minutes * 24 hours
	// we will want to use monthly period with 12 months of data
	MonthlyPeriodSeconds = 60 * 60 * 24 * 30 // 60 seconds * 60 minutes * 24 hours * 30 days
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

func (ms *MetricServiceV2) ProcessProvisionedCluster(ctx context.Context, cluster kafkatypes.Cluster, startTime time.Time, endTime time.Time) (*types.ClusterMetricsV2, error) {
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
		Period:               DailyPeriodSeconds,
	}

	queries := ms.buildMetricQueries(numberOfBrokerNodes, *cluster.ClusterName)

	queryResult, err := ms.executeMetricQuery(ctx, queries, startTime, endTime)
	if err != nil {
		return nil, err
	}

	clusterMetrics := types.ClusterMetricsV2{
		MetricMetadata: metricsMetadata,
		Results: types.ResultWrapper{
			Provisioned: &types.ProvisionedResult{
				Results: queryResult.MetricDataResults,
			},
		},
	}

	return &clusterMetrics, nil
}

func (ms *MetricServiceV2) ProcessServerlessCluster(ctx context.Context, cluster kafkatypes.Cluster, startTime time.Time, endTime time.Time) (*types.ClusterMetricsV2, error) {
	slog.Info("☁️ processing serverless cluster", "cluster", *cluster.ClusterName)
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

	ms.processServerlessNode(ctx, *cluster.ClusterName, startTime, endTime)

	// todo how to use this?
	_ = globalMetrics

	metricsMetadata := types.MetricMetadata{
		ClusterType:     string(cluster.ClusterType),
		StartWindowDate: startTime.Format(time.RFC3339),
		EndWindowDate:   endTime.Format(time.RFC3339),
		Period:          DailyPeriodSeconds,
	}

	// this wont work for serverless clusters as we need to get the metrics for the topics
	// queries := ms.buildMetricQueries(1, *cluster.ClusterName)

	// queryResult, err := ms.executeMetricQuery(ctx, queries, startTime, endTime)
	// if err != nil {
	// 	return nil, err
	// }

	// _ = queryResult

	clusterMetrics := types.ClusterMetricsV2{
		MetricMetadata: metricsMetadata,
		Results: types.ResultWrapper{
			Serverless: &types.ServerlessResult{},
		},
	}

	// nodesMetrics := []types.NodeMetrics{}
	// // serverless has 1 broker node
	// nodeMetric, err := ms.processServerlessNode(ctx, *cluster.ClusterName, startTime, endTime)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to process node: %v", err)
	// }

	// nodesMetrics = append(nodesMetrics, *nodeMetric)

	// clusterMetricsSummary := ms.calculateClusterMetricsSummary(nodesMetrics)
	// clusterMetricsSummary.InstanceType = nil

	// clusterMetricsSummary.TieredStorage = aws.Bool(false)

	// clusterMetricsSummary.Partitions = aws.Float64(float64(globalMetrics.GlobalPartitionCountMax))
	// clusterMetricsSummary.ReplicationFactor = ms.calculateReplicationFactor(nodesMetrics, globalMetrics.GlobalPartitionCountMax)

	// clusterMetric := types.NewClusterMetrics("something-for-now", time.Now())
	// clusterMetric.ClusterArn = *cluster.ClusterArn
	// clusterMetric.StartDate = startTime
	// clusterMetric.EndDate = endTime
	// clusterMetric.ClusterName = *cluster.ClusterName
	// clusterMetric.ClusterType = string(cluster.ClusterType)
	// clusterMetric.Authentication = authentication
	// clusterMetric.NodesMetrics = nodesMetrics
	// clusterMetric.ClusterMetricsSummary = clusterMetricsSummary
	// clusterMetric.GlobalMetrics = *globalMetrics

	return &clusterMetrics, nil
}

func (ms *MetricServiceV2) buildMetricQueries(brokers int, clusterName string) []cloudwatchtypes.MetricDataQuery {
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
					Period: aws.Int32(DailyPeriodSeconds),
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

func (ms *MetricServiceV2) processServerlessNode(ctx context.Context, clusterName string, startTime time.Time, endTime time.Time) (*types.NodeMetrics, error) {
	slog.Info("☁️ processing serverless node", "cluster", clusterName)
	nodeMetric := types.NodeMetrics{
		NodeID:       1,
		InstanceType: nil,
		VolumeSizeGB: nil,
	}

	averageMetricAssignments := []struct {
		metricName  string
		targetField *float64
	}{
		{"BytesInPerSec", &nodeMetric.BytesInPerSecAvg},
		{"BytesOutPerSec", &nodeMetric.BytesOutPerSecAvg},
		{"MessagesInPerSec", &nodeMetric.MessagesInPerSecAvg},
		{"KafkaDataLogsDiskUsed", &nodeMetric.KafkaDataLogsDiskUsedAvg},
		{"RemoteLogSizeBytes", &nodeMetric.RemoteLogSizeBytesAvg},
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(averageMetricAssignments))

	for _, assignment := range averageMetricAssignments {
		wg.Add(1)
		go func(assignment struct {
			metricName  string
			targetField *float64
		}) {
			defer wg.Done()
			metricValue, err := ms.getServerlessMetric(ctx, clusterName, assignment.metricName, cloudwatchtypes.StatisticAverage, startTime, endTime)
			if err != nil {
				errChan <- fmt.Errorf("failed to get metric %s: %v", assignment.metricName, err)
				return
			}
			*assignment.targetField = metricValue
		}(assignment)
	}

	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	return &nodeMetric, nil
}

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

// func (ms *MetricServiceV2) processProvisionedNode(ctx context.Context, clusterName string, nodeID int, instanceType string, startTime time.Time, endTime time.Time) (*types.NodeMetrics, error) {
// 	slog.Info("🏗️ processing provisioned node", "cluster", clusterName, "node", nodeID)
// 	nodeMetric := types.NodeMetrics{
// 		NodeID:       nodeID,
// 		InstanceType: &instanceType,
// 		VolumeSizeGB: nil,
// 	}

// 	averageMetricAssignments := []struct {
// 		metricName  string
// 		targetField *float64
// 	}{
// 		{"BytesInPerSec", &nodeMetric.BytesInPerSecAvg},
// 		{"BytesOutPerSec", &nodeMetric.BytesOutPerSecAvg},
// 		{"MessagesInPerSec", &nodeMetric.MessagesInPerSecAvg},
// 		{"KafkaDataLogsDiskUsed", &nodeMetric.KafkaDataLogsDiskUsedAvg},
// 		{"RemoteLogSizeBytes", &nodeMetric.RemoteLogSizeBytesAvg},
// 	}

// 	var wg sync.WaitGroup
// 	errChan := make(chan error, len(averageMetricAssignments))

// 	for _, assignment := range averageMetricAssignments {
// 		wg.Add(1)
// 		go func(assignment struct {
// 			metricName  string
// 			targetField *float64
// 		}) {
// 			defer wg.Done()
// 			metricValue, err := ms.getAverageMetric(ctx, clusterName, assignment.metricName, &nodeID, startTime, endTime)
// 			if err != nil {
// 				errChan <- fmt.Errorf("failed to get metric %s: %v", assignment.metricName, err)
// 				return
// 			}
// 			*assignment.targetField = metricValue
// 		}(assignment)
// 	}

// 	wg.Wait()
// 	close(errChan)

// 	// Check for any errors
// 	for err := range errChan {
// 		if err != nil {
// 			return nil, err
// 		}
// 	}

// 	peakMetricsAssignments := []struct {
// 		metricName  string
// 		targetField *float64
// 	}{
// 		{"BytesInPerSec", &nodeMetric.BytesInPerSecMax},
// 		{"BytesOutPerSec", &nodeMetric.BytesOutPerSecMax},
// 		{"MessagesInPerSec", &nodeMetric.MessagesInPerSecMax},
// 		{"KafkaDataLogsDiskUsed", &nodeMetric.KafkaDataLogsDiskUsedMax},
// 		{"RemoteLogSizeBytes", &nodeMetric.RemoteLogSizeBytesMax},
// 		{"ClientConnectionCount", &nodeMetric.ClientConnectionCountMax},
// 		{"PartitionCount", &nodeMetric.PartitionCountMax},
// 		// {"GlobalTopicCount", &nodeMetric.GlobalTopicCountMax},
// 		{"LeaderCount", &nodeMetric.LeaderCountMax},
// 		{"ReplicationBytesOutPerSec", &nodeMetric.ReplicationBytesOutPerSecMax},
// 		{"ReplicationBytesInPerSec", &nodeMetric.ReplicationBytesInPerSecMax},
// 	}

// 	var peakWg sync.WaitGroup
// 	peakErrChan := make(chan error, len(peakMetricsAssignments))

// 	for _, assignment := range peakMetricsAssignments {
// 		peakWg.Add(1)
// 		go func(assignment struct {
// 			metricName  string
// 			targetField *float64
// 		}) {
// 			defer peakWg.Done()
// 			metricValue, err := ms.getPeakMetric(ctx, clusterName, assignment.metricName, &nodeID, startTime, endTime)
// 			if err != nil {
// 				peakErrChan <- fmt.Errorf("failed to get metric %s: %v", assignment.metricName, err)
// 				return
// 			}
// 			*assignment.targetField = metricValue
// 		}(assignment)
// 	}

// 	peakWg.Wait()
// 	close(peakErrChan)

// 	// Check for any errors
// 	for err := range peakErrChan {
// 		if err != nil {
// 			return nil, err
// 		}
// 	}

// 	return &nodeMetric, nil
// }

func (ms *MetricServiceV2) calculateClusterMetricsSummary(nodesMetrics []types.NodeMetrics) types.ClusterMetricsSummary {
	if len(nodesMetrics) == 0 {
		return types.ClusterMetricsSummary{}
	}

	var avgIngressThroughputMegabytesPerSecond float64
	for _, nodeMetric := range nodesMetrics {
		avgIngressThroughputMegabytesPerSecond += nodeMetric.BytesInPerSecAvg
	}
	avgIngressThroughputMegabytesPerSecond = avgIngressThroughputMegabytesPerSecond / 1024 / 1024

	// TODO: this needs reworking. To calculate this accurately we need to the get the MAX(SUM(MAX(BytesInPerSec)))
	// This will require a new metric service method that can get the max of the sum of the max of the bytes out per sec.
	// eg
	// "metrics": [
	// 	[ { "expression": "SUM(METRICS())", "label": "Expression1", "id": "e1", "stat": "Maximum" } ],
	// 	[ "AWS/Kafka", "BytesInPerSec", "Cluster Name", "msk-exp1-cluster", "Broker ID", "1", { "id": "m1", "visible": false } ],
	// 	[ "...", "2", { "id": "m2", "visible": false } ],
	// 	[ "...", "3", { "id": "m3", "visible": false } ]
	// "stat": "Maximum"
	var peakIngressThroughputMegabytesPerSecond float64
	for _, nodeMetric := range nodesMetrics {
		peakIngressThroughputMegabytesPerSecond += nodeMetric.BytesInPerSecMax
	}
	peakIngressThroughputMegabytesPerSecond = peakIngressThroughputMegabytesPerSecond / 1024 / 1024

	var avgEgressThroughputMegabytesPerSecond float64
	for _, nodeMetric := range nodesMetrics {
		avgEgressThroughputMegabytesPerSecond += nodeMetric.BytesOutPerSecAvg
	}
	avgEgressThroughputMegabytesPerSecond = avgEgressThroughputMegabytesPerSecond / 1024 / 1024

	// TODO: this needs reworking. To calculate this accurately we need to the get the MAX(SUM(MAX(BytesOutPerSec)))
	// This will require a new metric service method that can get the max of the sum of the max of the bytes out per sec.
	// eg
	// "metrics": [
	// 	[ { "expression": "SUM(METRICS())", "label": "Expression1", "id": "e1", "stat": "Maximum" } ],
	// 	[ "AWS/Kafka", "BytesOutPerSec", "Cluster Name", "msk-exp1-cluster", "Broker ID", "1", { "id": "m1", "visible": false } ],
	// 	[ "...", "2", { "id": "m2", "visible": false } ],
	// 	[ "...", "3", { "id": "m3", "visible": false } ]
	// "stat": "Maximum"
	var peakEgressThroughputMegabytesPerSecond float64
	for _, nodeMetric := range nodesMetrics {
		peakEgressThroughputMegabytesPerSecond += nodeMetric.BytesOutPerSecMax
	}
	peakEgressThroughputMegabytesPerSecond = peakEgressThroughputMegabytesPerSecond / 1024 / 1024

	retention_days, local_retention_hours := ms.calculateRetention(nodesMetrics)

	clusterMetricsSummary := types.ClusterMetricsSummary{
		AvgIngressThroughputMegabytesPerSecond:  &avgIngressThroughputMegabytesPerSecond,
		PeakIngressThroughputMegabytesPerSecond: &peakIngressThroughputMegabytesPerSecond,
		AvgEgressThroughputMegabytesPerSecond:   &avgEgressThroughputMegabytesPerSecond,
		PeakEgressThroughputMegabytesPerSecond:  &peakEgressThroughputMegabytesPerSecond,
		RetentionDays:                           &retention_days,
		LocalRetentionInPrimaryStorageHours:     &local_retention_hours,
	}

	return clusterMetricsSummary
}

func (ms *MetricServiceV2) calculateReplicationFactor(nodesMetrics []types.NodeMetrics, globalPartitionCountMax float64) *float64 {
	totalPartitions := 0
	for _, nodeMetric := range nodesMetrics {
		totalPartitions += int(nodeMetric.PartitionCountMax)
	}
	if globalPartitionCountMax == 0 {
		return aws.Float64(0)
	}
	replicationFactor := float64(totalPartitions) / float64(globalPartitionCountMax)
	return &replicationFactor

}

// Metric Retrieval Functions

// func (ms *MetricServiceV2) getAverageMetric(ctx context.Context, clusterName string, metricName string, node *int, startTime time.Time, endTime time.Time) (float64, error) {
// 	slog.Info("📊 getting cloudwatch average metric", "cluster", clusterName, "metric", metricName, "node", *node)
// 	metricRequest := ms.buildCloudWatchInput(clusterName, metricName, node, []cloudwatchtypes.Statistic{cloudwatchtypes.StatisticAverage}, startTime, endTime)

// 	response, err := ms.client.GetMetricStatistics(ctx, metricRequest)
// 	if err != nil {
// 		return 0, fmt.Errorf("failed to get metric statistics: %v", err)
// 	}
// 	if len(response.Datapoints) == 0 {
// 		return 0, nil
// 	}

// 	return *response.Datapoints[0].Average, nil
// }

// func (ms *MetricServiceV2) getPeakMetric(ctx context.Context, clusterName string, metricName string, node *int, startTime time.Time, endTime time.Time) (float64, error) {
// 	slog.Info("📈 getting cloudwatch peak metric", "cluster", clusterName, "metric", metricName, "node", *node)
// 	metricRequest := ms.buildCloudWatchInput(clusterName, metricName, node, []cloudwatchtypes.Statistic{cloudwatchtypes.StatisticMaximum}, startTime, endTime)

// 	response, err := ms.client.GetMetricStatistics(ctx, metricRequest)
// 	if err != nil {
// 		return 0, fmt.Errorf("failed to get metric statistics: %v", err)
// 	}
// 	if len(response.Datapoints) == 0 {
// 		return 0, nil
// 	}
// 	return *response.Datapoints[0].Maximum, nil
// }

// CloudWatch Helper Functions

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

// func (ms *MetricServiceV2) buildCloudWatchInput(clusterName, metricName string, node *int, statistics []cloudwatchtypes.Statistic, startTime time.Time, endTime time.Time) *cloudwatch.GetMetricStatisticsInput {
// 	// Use monthly period for consistent monthly data points
// 	period := int32(DailyPeriodSeconds)

// 	dimensions := []cloudwatchtypes.Dimension{
// 		{
// 			Name:  aws.String("Cluster Name"),
// 			Value: aws.String(clusterName),
// 		},
// 		{
// 			Name:  aws.String("Broker ID"),
// 			Value: aws.String(strconv.Itoa(*node)),
// 		},
// 	}

// 	return &cloudwatch.GetMetricStatisticsInput{
// 		Namespace:  aws.String("AWS/Kafka"),
// 		MetricName: aws.String(metricName),
// 		Dimensions: dimensions,
// 		StartTime:  aws.Time(startTime),
// 		EndTime:    aws.Time(endTime),
// 		Period:     aws.Int32(period),
// 		Statistics: statistics,
// 	}
// }

// Calculation Helper Functions

func (ms *MetricServiceV2) calculateRetention(nodesMetrics []types.NodeMetrics) (float64, float64) {
	var totalBytesInPerDay, totalLocalStorageUsed, totalRemoteStorageUsed float64

	for _, nodeMetric := range nodesMetrics {
		totalBytesInPerDay += nodeMetric.BytesInPerSecAvg * 60 * 60 * 24
		if nodeMetric.VolumeSizeGB != nil {
			totalLocalStorageUsed += ((nodeMetric.KafkaDataLogsDiskUsedAvg / 100) * float64(*nodeMetric.VolumeSizeGB)) * 1024 * 1024 * 1024
		}
		totalRemoteStorageUsed += nodeMetric.RemoteLogSizeBytesAvg
	}

	slog.Info("🔄 total bytes in per sec", "totalBytesInPerSec", totalBytesInPerDay)
	slog.Info("🔄 total local storage used", "totalLocalStorageUsed", totalLocalStorageUsed)
	slog.Info("🔄 total remote storage used", "totalRemoteStorageUsed", totalRemoteStorageUsed)

	if totalBytesInPerDay == 0 {
		return 0, 0
	}

	retention_days := (totalLocalStorageUsed + totalRemoteStorageUsed) / totalBytesInPerDay
	local_retention_days := totalLocalStorageUsed / totalBytesInPerDay

	return retention_days, local_retention_days
}

func (ms *MetricServiceV2) getServerlessMetric(ctx context.Context, clusterName string, metricName string, statistic cloudwatchtypes.Statistic, startTime time.Time, endTime time.Time) (float64, error) {
	// Use monthly period for consistent monthly data points
	period := int32(DailyPeriodSeconds)

	topics := make(map[string]struct{})

	listMetricsInput := &cloudwatch.ListMetricsInput{
		Namespace:  aws.String("AWS/Kafka"),
		MetricName: aws.String(metricName),
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
			return 0, fmt.Errorf("failed to list metrics: %v", err)
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

	if len(topics) == 0 {
		slog.Info("No topics found for cluster and metric with a 'Topic' dimension", "cluster", clusterName, "metric", metricName)

		return 0, nil
	}

	topicList := make([]string, 0, len(topics))
	for topic := range topics {
		topicList = append(topicList, topic)
	}

	aggregatedSum := 0.0
	maxQueriesPerCall := 500

	var output *cloudwatch.GetMetricDataOutput
	var err error
	for i := 0; i < len(topicList); i += maxQueriesPerCall {
		end := min(i+maxQueriesPerCall, len(topicList))

		metricDataQueries := make([]cloudwatchtypes.MetricDataQuery, 0, end-i)
		for j, topic := range topicList[i:end] {
			queryID := fmt.Sprintf("query_%s_%d", strings.ToLower(strings.ReplaceAll(metricName, "-", "_")), j)

			metricDataQueries = append(metricDataQueries, cloudwatchtypes.MetricDataQuery{
				Id: aws.String(queryID),
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
					Stat:   aws.String(string(statistic)),
				},
				ReturnData: aws.Bool(true),
			})
		}

		input := &cloudwatch.GetMetricDataInput{
			MetricDataQueries: metricDataQueries,
			StartTime:         aws.Time(startTime),
			EndTime:           aws.Time(endTime),
			ScanBy:            cloudwatchtypes.ScanByTimestampAscending,
		}

		output, err = ms.client.GetMetricData(ctx, input)
		if err != nil {
			slog.Error("Error during get_metric_data call", "error", err)
			continue
		}

		for _, result := range output.MetricDataResults {
			if len(result.Values) > 0 {
				aggregatedSum += result.Values[0]
			}
		}
	}

	outputJSON, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal output: %v", err)
	}
	slog.Info("🔄 output", "output", string(outputJSON))

	return aggregatedSum, nil
}
