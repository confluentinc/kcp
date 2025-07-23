package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp-internal/internal/types"
)

type ClusterMetricsOpts struct {
	Region    string
	StartDate time.Time
	EndDate   time.Time
}

type ClusterMetricsCollector struct {
	region        string
	mskService    MSKService
	metricService MetricService
	startDate     time.Time
	endDate       time.Time
}

type MSKService interface {
	GetClusters(ctx context.Context) ([]kafkatypes.Cluster, error)
	IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (*bool, error)
}

type MetricService interface {
	GetAverageMetric(clusterName string, metricName string, node *int) (float64, error)
	GetPeakMetric(clusterName string, metricName string, node *int) (float64, error)
	GetServerlessAverageMetric(clusterName string, metricName string) (float64, error)
	GetServerlessPeakMetric(clusterName string, metricName string) (float64, error)
}

func NewClusterMetrics(mskService MSKService, metricService MetricService, opts ClusterMetricsOpts) *ClusterMetricsCollector {
	return &ClusterMetricsCollector{
		region:        opts.Region,
		mskService:    mskService,
		metricService: metricService,
		startDate:     opts.StartDate,
		endDate:       opts.EndDate,
	}
}

func (rm *ClusterMetricsCollector) Run() error {
	slog.Info("🚀 starting region metrics report", "region", rm.region)

	clusters, err := rm.mskService.GetClusters(context.Background())
	if err != nil {
		return fmt.Errorf("❌ Failed to get clusters: %v", err)
	}

	clusterMetrics, err := rm.processClusters(clusters)
	if err != nil {
		return fmt.Errorf("❌ Failed to process clusters: %v", err)
	}

	metrics := types.RegionMetrics{
		Region:         rm.region,
		ClusterMetrics: clusterMetrics,
	}

	err = rm.writeOutput(metrics)
	if err != nil {
		return fmt.Errorf("❌ Failed to write output: %v", err)
	}

	slog.Info("✅ region metrics report complete", "region", rm.region)
	return nil
}

func (rm *ClusterMetricsCollector) processClusters(clusters []kafkatypes.Cluster) ([]types.ClusterMetrics, error) {
	clusterMetrics := []types.ClusterMetrics{}

	for _, cluster := range clusters {

		clusterMetric, err := rm.processCluster(cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to process cluster: %v", err)
		}
		clusterMetrics = append(clusterMetrics, *clusterMetric)

	}

	return clusterMetrics, nil
}

func (rm *ClusterMetricsCollector) processCluster(cluster kafkatypes.Cluster) (*types.ClusterMetrics, error) {
	slog.Info("🔄 processing cluster", "cluster", *cluster.ClusterName)
	var clusterMetric *types.ClusterMetrics
	var err error
	if cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		clusterMetric, err = rm.processProvisionedCluster(cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to process provisioned cluster: %v", err)
		}
	} else {
		clusterMetric, err = rm.processServerlessCluster(cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to process serverless cluster: %v", err)
		}
	}
	slog.Info("✅ cluster complete", "cluster", *cluster.ClusterName)
	slog.Info("")
	return clusterMetric, nil
}

