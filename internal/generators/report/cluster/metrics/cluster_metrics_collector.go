package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type ClusterMetricsOpts struct {
	Region            string
	StartDate         time.Time
	EndDate           time.Time
	ClusterArn        string
	SkipKafka         bool
	AuthType          types.AuthType
	SASLScramUsername string
	SASLScramPassword string
	TLSCACert         string
	TLSClientCert     string
	TLSClientKey      string
}

type Topic struct {
	Name              string
	ReplicationFactor int
}

type ClusterMetricsCollector struct {
	region            string
	mskService        MSKService
	metricService     MetricService
	kafkaAdminFactory KafkaAdminFactory
	startDate         time.Time
	endDate           time.Time
	clusterArn        string
	authType          types.AuthType
	skipKafka         bool
}

type MSKService interface {
	DescribeCluster(ctx context.Context, clusterArn *string) (*kafkatypes.Cluster, error)
	IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (*bool, error)
	GetBootstrapBrokers(ctx context.Context, clusterArn *string, authType types.AuthType) ([]string, error)
}

type KafkaAdminFactory func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error)

type MetricService interface {
	GetAverageMetric(clusterName string, metricName string, node *int) (float64, error)
	GetPeakMetric(clusterName string, metricName string, node *int) (float64, error)
	GetServerlessAverageMetric(clusterName string, metricName string) (float64, error)
	GetServerlessPeakMetric(clusterName string, metricName string) (float64, error)
	GetAverageBytesInPerSec(clusterName string, numNodes int, topic string) ([]float64, error)
}

func NewClusterMetrics(mskService MSKService, metricService MetricService, kafkaAdminFactory KafkaAdminFactory, opts ClusterMetricsOpts) *ClusterMetricsCollector {
	return &ClusterMetricsCollector{
		region:            opts.Region,
		mskService:        mskService,
		metricService:     metricService,
		kafkaAdminFactory: kafkaAdminFactory,
		startDate:         opts.StartDate,
		endDate:           opts.EndDate,
		clusterArn:        opts.ClusterArn,
		authType:          opts.AuthType,
		skipKafka:         opts.SkipKafka,
	}
}

