package metrics

import (
	"context"
	"encoding/json"
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

// cloudWatchGetMetricDataAPI is the subset of the CloudWatch client used by
// MetricService. It exists so the chunking/stitching logic can be unit-tested
// with a fake. *cloudwatch.Client satisfies it.
type cloudWatchGetMetricDataAPI interface {
	GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

const (
	// datapointBudget is CloudWatch's per-request GetMetricData limit (100,800)
	// with a safety margin. Chunk windows are sized so the datapoints from all
	// series in a request (fan-out + returned math) stay under it.
	datapointBudget = 100_000
	// maxClientAuthTypes bounds the "Client Authentication" dimension cardinality
	// (TLS, SASL/SCRAM, IAM, Unauthenticated) for the ClientConnectionCount estimate.
	maxClientAuthTypes = 4
)

type MetricService struct {
	client cloudWatchGetMetricDataAPI
}

func NewMetricService(client *cloudwatch.Client) *MetricService {
	return &MetricService{client: client}
}

// buildProvisionedMetadata extracts cluster metadata from a provisioned cluster,
// with nil guards for all optional AWS fields. Safe to call without a CloudWatch client.
// followerFetching comes from the IsFetchFromFollowerEnabled check done before this call.
func buildProvisionedMetadata(cluster kafkatypes.Cluster, timeWindow types.CloudWatchTimeWindow, followerFetching bool) types.MetricMetadata {
	clusterName := aws.ToString(cluster.ClusterName)

	if cluster.Provisioned == nil {
		slog.Warn("Provisioned config is nil, returning empty metadata", "cluster", clusterName)
		return types.MetricMetadata{
			ClusterType: string(cluster.ClusterType),
			StartDate:   timeWindow.StartTime,
			EndDate:     timeWindow.EndTime,
			Period:      timeWindow.Period,
		}
	}

	enhancedMonitoring := string(cluster.Provisioned.EnhancedMonitoring)
	tieredStorage := cluster.Provisioned.StorageMode == kafkatypes.StorageModeTiered

	brokerAZDistribution := ""
	instanceType := ""
	if cluster.Provisioned.BrokerNodeGroupInfo != nil {
		brokerAZDistribution = string(cluster.Provisioned.BrokerNodeGroupInfo.BrokerAZDistribution)
		instanceType = aws.ToString(cluster.Provisioned.BrokerNodeGroupInfo.InstanceType)
	} else {
		slog.Warn("BrokerNodeGroupInfo is nil, AZ distribution and instance type unavailable", "cluster", clusterName)
	}

	numberOfBrokerNodes := 0
	if cluster.Provisioned.NumberOfBrokerNodes != nil {
		numberOfBrokerNodes = int(*cluster.Provisioned.NumberOfBrokerNodes)
	} else {
		slog.Warn("NumberOfBrokerNodes is nil, defaulting to 0", "cluster", clusterName)
	}

	kafkaVersion := ""
	if cluster.Provisioned.CurrentBrokerSoftwareInfo != nil {
		kafkaVersion = aws.ToString(cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	} else {
		slog.Warn("CurrentBrokerSoftwareInfo is nil, KafkaVersion unavailable", "cluster", clusterName)
	}

	return types.MetricMetadata{
		ClusterType:          string(cluster.ClusterType),
		BrokerAzDistribution: brokerAZDistribution,
		NumberOfBrokerNodes:  numberOfBrokerNodes,
		KafkaVersion:         kafkaVersion,
		EnhancedMonitoring:   enhancedMonitoring,
		StartDate:            timeWindow.StartTime,
		EndDate:              timeWindow.EndTime,
		Period:               timeWindow.Period,
		InstanceType:         instanceType,
		TieredStorage:        tieredStorage,
		BrokerType:           getBrokerType(instanceType),
		FollowerFetching:     followerFetching,
	}
}

// ProcessProvisionedCluster processes metrics for provisioned aggregated across all brokers in a cluster
func (ms *MetricService) ProcessProvisionedCluster(ctx context.Context, cluster kafkatypes.Cluster, followerFetching bool, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error) {

	slog.Info("🔍 processing provisioned cluster", "cluster", aws.ToString(cluster.ClusterName), "startDate", timeWindow.StartTime, "endDate", timeWindow.EndTime, "period", timeWindow.Period)

	if cluster.Provisioned == nil {
		return nil, fmt.Errorf("cluster %s has no provisioned configuration", aws.ToString(cluster.ClusterName))
	}

	metricsMetadata := buildProvisionedMetadata(cluster, timeWindow, followerFetching)
	numBrokers := metricsMetadata.NumberOfBrokerNodes
	clusterName := aws.ToString(cluster.ClusterName)

	brokerQueries, brokerQueryInfos := ms.buildBrokerMetricQueries(clusterName, timeWindow.Period)
	brokerQueryResult, err := ms.executeChunkedQuery(ctx, brokerQueries, timeWindow.StartTime, timeWindow.EndTime, timeWindow.Period, brokerSeriesEstimate(numBrokers), "broker metrics for "+clusterName)
	if err != nil {
		return nil, err
	}

	clientConnectionQueries, clientConnQueryInfos := ms.buildClientConnectionQueries(clusterName, timeWindow.Period)
	clientConnectionQueryResult, err := ms.executeChunkedQuery(ctx, clientConnectionQueries, timeWindow.StartTime, timeWindow.EndTime, timeWindow.Period, clientConnSeriesEstimate(numBrokers), "client-connection metrics for "+clusterName)
	if err != nil {
		return nil, err
	}

	clusterQueries, clusterQueryInfos := ms.buildClusterMetricQueries(clusterName, timeWindow.Period)
	clusterQueryResult, err := ms.executeChunkedQuery(ctx, clusterQueries, timeWindow.StartTime, timeWindow.EndTime, timeWindow.Period, 1, "cluster metrics for "+clusterName)
	if err != nil {
		return nil, err
	}

	// for express brokers there is no storage info
	if metricsMetadata.BrokerType == types.BrokerTypeExpress {
		allQueries := make([]cloudwatchtypes.MetricDataQuery, 0, len(brokerQueries)+len(clusterQueries))
		allQueries = append(allQueries, brokerQueries...)
		allQueries = append(allQueries, clusterQueries...)
		allQueryInfos := make([]types.MetricQueryInfo, 0, len(brokerQueryInfos)+len(clusterQueryInfos))
		allQueryInfos = append(allQueryInfos, brokerQueryInfos...)
		allQueryInfos = append(allQueryInfos, clusterQueryInfos...)
		populateCLICommands(allQueryInfos, allQueries, timeWindow.StartTime, timeWindow.EndTime, regionFromArn(cluster.ClusterArn))
		return &types.ClusterMetrics{
			MetricMetadata: metricsMetadata,
			Results:        append(brokerQueryResult.MetricDataResults, clusterQueryResult.MetricDataResults...),
			QueryInfo:      allQueryInfos,
		}, nil
	}

	// Guard the EBS volume size nil chain — express brokers already returned above,
	// but standard brokers may have configurations without EBS info.
	clusterVolumeSizeGB := 0
	if cluster.Provisioned.BrokerNodeGroupInfo != nil &&
		cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo != nil &&
		cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo != nil &&
		cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize != nil {
		clusterVolumeSizeGB = int(*cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
	} else {
		slog.Warn("EBS volume size unavailable, local storage metrics may be inaccurate", "cluster", aws.ToString(cluster.ClusterName))
	}
	localStorageQueries, localStorageQueryInfos := ms.buildLocalStorageUsageQuery(clusterName, timeWindow.Period, clusterVolumeSizeGB)
	storageQueryResult, err := ms.executeChunkedQuery(ctx, localStorageQueries, timeWindow.StartTime, timeWindow.EndTime, timeWindow.Period, storageSeriesEstimate(numBrokers), "local-storage metrics for "+clusterName)
	if err != nil {
		return nil, err
	}

	remoteStorageQueries, remoteStorageQueryInfos := ms.buildRemoteStorageUsageQuery(clusterName, timeWindow.Period)
	remoteStorageQueryResult, err := ms.executeChunkedQuery(ctx, remoteStorageQueries, timeWindow.StartTime, timeWindow.EndTime, timeWindow.Period, storageSeriesEstimate(numBrokers), "remote-storage metrics for "+clusterName)
	if err != nil {
		return nil, err
	}

	// Combine broker and cluster metric results
	combinedResults := make([]cloudwatchtypes.MetricDataResult, 0,
		len(brokerQueryResult.MetricDataResults)+len(clusterQueryResult.MetricDataResults)+
			len(clientConnectionQueryResult.MetricDataResults)+len(storageQueryResult.MetricDataResults)+
			len(remoteStorageQueryResult.MetricDataResults))
	combinedResults = append(combinedResults, brokerQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, clusterQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, clientConnectionQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, storageQueryResult.MetricDataResults...)
	combinedResults = append(combinedResults, remoteStorageQueryResult.MetricDataResults...)

	// Combine all query infos and populate CLI commands
	allQueries := make([]cloudwatchtypes.MetricDataQuery, 0,
		len(brokerQueries)+len(clientConnectionQueries)+len(clusterQueries)+
			len(localStorageQueries)+len(remoteStorageQueries))
	allQueries = append(allQueries, brokerQueries...)
	allQueries = append(allQueries, clientConnectionQueries...)
	allQueries = append(allQueries, clusterQueries...)
	allQueries = append(allQueries, localStorageQueries...)
	allQueries = append(allQueries, remoteStorageQueries...)

	allQueryInfos := make([]types.MetricQueryInfo, 0,
		len(brokerQueryInfos)+len(clientConnQueryInfos)+len(clusterQueryInfos)+
			len(localStorageQueryInfos)+len(remoteStorageQueryInfos))
	allQueryInfos = append(allQueryInfos, brokerQueryInfos...)
	allQueryInfos = append(allQueryInfos, clientConnQueryInfos...)
	allQueryInfos = append(allQueryInfos, clusterQueryInfos...)
	allQueryInfos = append(allQueryInfos, localStorageQueryInfos...)
	allQueryInfos = append(allQueryInfos, remoteStorageQueryInfos...)
	populateCLICommands(allQueryInfos, allQueries, timeWindow.StartTime, timeWindow.EndTime, regionFromArn(cluster.ClusterArn))

	clusterMetrics := types.ClusterMetrics{
		MetricMetadata: metricsMetadata,
		Results:        combinedResults,
		QueryInfo:      allQueryInfos,
	}

	return &clusterMetrics, nil
}

// ProcessServerlessCluster processes metrics for serverless aggregated across all topics in a cluster
func (ms *MetricService) ProcessServerlessCluster(ctx context.Context, cluster kafkatypes.Cluster, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error) {
	slog.Info("🔍 processing serverless cluster with topic aggregation", "cluster", aws.ToString(cluster.ClusterName), "startDate", timeWindow.StartTime, "endDate", timeWindow.EndTime)

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
	queries, queryInfos := ms.buildServerlessMetricQueries(aws.ToString(cluster.ClusterName), timeWindow.Period)
	populateCLICommands(queryInfos, queries, timeWindow.StartTime, timeWindow.EndTime, regionFromArn(cluster.ClusterArn))

	// Execute the metric query
	queryResult, err := ms.executeChunkedQuery(ctx, queries, timeWindow.StartTime, timeWindow.EndTime, timeWindow.Period, 0, "serverless metrics for "+aws.ToString(cluster.ClusterName))
	if err != nil {
		return nil, fmt.Errorf("failed to execute serverless metric queries: %w", err)
	}

	clusterMetrics := types.ClusterMetrics{
		MetricMetadata: metricsMetadata,
		Results:        queryResult.MetricDataResults,
		QueryInfo:      queryInfos,
	}

	return &clusterMetrics, nil
}

// Private Helper Functions - Query Building
func (ms *MetricService) buildBrokerMetricQueries(clusterName string, period int32) ([]cloudwatchtypes.MetricDataQuery, []types.MetricQueryInfo) {
	metricStatMap := map[string]string{
		"BytesInPerSec":    "Average",
		"BytesOutPerSec":   "Average",
		"MessagesInPerSec": "Average",
		"PartitionCount":   "Maximum",
	}

	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', '%s', %d)"

	var queries []cloudwatchtypes.MetricDataQuery
	var queryInfos []types.MetricQueryInfo
	for metricName, metricStat := range metricStatMap {
		searchID := fmt.Sprintf("m_%s", strings.ToLower(metricName))
		sumID := fmt.Sprintf("sum_%s", strings.ToLower(metricName))
		searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, metricStat, period)
		mathExpr := fmt.Sprintf("SUM(%s)", searchID)
		queries = append(queries,
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(searchID),
				Expression: aws.String(searchExpr),
				ReturnData: aws.Bool(false),
			},
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(sumID),
				Expression: aws.String(mathExpr),
				Label:      aws.String(metricName),
				ReturnData: aws.Bool(true),
			},
		)
		queryInfos = append(queryInfos, newSearchMetricQueryInfo(metricName, searchExpr, mathExpr, metricStat, period, "Cluster Name, Broker ID"))
	}
	return queries, queryInfos
}

func (ms *MetricService) buildClientConnectionQueries(clusterName string, period int32) ([]cloudwatchtypes.MetricDataQuery, []types.MetricQueryInfo) {
	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\",\"Client Authentication\"} MetricName=\"ClientConnectionCount\" \"Cluster Name\"=\"%s\"', '%s', %d)"
	dimensions := "Cluster Name, Broker ID, Client Authentication"

	searchExprMax := fmt.Sprintf(searchTemplate, clusterName, "Maximum", period)
	searchExprAvg := fmt.Sprintf(searchTemplate, clusterName, "Average", period)

	queries := []cloudwatchtypes.MetricDataQuery{
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

	queryInfos := []types.MetricQueryInfo{
		newSearchMetricQueryInfo("ClientConnectionCount (Maximum)", searchExprMax, "SUM(max_all)", "Maximum", period, dimensions),
		newSearchMetricQueryInfo("ClientConnectionCount (Average)", searchExprAvg, "SUM(avg_all)", "Average", period, dimensions),
	}

	return queries, queryInfos
}

func (ms *MetricService) buildLocalStorageUsageQuery(clusterName string, period int32, volumeSizeGB int) ([]cloudwatchtypes.MetricDataQuery, []types.MetricQueryInfo) {
	metricName := "KafkaDataLogsDiskUsed"
	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', 'Maximum', %d)"
	searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, period)
	mathExpr := fmt.Sprintf("SUM(((m_local_disk / 100) * %d))", volumeSizeGB)
	queries := []cloudwatchtypes.MetricDataQuery{
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
	queryInfos := []types.MetricQueryInfo{
		newSearchMetricQueryInfo("TotalLocalStorageUsage(GB)", searchExpr, mathExpr, "Maximum", period, "Cluster Name, Broker ID"),
	}
	return queries, queryInfos
}

func (ms *MetricService) buildRemoteStorageUsageQuery(clusterName string, period int32) ([]cloudwatchtypes.MetricDataQuery, []types.MetricQueryInfo) {
	metricName := "RemoteLogSizeBytes"
	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Broker ID\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', 'Maximum', %d)"
	searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, period)
	const bytesPerGB = 1073741824
	mathExpr := fmt.Sprintf("SUM((m_remote / %d))", bytesPerGB)
	queries := []cloudwatchtypes.MetricDataQuery{
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
	queryInfos := []types.MetricQueryInfo{
		newSearchMetricQueryInfo("TotalRemoteStorageUsage(GB)", searchExpr, mathExpr, "Maximum", period, "Cluster Name, Broker ID"),
	}
	return queries, queryInfos
}

func (ms *MetricService) buildClusterMetricQueries(clusterName string, period int32) ([]cloudwatchtypes.MetricDataQuery, []types.MetricQueryInfo) {
	metricName := "GlobalPartitionCount"

	metricID := fmt.Sprintf("m_%s", strings.ToLower(metricName))
	queries := []cloudwatchtypes.MetricDataQuery{
		{
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
		},
	}

	queryInfos := []types.MetricQueryInfo{
		{
			MetricName:       metricName,
			SourceType:       types.MetricBackendCloudWatch,
			Namespace:        "AWS/Kafka",
			Dimensions:       "Cluster Name",
			Statistic:        "Maximum",
			Period:           period,
			SearchExpression: "",
			MathExpression:   "",
			AggregationNote:  "This metric is queried directly using MetricStat (not a SEARCH expression). It reports the cluster-level global partition count.",
		},
	}

	return queries, queryInfos
}

func (ms *MetricService) buildServerlessMetricQueries(clusterName string, period int32) ([]cloudwatchtypes.MetricDataQuery, []types.MetricQueryInfo) {
	metricNames := []string{
		"BytesInPerSec",
		"BytesOutPerSec",
		"MessagesInPerSec",
	}

	searchTemplate := "SEARCH('{AWS/Kafka,\"Cluster Name\",\"Topic\"} MetricName=\"%s\" \"Cluster Name\"=\"%s\"', 'Average', %d)"

	var queries []cloudwatchtypes.MetricDataQuery
	var queryInfos []types.MetricQueryInfo
	for _, metricName := range metricNames {
		searchID := fmt.Sprintf("m_%s", strings.ToLower(metricName))
		sumID := fmt.Sprintf("sum_%s", strings.ToLower(metricName))
		searchExpr := fmt.Sprintf(searchTemplate, metricName, clusterName, period)
		mathExpr := fmt.Sprintf("SUM(%s)", searchID)
		queries = append(queries,
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(searchID),
				Expression: aws.String(searchExpr),
				ReturnData: aws.Bool(false),
			},
			cloudwatchtypes.MetricDataQuery{
				Id:         aws.String(sumID),
				Expression: aws.String(mathExpr),
				Label:      aws.String(metricName),
				ReturnData: aws.Bool(true),
			},
		)
		queryInfos = append(queryInfos, newSearchMetricQueryInfo(metricName, searchExpr, mathExpr, "Average", period, "Cluster Name, Topic"))
	}
	return queries, queryInfos
}

// Private Helper Functions - Query Info Building

func newSearchMetricQueryInfo(metricName, searchExpr, mathExpr, stat string, period int32, dimensions string) types.MetricQueryInfo {
	return types.MetricQueryInfo{
		MetricName:       metricName,
		SourceType:       types.MetricBackendCloudWatch,
		Namespace:        "AWS/Kafka",
		Dimensions:       dimensions,
		Statistic:        stat,
		Period:           period,
		SearchExpression: searchExpr,
		MathExpression:   mathExpr,
		AggregationNote:  fmt.Sprintf("Uses SEARCH to find %s across all %s, then aggregates with %s.", metricName, dimensions, mathExpr),
	}
}

// cliQueryEntry is a simplified representation of a CloudWatch MetricDataQuery for CLI command serialization.
type cliQueryEntry struct {
	ID         string         `json:"Id"`
	Expression string         `json:"Expression,omitempty"`
	MetricStat *cliMetricStat `json:"MetricStat,omitempty"`
	Label      string         `json:"Label,omitempty"`
	ReturnData bool           `json:"ReturnData"`
}

type cliMetricStat struct {
	Metric struct {
		Namespace  string         `json:"Namespace"`
		MetricName string         `json:"MetricName"`
		Dimensions []cliDimension `json:"Dimensions"`
	} `json:"Metric"`
	Period int32  `json:"Period"`
	Stat   string `json:"Stat"`
}

type cliDimension struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

func populateCLICommands(queryInfos []types.MetricQueryInfo, queries []cloudwatchtypes.MetricDataQuery, startTime, endTime time.Time, region string) {
	// Build a map from metric query info to the relevant MetricDataQuery entries.
	// For SEARCH-based metrics: match by finding the SEARCH expression in the query list.
	// For MetricStat-based metrics: match by MetricName.
	for i := range queryInfos {
		info := &queryInfos[i]
		var cliEntries []cliQueryEntry

		if info.SearchExpression != "" {
			// Find the SEARCH query and all dependent math queries (transitive)
			for _, q := range queries {
				if q.Expression != nil && *q.Expression == info.SearchExpression {
					cliEntries = append(cliEntries, cliQueryEntry{
						ID:         aws.ToString(q.Id),
						Expression: aws.ToString(q.Expression),
						ReturnData: aws.ToBool(q.ReturnData),
					})
					// Collect all IDs we've found so far, then find queries referencing them
					knownIDs := map[string]bool{aws.ToString(q.Id): true}
					for changed := true; changed; {
						changed = false
						for _, mq := range queries {
							mqID := aws.ToString(mq.Id)
							if knownIDs[mqID] || mq.Expression == nil || *mq.Expression == info.SearchExpression {
								continue
							}
							// Check if this query references any known ID
							for id := range knownIDs {
								if strings.Contains(*mq.Expression, id) {
									entry := cliQueryEntry{
										ID:         mqID,
										Expression: aws.ToString(mq.Expression),
										ReturnData: aws.ToBool(mq.ReturnData),
									}
									if mq.Label != nil {
										entry.Label = *mq.Label
									}
									cliEntries = append(cliEntries, entry)
									knownIDs[mqID] = true
									changed = true
									break
								}
							}
						}
					}
					break
				}
			}
		} else {
			// MetricStat-based query (e.g., GlobalPartitionCount)
			for _, q := range queries {
				if q.MetricStat != nil && q.MetricStat.Metric != nil &&
					aws.ToString(q.MetricStat.Metric.MetricName) == info.MetricName {
					var dims []cliDimension
					for _, d := range q.MetricStat.Metric.Dimensions {
						dims = append(dims, cliDimension{
							Name:  aws.ToString(d.Name),
							Value: aws.ToString(d.Value),
						})
					}
					stat := &cliMetricStat{
						Period: aws.ToInt32(q.MetricStat.Period),
						Stat:   aws.ToString(q.MetricStat.Stat),
					}
					stat.Metric.Namespace = aws.ToString(q.MetricStat.Metric.Namespace)
					stat.Metric.MetricName = aws.ToString(q.MetricStat.Metric.MetricName)
					stat.Metric.Dimensions = dims
					cliEntries = append(cliEntries, cliQueryEntry{
						ID:         aws.ToString(q.Id),
						MetricStat: stat,
						ReturnData: aws.ToBool(q.ReturnData),
					})
					break
				}
			}
		}

		if len(cliEntries) > 0 {
			queriesJSON, err := json.MarshalIndent(cliEntries, "    ", "  ")
			if err == nil {
				// Use a heredoc to avoid all shell quoting issues with single/double quotes.
				// The <<'QUERY' form prevents any shell interpretation of the JSON content.
				info.AWSCLICommand = fmt.Sprintf("aws cloudwatch get-metric-data \\\n  --region %s \\\n  --start-time %s \\\n  --end-time %s \\\n  --metric-data-queries \"$(cat <<'QUERY'\n%s\nQUERY\n)\"",
					region,
					startTime.Format(time.RFC3339),
					endTime.Format(time.RFC3339),
					string(queriesJSON))
			}
		}

		// Build CloudWatch Console Source tab JSON from the same entries
		info.ConsoleSourceJSON = buildConsoleSourceJSON(cliEntries, region)
	}
}

// consoleSourceJSON is the JSON structure expected by the CloudWatch Console Source tab.
type consoleSourceJSON struct {
	View    string `json:"view"`
	Stacked bool   `json:"stacked"`
	Region  string `json:"region"`
	Metrics []any  `json:"metrics"`
}

// consoleExpressionEntry represents an expression-based metric in CloudWatch Console Source JSON.
type consoleExpressionEntry struct {
	ID         string `json:"id"`
	Expression string `json:"expression"`
	Label      string `json:"label,omitempty"`
}

func buildConsoleSourceJSON(entries []cliQueryEntry, region string) string {
	if len(entries) == 0 {
		return ""
	}

	source := consoleSourceJSON{
		View:    "timeSeries",
		Stacked: false,
		Region:  region,
	}

	for _, entry := range entries {
		if entry.Expression != "" {
			ce := consoleExpressionEntry{
				ID:         entry.ID,
				Expression: entry.Expression,
				Label:      entry.Label,
			}
			source.Metrics = append(source.Metrics, []any{ce})
		} else if entry.MetricStat != nil {
			// MetricStat-based: ["namespace", "metricName", "dimName", "dimValue", ...]
			metricEntry := []any{entry.MetricStat.Metric.Namespace, entry.MetricStat.Metric.MetricName}
			for _, dim := range entry.MetricStat.Metric.Dimensions {
				metricEntry = append(metricEntry, dim.Name, dim.Value)
			}
			source.Metrics = append(source.Metrics, metricEntry)
		}
	}

	jsonBytes, err := json.MarshalIndent(source, "", "    ")
	if err != nil {
		return ""
	}
	return string(jsonBytes)
}

// regionFromArn extracts the AWS region from an ARN (e.g. arn:aws:kafka:us-east-1:123456:cluster/...)
func regionFromArn(arn *string) string {
	if arn == nil {
		return ""
	}
	parts := strings.Split(*arn, ":")
	if len(parts) >= 4 {
		return parts[3]
	}
	return ""
}

// Private Helper Functions - Query Execution

// executeWindow fetches one [startTime, endTime) window, following NextToken
// pagination. ScanBy is ascending so callers can concatenate windows in time order.
func (ms *MetricService) executeWindow(ctx context.Context, queries []cloudwatchtypes.MetricDataQuery, startTime, endTime time.Time) (*cloudwatch.GetMetricDataOutput, error) {
	input := &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(startTime),
		EndTime:           aws.Time(endTime),
		ScanBy:            cloudwatchtypes.ScanByTimestampAscending,
	}

	var allResults []cloudwatchtypes.MetricDataResult
	var allMessages []cloudwatchtypes.MessageData
	var nextToken *string
	for {
		input.NextToken = nextToken
		result, err := ms.client.GetMetricData(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to get metric data: %w", err)
		}
		allResults = append(allResults, result.MetricDataResults...)
		allMessages = append(allMessages, result.Messages...)
		if result.NextToken == nil {
			break
		}
		nextToken = result.NextToken
	}

	return &cloudwatch.GetMetricDataOutput{MetricDataResults: allResults, Messages: allMessages}, nil
}