func (rm *ClusterMetricsCollector) calculateClusterMetricsSummary(nodesMetrics []types.NodeMetrics) types.ClusterMetricsSummary {

	var avgIngressThroughputMegabytesPerSecond float64
	for _, nodeMetric := range nodesMetrics {
		avgIngressThroughputMegabytesPerSecond += nodeMetric.BytesInPerSecAvg
	}

	if len(nodesMetrics) == 0 {
		return types.ClusterMetricsSummary{}
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

	var partitions float64
	for _, nodeMetric := range nodesMetrics {
		partitions += float64(nodeMetric.PartitionCountMax)
	}

	clusterMetricsSummary := types.ClusterMetricsSummary{
		AvgIngressThroughputMegabytesPerSecond:  &avgIngressThroughputMegabytesPerSecond,
		PeakIngressThroughputMegabytesPerSecond: &peakIngressThroughputMegabytesPerSecond,
		AvgEgressThroughputMegabytesPerSecond:   &avgEgressThroughputMegabytesPerSecond,
		PeakEgressThroughputMegabytesPerSecond:  &peakEgressThroughputMegabytesPerSecond,
		Partitions:                              &partitions,
	}

	return clusterMetricsSummary
}

func (rm *ClusterMetricsCollector) processProvisionedCluster(cluster kafkatypes.Cluster) (*types.ClusterMetrics, error) {
	slog.Info("🏗️ processing provisioned cluster", "cluster", *cluster.ClusterName)
	authentication, err := structToMap(cluster.Provisioned.ClientAuthentication)
	if err != nil {
		return nil, fmt.Errorf("failed to convert provisioned client authentication to map: %w", err)
	}
	if authentication == nil {
		return nil, fmt.Errorf("provisioned client authentication is nil")
	}

	brokerAZDistribution := aws.String(string(cluster.Provisioned.BrokerNodeGroupInfo.BrokerAZDistribution))
	kafkaVersion := cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion
	enhancedMonitoring := aws.String(string(cluster.Provisioned.EnhancedMonitoring))
	numberOfBrokerNodes := int(*cluster.Provisioned.NumberOfBrokerNodes)
	instanceType := aws.ToString(cluster.Provisioned.BrokerNodeGroupInfo.InstanceType)

	nodesMetrics := []types.NodeMetrics{}

	for i := 1; i <= numberOfBrokerNodes; i++ {
		nodeMetric, err := rm.processProvisionedNode(*cluster.ClusterName, i, instanceType)
		if err != nil {
			return nil, fmt.Errorf("failed to process node: %v", err)
		}

		nodesMetrics = append(nodesMetrics, *nodeMetric)
	}

	clusterMetricsSummary := rm.calculateClusterMetricsSummary(nodesMetrics)
	clusterMetricsSummary.InstanceType = &instanceType
	clusterMetricsSummary.TieredStorage = aws.Bool(cluster.Provisioned.StorageMode == kafkatypes.StorageModeTiered)

	followerFetching, err := rm.mskService.IsFetchFromFollowerEnabled(context.Background(), cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to check if follower fetching is enabled: %v", err)
	}
	clusterMetricsSummary.FollowerFetching = followerFetching

	// // Replication Factor (Optional, Default = 3)
	// // Retention (Days)
	// // "Local Retention in Primary Storage (Hrs) ** leave blank if TS = FALSE"

	clusterMetric := types.ClusterMetrics{
		ClusterName:           *cluster.ClusterName,
		ClusterType:           string(cluster.ClusterType),
		BrokerAZDistribution:  brokerAZDistribution,
		KafkaVersion:          kafkaVersion,
		EnhancedMonitoring:    enhancedMonitoring,
		Authentication:        authentication,
		NodesMetrics:          nodesMetrics,
		ClusterMetricsSummary: clusterMetricsSummary,
	}

	return &clusterMetric, nil
}

func (rm *ClusterMetricsCollector) processServerlessCluster(cluster kafkatypes.Cluster) (*types.ClusterMetrics, error) {
	slog.Info("☁️ processing serverless cluster", "cluster", *cluster.ClusterName)
	authentication, err := structToMap(cluster.Serverless.ClientAuthentication)
	if err != nil {
		return nil, fmt.Errorf("failed to convert serverless client authentication to map: %w", err)
	}

	if authentication == nil {
		return nil, fmt.Errorf("serverless client authentication is nil")
	}

	nodesMetrics := []types.NodeMetrics{}
	// serverless has 1 broker node
	nodeMetric, err := rm.processServerlessNode(*cluster.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to process node: %v", err)
	}

	nodesMetrics = append(nodesMetrics, *nodeMetric)

	clusterMetricsSummary := rm.calculateClusterMetricsSummary(nodesMetrics)
	clusterMetricsSummary.InstanceType = nil

	clusterMetric := types.ClusterMetrics{
		ClusterName:           *cluster.ClusterName,
		ClusterType:           string(cluster.ClusterType),
		Authentication:        authentication,
		NodesMetrics:          nodesMetrics,
		ClusterMetricsSummary: clusterMetricsSummary,
	}

	return &clusterMetric, nil
}

func (rm *ClusterMetricsCollector) processProvisionedNode(clusterName string, nodeID int, instanceType string) (*types.NodeMetrics, error) {
	slog.Info("🏗️ processing provisioned node", "cluster", clusterName, "node", nodeID)
	nodeMetric := types.NodeMetrics{
		NodeID:       nodeID,
		InstanceType: &instanceType,
		VolumeSizeGB: 0,
	}

	averageMetricAssignments := []struct {
		metricName  string
		targetField *float64
	}{
		{"BytesInPerSec", &nodeMetric.BytesInPerSecAvg},
		{"BytesOutPerSec", &nodeMetric.BytesOutPerSecAvg},
		{"MessagesInPerSec", &nodeMetric.MessagesInPerSecAvg},
		{"KafkaDataLogsDiskUsed", &nodeMetric.KafkaDataLogsDiskUsedAvg},
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
			metricValue, err := rm.metricService.GetAverageMetric(clusterName, assignment.metricName, &nodeID)
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

	peakMetricsAssignments := []struct {
		metricName  string
		targetField *float64
	}{
		{"BytesInPerSec", &nodeMetric.BytesInPerSecMax},
		{"BytesOutPerSec", &nodeMetric.BytesOutPerSecMax},
		{"MessagesInPerSec", &nodeMetric.MessagesInPerSecMax},
		{"KafkaDataLogsDiskUsed", &nodeMetric.KafkaDataLogsDiskUsedMax},
		{"ClientConnectionCount", &nodeMetric.ClientConnectionCountMax},
		{"PartitionCount", &nodeMetric.PartitionCountMax},
		{"GlobalTopicCount", &nodeMetric.GlobalTopicCountMax},
		{"LeaderCount", &nodeMetric.LeaderCountMax},
		{"ReplicationBytesOutPerSec", &nodeMetric.ReplicationBytesOutPerSecMax},
		{"ReplicationBytesInPerSec", &nodeMetric.ReplicationBytesInPerSecMax},
	}

	var peakWg sync.WaitGroup
	peakErrChan := make(chan error, len(peakMetricsAssignments))

	for _, assignment := range peakMetricsAssignments {
		peakWg.Add(1)
		go func(assignment struct {
			metricName  string
			targetField *float64
		}) {
			defer peakWg.Done()
			metricValue, err := rm.metricService.GetPeakMetric(clusterName, assignment.metricName, &nodeID)
			if err != nil {
				peakErrChan <- fmt.Errorf("failed to get metric %s: %v", assignment.metricName, err)
				return
			}
			*assignment.targetField = metricValue
		}(assignment)
	}

	peakWg.Wait()
	close(peakErrChan)

	// Check for any errors
	for err := range peakErrChan {
		if err != nil {
			return nil, err
		}
	}

	return &nodeMetric, nil
}

func (rm *ClusterMetricsCollector) processServerlessNode(clusterName string) (*types.NodeMetrics, error) {
	slog.Info("☁️ processing serverless node", "cluster", clusterName)
	nodeMetric := types.NodeMetrics{
		NodeID:       1,
		InstanceType: nil,
		VolumeSizeGB: 0,
	}

	averageMetricAssignments := []struct {
		metricName  string
		targetField *float64
	}{
		{"BytesInPerSec", &nodeMetric.BytesInPerSecAvg},
		{"BytesOutPerSec", &nodeMetric.BytesOutPerSecAvg},
		{"MessagesInPerSec", &nodeMetric.MessagesInPerSecAvg},
		{"KafkaDataLogsDiskUsed", &nodeMetric.KafkaDataLogsDiskUsedAvg},
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
			metricValue, err := rm.metricService.GetServerlessAverageMetric(clusterName, assignment.metricName)
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

	peakMetricsAssignments := []struct {
		metricName  string
		targetField *float64
	}{
		{"BytesInPerSec", &nodeMetric.BytesInPerSecMax},
		{"BytesOutPerSec", &nodeMetric.BytesOutPerSecMax},
		{"MessagesInPerSec", &nodeMetric.MessagesInPerSecMax},
		{"KafkaDataLogsDiskUsed", &nodeMetric.KafkaDataLogsDiskUsedMax},
		{"ClientConnectionCount", &nodeMetric.ClientConnectionCountMax},
		{"PartitionCount", &nodeMetric.PartitionCountMax},
		{"GlobalTopicCount", &nodeMetric.GlobalTopicCountMax},
		{"LeaderCount", &nodeMetric.LeaderCountMax},
		{"ReplicationBytesOutPerSec", &nodeMetric.ReplicationBytesOutPerSecMax},
		{"ReplicationBytesInPerSec", &nodeMetric.ReplicationBytesInPerSecMax},
	}

	var peakWg sync.WaitGroup
	peakErrChan := make(chan error, len(peakMetricsAssignments))

	for _, assignment := range peakMetricsAssignments {
		peakWg.Add(1)
		go func(assignment struct {
			metricName  string
			targetField *float64
		}) {
			defer peakWg.Done()
			metricValue, err := rm.metricService.GetServerlessPeakMetric(clusterName, assignment.metricName)
			if err != nil {
				peakErrChan <- fmt.Errorf("failed to get metric %s: %v", assignment.metricName, err)
				return
			}
			*assignment.targetField = metricValue
		}(assignment)
	}

	peakWg.Wait()
	close(peakErrChan)

	// Check for any errors
	for err := range peakErrChan {
		if err != nil {
			return nil, err
		}
	}

	return &nodeMetric, nil
}

func (rm *ClusterMetricsCollector) writeOutput(metrics types.RegionMetrics) error {
	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cluster information: %v", err)
	}

	filePath := fmt.Sprintf("%s-metrics.json", rm.region)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	// Generate markdown report
	mdFilePath := fmt.Sprintf("%s-metrics.md", rm.region)
	if err := rm.generateMarkdownReport(metrics, mdFilePath); err != nil {
		return fmt.Errorf("failed to generate markdown report: %v", err)
	}

	return nil
}

func structToMap(s any) (map[string]any, error) {
	jsonBytes, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	err = json.Unmarshal(jsonBytes, &result)
	return result, err
}