func (rm *ClusterMetricsCollector) Run() error {
	slog.Info("🚀 starting cluster metrics report", "cluster", rm.clusterArn)

	cluster, err := rm.mskService.DescribeCluster(context.Background(), &rm.clusterArn)
	if err != nil {
		return fmt.Errorf("❌ Failed to get clusters: %v", err)
	}

	clusterMetrics, err := rm.processCluster(*cluster)
	if err != nil {
		return fmt.Errorf("❌ Failed to process clusters: %v", err)
	}

	err = rm.writeOutput(*clusterMetrics)
	if err != nil {
		return fmt.Errorf("❌ Failed to write output: %v", err)
	}

	slog.Info("✅ cluster metrics report complete", "cluster", rm.clusterArn)
	return nil
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

func (rm *ClusterMetricsCollector) calculateRetention(nodesMetrics []types.NodeMetrics) (float64, float64) {
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

	retention_days := (totalLocalStorageUsed + totalRemoteStorageUsed) / totalBytesInPerDay
	local_retention_days := totalLocalStorageUsed / totalBytesInPerDay

	return retention_days, local_retention_days
}

func (rm *ClusterMetricsCollector) calculateClusterMetricsSummary(nodesMetrics []types.NodeMetrics) types.ClusterMetricsSummary {

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

	var partitions float64
	for _, nodeMetric := range nodesMetrics {
		partitions += float64(nodeMetric.PartitionCountMax)
	}

	retention_days, local_retention_hours := rm.calculateRetention(nodesMetrics)

	clusterMetricsSummary := types.ClusterMetricsSummary{
		AvgIngressThroughputMegabytesPerSecond:  &avgIngressThroughputMegabytesPerSecond,
		PeakIngressThroughputMegabytesPerSecond: &peakIngressThroughputMegabytesPerSecond,
		AvgEgressThroughputMegabytesPerSecond:   &avgEgressThroughputMegabytesPerSecond,
		PeakEgressThroughputMegabytesPerSecond:  &peakEgressThroughputMegabytesPerSecond,
		Partitions:                              &partitions,
		RetentionDays:                           &retention_days,
		LocalRetentionInPrimaryStorageHours:     &local_retention_hours,
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

		if cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo != nil {
			nodeMetric.VolumeSizeGB = aws.Int(int(*cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize))
		}

		nodesMetrics = append(nodesMetrics, *nodeMetric)
	}

	clusterMetricsSummary := rm.calculateClusterMetricsSummary(nodesMetrics)
	clusterMetricsSummary.InstanceType = &instanceType
	clusterMetricsSummary.TieredStorage = aws.Bool(cluster.Provisioned.StorageMode == kafkatypes.StorageModeTiered)
	if !*clusterMetricsSummary.TieredStorage {
		clusterMetricsSummary.LocalRetentionInPrimaryStorageHours = nil
	}

	followerFetching, err := rm.mskService.IsFetchFromFollowerEnabled(context.Background(), cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to check if follower fetching is enabled: %v", err)
	}
	clusterMetricsSummary.FollowerFetching = followerFetching

	// // Replication Factor (Optional, Default = 3)
	replicationFactor, err := rm.calculateReplicationFactor(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate replication factor: %v", err)
	}
	clusterMetricsSummary.ReplicationFactor = replicationFactor

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

func (rm *ClusterMetricsCollector) calculateReplicationFactor(cluster kafkatypes.Cluster) (*float64, error) {

	if rm.skipKafka {
		slog.Info("🔍 skipping replication calculation due to --skipKafka flag preventing broker connection")
		return nil, nil
	}

	// initialize the kafka admin client
	brokerAddresses, err := rm.mskService.GetBootstrapBrokers(context.Background(), &rm.clusterArn, rm.authType)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to get bootstrap brokers: %v", err)
	}

	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(cluster)

	admin, err := rm.kafkaAdminFactory(brokerAddresses, clientBrokerEncryptionInTransit)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer admin.Close()

	defaultReplicationFactor := 3
	config, err := admin.DescribeConfig()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe config: %v", err)
	}

	for _, c := range config {
		if c.Name == "default.replication.factor" {
			if val, err := strconv.Atoi(c.Value); err == nil {
				defaultReplicationFactor = val
			} else {
				slog.Warn("Failed to parse default.replication.factor, using default value", "value", c.Value, "error", err)
			}
			break
		}
	}

	topics, err := admin.ListTopics()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	topicsData := make([]Topic, 0, len(topics))
	for topic := range topics {
		replicationFactor := int(topics[topic].ReplicationFactor)
		if replicationFactor == -1 {
			replicationFactor = defaultReplicationFactor
		}
		topicsData = append(topicsData, Topic{
			Name:              topic,
			ReplicationFactor: replicationFactor,
		})
	}

	totalBytesInPerSec := 0.0
	totalReplicationDataBytes := 0.0

	for _, topic := range topicsData {

		numNodes := 1
		if cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
			numNodes = int(*cluster.Provisioned.NumberOfBrokerNodes)
		}
		
		bytesInPerSec, err := rm.metricService.GetAverageBytesInPerSec(*cluster.ClusterName, numNodes, topic.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get average bytes in per sec: %v", err)
		}
		for _, bytesInPerSec := range bytesInPerSec {
			totalBytesInPerSec += bytesInPerSec
			totalReplicationDataBytes += bytesInPerSec * float64(topic.ReplicationFactor)
		}
	}

	if totalBytesInPerSec == 0 {
		return nil, nil
	}

	replicationFactor := totalReplicationDataBytes / totalBytesInPerSec
	return &replicationFactor, nil
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

	clusterMetricsSummary.TieredStorage = aws.Bool(false)

	followerFetching, err := rm.mskService.IsFetchFromFollowerEnabled(context.Background(), cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to check if follower fetching is enabled: %v", err)
	}
	clusterMetricsSummary.FollowerFetching = followerFetching


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
		{"RemoteLogSizeBytes", &nodeMetric.RemoteLogSizeBytesMax},
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
		{"RemoteLogSizeBytes", &nodeMetric.RemoteLogSizeBytesMax},
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

func (rm *ClusterMetricsCollector) writeOutput(metrics types.ClusterMetrics) error {
	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cluster information: %v", err)
	}

	filePath := fmt.Sprintf("%s-metrics.json", metrics.ClusterName)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	// Generate markdown report
	mdFilePath := fmt.Sprintf("%s-metrics.md", metrics.ClusterName)
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