// resultStitcher concatenates MetricDataResult points per Id across sub-window
// calls, preserving first-seen Id order and tracking whether any chunk was partial.
type resultStitcher struct {
	order   []string
	byID    map[string]*cloudwatchtypes.MetricDataResult
	partial bool
}

func newResultStitcher() *resultStitcher {
	return &resultStitcher{byID: map[string]*cloudwatchtypes.MetricDataResult{}}
}

func (s *resultStitcher) markPartial() { s.partial = true }

func (s *resultStitcher) add(results []cloudwatchtypes.MetricDataResult) {
	for _, r := range results {
		id := aws.ToString(r.Id)
		existing, ok := s.byID[id]
		if !ok {
			cp := r
			s.byID[id] = &cp
			s.order = append(s.order, id)
			continue
		}
		existing.Timestamps = append(existing.Timestamps, r.Timestamps...)
		existing.Values = append(existing.Values, r.Values...)
		if r.Label != nil {
			existing.Label = r.Label
		}
		if r.StatusCode == cloudwatchtypes.StatusCodePartialData {
			existing.StatusCode = cloudwatchtypes.StatusCodePartialData
		}
	}
}

// output returns the stitched results. Per-window Messages are intentionally not
// carried through (they are consumed during collection via isPartial); current
// callers only read MetricDataResults.
func (s *resultStitcher) output() *cloudwatch.GetMetricDataOutput {
	results := make([]cloudwatchtypes.MetricDataResult, 0, len(s.order))
	for _, id := range s.order {
		results = append(results, *s.byID[id])
	}
	return &cloudwatch.GetMetricDataOutput{MetricDataResults: results}
}

