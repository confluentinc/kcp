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
)

type MetricService struct {
	client    *cloudwatch.Client
	startTime time.Time
	endTime   time.Time
}

func NewMetricService(client *cloudwatch.Client, startTime, endTime time.Time) *MetricService {
	return &MetricService{
		client:    client,
		startTime: startTime,
		endTime:   endTime,
	}
}

func (ms *MetricService) buildCloudWatchInput(clusterName, metricName string, node *int, statistics []cloudwatchtypes.Statistic) *cloudwatch.GetMetricStatisticsInput {
	// Calculate period in seconds based on the time range
	duration := ms.endTime.Sub(ms.startTime)
	period := int32(duration.Seconds())

	dimensions := []cloudwatchtypes.Dimension{
		{
			Name:  aws.String("Cluster Name"),
			Value: aws.String(clusterName),
		},
	}
	if node != nil && metricName != "GlobalTopicCount" {
		dimensions = append(dimensions, cloudwatchtypes.Dimension{
			Name:  aws.String("Broker ID"),
			Value: aws.String(strconv.Itoa(*node)),
		})
	}

	return &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/Kafka"),
		MetricName: aws.String(metricName),
		Dimensions: dimensions,
		StartTime:  aws.Time(ms.startTime),
		EndTime:    aws.Time(ms.endTime),
		Period:     aws.Int32(period),
		Statistics: statistics,
	}
}

func (ms *MetricService) GetAverageMetric(clusterName string, metricName string, node *int) (float64, error) {
	slog.Info("📊 getting cloudwatch average metric", "cluster", clusterName, "metric", metricName, "node", *node)
	metricRequest := ms.buildCloudWatchInput(clusterName, metricName, node, []cloudwatchtypes.Statistic{cloudwatchtypes.StatisticAverage})

	response, err := ms.client.GetMetricStatistics(context.Background(), metricRequest)
	if err != nil {
		return 0, fmt.Errorf("failed to get metric statistics: %v", err)
	}
	if len(response.Datapoints) == 0 {
		return 0, nil
	}
	return *response.Datapoints[0].Average, nil
}

func (ms *MetricService) GetPeakMetric(clusterName string, metricName string, node *int) (float64, error) {
	slog.Info("📈 getting cloudwatch peak metric", "cluster", clusterName, "metric", metricName, "node", *node)
	metricRequest := ms.buildCloudWatchInput(clusterName, metricName, node, []cloudwatchtypes.Statistic{cloudwatchtypes.StatisticMaximum})

	response, err := ms.client.GetMetricStatistics(context.Background(), metricRequest)
	if err != nil {
		return 0, fmt.Errorf("failed to get metric statistics: %v", err)
	}
	if len(response.Datapoints) == 0 {
		return 0, nil
	}
	return *response.Datapoints[0].Maximum, nil
}

func (ms *MetricService) GetServerlessAverageMetric(clusterName string, metricName string) (float64, error) {
	slog.Info("🔍 getting cloudwatch serverless average metric", "cluster", clusterName, "metric", metricName)
	return ms.getServerlessMetric(clusterName, metricName, cloudwatchtypes.StatisticAverage)
}

func (ms *MetricService) GetServerlessPeakMetric(clusterName string, metricName string) (float64, error) {
	slog.Info("🔍 getting cloudwatch serverless peak metric", "cluster", clusterName, "metric", metricName)
	return ms.getServerlessMetric(clusterName, metricName, cloudwatchtypes.StatisticMaximum)
}

func (ms *MetricService) getServerlessMetric(clusterName string, metricName string, statistic cloudwatchtypes.Statistic) (float64, error) {
	// Calculate period in seconds based on the time range
	duration := ms.endTime.Sub(ms.startTime)
	period := int32(duration.Seconds())

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
		output, err := ms.client.ListMetrics(context.Background(), listMetricsInput)
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
			StartTime:         aws.Time(ms.startTime),
			EndTime:           aws.Time(ms.endTime),
			ScanBy:            cloudwatchtypes.ScanByTimestampAscending,
		}

		output, err := ms.client.GetMetricData(context.Background(), input)
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

	return aggregatedSum, nil
}
