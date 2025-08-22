package scan

import (
	"context"
	"log/slog"
	"time"

	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	rrm "github.com/confluentinc/kcp/internal/generators/report/cluster/metrics"
	cs "github.com/confluentinc/kcp/internal/generators/scan/cluster"
	"github.com/confluentinc/kcp/internal/generators/scan/region"
	"github.com/confluentinc/kcp/internal/services/cost"
	"github.com/confluentinc/kcp/internal/services/ec2"
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/services/msk"
)

type ScanOpts struct {
	Regions []string
}

type Scanner struct {
	regions   []string
	skipKafka bool
}

func NewScanner(opts ScanOpts) *Scanner {
	return &Scanner{
		regions: opts.Regions,
	}
}

func (rs *Scanner) Run() error {

	for _, r := range rs.regions {

		// create the msk client and service
		mskClient, err := client.NewMSKClient(r)
		if err != nil {
			slog.Error("failed to create msk client", "region", r, "error", err)
			continue
		}
		mskService := msk.NewMSKService(mskClient)

		// default the time period
		now := time.Now()
		startDate := now.AddDate(0, 0, -31).UTC().Truncate(24 * time.Hour)
		endDate := now.UTC().Truncate(24 * time.Hour)

		// scan the region
		regionScanOpts := region.ScanRegionOpts{
			Region: r,
		}

		regionScanner := region.NewRegionScanner(mskClient, regionScanOpts)
		regionScanResult, err := regionScanner.ScanRegion(context.Background())
		if err != nil {
			slog.Error("failed to scan region", "region", r, "error", err)
		} else {
			if err := regionScanResult.WriteAsJson(); err != nil {
				slog.Error("failed to write region scan result", "region", r, "error", err)
			}

			if err := regionScanResult.WriteAsMarkdown(true); err != nil {
				slog.Error("failed to write region scan result", "region", r, "error", err)
			}
		}

		// get region costs
		costExplorerClient, err := client.NewCostExplorerClient(r)
		if err != nil {
			slog.Error("failed to create cost explorer client", "region", r, "error", err)
		}

		costService := cost.NewCostService(costExplorerClient)

		costs, err := costService.GetCostsForTimeRange(r, startDate, endDate, costexplorertypes.GranularityDaily, map[string][]string{})
		if err != nil {
			slog.Error("failed to get region costs", "region", r, "error", err)
		} else {

			if err := costs.WriteAsJson(); err != nil {
				slog.Error("failed to write region costs", "region", r, "error", err)
			}

			if err := costs.WriteAsMarkdown(true); err != nil {
				slog.Error("failed to write region costs", "region", r, "error", err)
			}
		}

		// scan clusters within the region
		if regionScanResult != nil {

			ec2Service, err := ec2.NewEC2Service(r)
			if err != nil {
				slog.Error("failed to create ec2 service", "region", r, "error", err)
				continue
			}

			for _, cluster := range regionScanResult.Clusters {
				// scan the cluster
				clusterScannerOpts := cs.ClusterScannerOpts{
					Region:     r,
					ClusterArn: cluster.ClusterARN,
					SkipKafka:  true,
				}

				kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
					return nil, nil
				}

				clusterScanner := cs.NewClusterScanner(mskService, ec2Service, kafkaAdminFactory, clusterScannerOpts)
				clusterScanResult, err := clusterScanner.ScanCluster(context.Background())
				if err != nil {
					slog.Error("failed to scan cluster", "cluster", cluster.ClusterARN, "error", err)
				} else {
					if err := clusterScanResult.WriteAsJson(); err != nil {
						slog.Error("failed to write cluster scan result", "cluster", cluster.ClusterARN, "error", err)
					}

					if err := clusterScanResult.WriteAsMarkdown(true); err != nil {
						slog.Error("failed to write cluster scan result", "cluster", cluster.ClusterARN, "error", err)
					}
				}

				// get the cluster metrics
				metricsOpts := rrm.ClusterMetricsOpts{
					Region:     r,
					StartDate:  startDate,
					EndDate:    endDate,
					ClusterArn: cluster.ClusterARN,
					SkipKafka:  true,
				}

				cloudWatchClient, err := client.NewCloudWatchClient(metricsOpts.Region)
				if err != nil {
					slog.Error("failed to create cloudWatch client", "region", r, "error", err)
					continue
				}

				metricService := metrics.NewMetricService(cloudWatchClient, metricsOpts.StartDate, metricsOpts.EndDate)

				clusterMetrics := rrm.NewClusterMetrics(mskService, metricService, kafkaAdminFactory, metricsOpts)
				clusterMetricsResult, err := clusterMetrics.ProcessCluster()

				if err != nil {
					slog.Error("failed to scan cluster metrics", "cluster", cluster.ClusterARN, "error", err)
				} else {
					if err := clusterMetricsResult.WriteAsJson(); err != nil {
						slog.Error("failed to write cluster metrics result", "cluster", cluster.ClusterARN, "error", err)
					}

					if err := clusterMetricsResult.WriteAsMarkdown(true); err != nil {
						slog.Error("failed to write cluster metrics result", "cluster", cluster.ClusterARN, "error", err)
					}
				}

			}
		}
	}

	return nil
}