// chunkSeconds returns the maximum sub-window length (seconds) that keeps a
// request under datapointBudget for the given period and series count. Returns
// 0 when the estimate is unknown (<=0) or period is non-positive, signalling the
// caller to use the full window and rely on the partial-data fallback.
func chunkSeconds(period int32, seriesEstimate int) int64 {
	if period <= 0 || seriesEstimate <= 0 {
		return 0
	}
	maxPtsPerSeries := datapointBudget / seriesEstimate
	if maxPtsPerSeries < 1 {
		maxPtsPerSeries = 1
	}
	return int64(maxPtsPerSeries) * int64(period)
}

// Series-count estimates count fan-out series (one per broker) PLUS the returned
// math-result series, since both consume the datapoint budget. They err high so
// chunks never exceed the cap; the fallback splitter self-heals any under-estimate.
func brokerSeriesEstimate(numBrokers int) int     { return 4 * (numBrokers + 1) }                    // 4 metrics
func clientConnSeriesEstimate(numBrokers int) int { return 2 * (numBrokers*maxClientAuthTypes + 1) } // 2 stats
func storageSeriesEstimate(numBrokers int) int    { return numBrokers + 2 }                          // 1 returned + 1 intermediate

func getBrokerType(instanceType string) types.BrokerType {
	if strings.HasPrefix(instanceType, "express.") {
		return types.BrokerTypeExpress
	}
	return types.BrokerTypeStandard
}

