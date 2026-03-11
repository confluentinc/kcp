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

	brokerQueries, brokerQueryInfos := ms.buildBrokerMetricQueries(*cluster.ClusterName, timeWindow.Period)
	brokerQueryResult, err := ms.executeMetricQuery(ctx, brokerQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	clientConnectionQueries, clientConnQueryInfos := ms.buildClientConnectionQueries(*cluster.ClusterName, timeWindow.Period)
	clientConnectionQueryResult, err := ms.executeMetricQuery(ctx, clientConnectionQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	clusterQueries, clusterQueryInfos := ms.buildClusterMetricQueries(*cluster.ClusterName, timeWindow.Period)
	clusterQueryResult, err := ms.executeMetricQuery(ctx, clusterQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	// for express brokers there is no storage info
	if brokerType == types.BrokerTypeExpress {
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

	clusterVolumeSizeGB := int(*cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
	localStorageQueries, localStorageQueryInfos := ms.buildLocalStorageUsageQuery(*cluster.ClusterName, timeWindow.Period, clusterVolumeSizeGB)
	storageQueryResult, err := ms.executeMetricQuery(ctx, localStorageQueries, timeWindow.StartTime, timeWindow.EndTime)
	if err != nil {
		return nil, err
	}

	remoteStorageQueries, remoteStorageQueryInfos := ms.buildRemoteStorageUsageQuery(*cluster.ClusterName, timeWindow.Period)
	remoteStorageQueryResult, err := ms.executeMetricQuery(ctx, remoteStorageQueries, timeWindow.StartTime, timeWindow.EndTime)
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
	queries, queryInfos := ms.buildServerlessMetricQueries(*cluster.ClusterName, timeWindow.Period)
	populateCLICommands(queryInfos, queries, timeWindow.StartTime, timeWindow.EndTime, regionFromArn(cluster.ClusterArn))

	// Execute the metric query
	queryResult, err := ms.executeMetricQuery(ctx, queries, timeWindow.StartTime, timeWindow.EndTime)
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
	}
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