// isPartial reports whether a window response was truncated by the datapoint cap.
// The per-result PartialData status is the primary, reliable signal; the
// MaxMetricsExceeded top-level message is a documented secondary signal.
func isPartial(out *cloudwatch.GetMetricDataOutput) bool {
	for _, r := range out.MetricDataResults {
		if r.StatusCode == cloudwatchtypes.StatusCodePartialData {
			return true
		}
	}
	for _, m := range out.Messages {
		if aws.ToString(m.Code) == "MaxMetricsExceeded" {
			return true
		}
	}
	return false
}

// collectWindow fetches one window into the stitcher. If the response is partial
// and the window spans more than one period, it bisects and recurses; at the
// one-period floor it records a partial flag (warned once by executeChunkedQuery).
func (ms *MetricService) collectWindow(ctx context.Context, queries []cloudwatchtypes.MetricDataQuery, start, end time.Time, period int32, st *resultStitcher) error {
	out, err := ms.executeWindow(ctx, queries, start, end)
	if err != nil {
		return err
	}
	if isPartial(out) {
		windowSeconds := int64(end.Sub(start).Seconds())
		// Snap the split to a period boundary so neither half re-buckets the
		// datapoint straddling mid (CloudWatch aligns buckets from each call's
		// StartTime). If the window is too small to split into two period-aligned
		// halves, we can't reduce further — record partial and keep what we got.
		halfPeriods := (windowSeconds / 2) / int64(period)
		if windowSeconds > int64(period) && halfPeriods >= 1 {
			mid := start.Add(time.Duration(halfPeriods*int64(period)) * time.Second)
			if mid.After(start) && mid.Before(end) {
				if err := ms.collectWindow(ctx, queries, start, mid, period, st); err != nil {
					return err
				}
				return ms.collectWindow(ctx, queries, mid, end, period, st)
			}
		}
		st.markPartial()
	}
	st.add(out.MetricDataResults)
	return nil
}

// executeChunkedQuery fetches metrics across [startTime, endTime), splitting into
// sub-windows sized to stay under datapointBudget for seriesEstimate series at the
// given period (seriesEstimate <= 0 means "unknown" -> full window + fallback).
// Results are stitched per Id; a single warning is emitted if any data remained
// partial. label identifies the query group/cluster in that warning.
func (ms *MetricService) executeChunkedQuery(ctx context.Context, queries []cloudwatchtypes.MetricDataQuery, startTime, endTime time.Time, period int32, seriesEstimate int, label string) (*cloudwatch.GetMetricDataOutput, error) {
	if len(queries) == 0 || !endTime.After(startTime) {
		return &cloudwatch.GetMetricDataOutput{}, nil
	}

	st := newResultStitcher()
	cs := chunkSeconds(period, seriesEstimate)
	totalSeconds := int64(endTime.Sub(startTime).Seconds())

	if cs <= 0 || totalSeconds <= cs {
		if err := ms.collectWindow(ctx, queries, startTime, endTime, period, st); err != nil {
			return nil, err
		}
	} else {
		for chunkStart := startTime; chunkStart.Before(endTime); {
			chunkEnd := chunkStart.Add(time.Duration(cs) * time.Second)
			if chunkEnd.After(endTime) {
				chunkEnd = endTime
			}
			if err := ms.collectWindow(ctx, queries, chunkStart, chunkEnd, period, st); err != nil {
				return nil, err
			}
			chunkStart = chunkEnd
		}
	}

	if st.partial {
		slog.Warn("metrics may be incomplete: CloudWatch returned partial data at the minimum window",
			"query", label, "period", period, "start", startTime, "end", endTime)
	}
	return st.output(), nil
}
