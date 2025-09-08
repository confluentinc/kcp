package discover

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	rrm "github.com/confluentinc/kcp/internal/generators/report/cluster/metrics"
	cs "github.com/confluentinc/kcp/internal/generators/scan/cluster"
	sr "github.com/confluentinc/kcp/internal/generators/scan/region"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/cost"
	"github.com/confluentinc/kcp/internal/services/ec2"
	"github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/types"
)

type DiscovererOpts struct {
	Regions []string
}

type Discoverer struct {
	regions []string
}

func NewDiscoverer(opts DiscovererOpts) *Discoverer {
	return &Discoverer{
		regions: opts.Regions,
	}
}

func (d *Discoverer) Run() error {
	outputDir := "kcp-scan"

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create kcp discover output folder: %w", err)
	}

	if err := d.processRegions(outputDir); err != nil {
		slog.Error("failed to discover region", "error", err)
	}

	return nil
}

func (d *Discoverer) processRegions(outputDir string) error {
	regionEntries := []types.RegionEntry{}
	regionsWithoutClusters := []string{}

	for _, region := range d.regions {
		mskClient, err := client.NewMSKClient(region)
		if err != nil {
			slog.Error("failed to create msk client", "region", region, "error", err)
			continue
		}
		mskService := msk.NewMSKService(mskClient)

		// region scanning
		regionScanOpts := sr.ScanRegionOpts{
			Region: region,
		}

		mskConnectClient, err := client.NewMSKConnectClient(region)
		if err != nil {
			slog.Error("failed to create msk connect client", "region", region, "error", err)
			continue
		}
		regionScanner := sr.NewRegionScanner(mskClient, mskConnectClient, regionScanOpts)
		regionScanResult, err := regionScanner.ScanRegion(context.Background())
		if err != nil {
			slog.Error("failed to scan region", "region", region, "error", err)
		} else {
			if err := regionScanResult.WriteAsJson(); err != nil {
				slog.Error("failed to write region scan result", "region", region, "error", err)
			}

			if err := regionScanResult.WriteAsMarkdown(true); err != nil {
				slog.Error("failed to write region scan result", "region", region, "error", err)
			}
		}

		// get region costs
		costExplorerClient, err := client.NewCostExplorerClient(region)
		if err != nil {
			slog.Error("failed to create cost explorer client", "region", region, "error", err)
		}

		costService := cost.NewCostService(costExplorerClient)

		// default the time period
		now := time.Now()
		startDate := now.AddDate(0, 0, -31).UTC().Truncate(24 * time.Hour)
		endDate := now.UTC().Truncate(24 * time.Hour)

		costs, err := costService.GetCostsForTimeRange(region, startDate, endDate, costexplorertypes.GranularityDaily, map[string][]string{})
		if err != nil {
			slog.Error("failed to get region costs", "region", region, "error", err)
		} else {

			if err := costs.WriteAsJson(); err != nil {
				slog.Error("failed to write region costs", "region", region, "error", err)
			}

			if err := costs.WriteAsMarkdown(true); err != nil {
				slog.Error("failed to write region costs", "region", region, "error", err)
			}
		}

		// scan clusters within the region
		if regionScanResult != nil {
			ec2Service, err := ec2.NewEC2Service(region)
			if err != nil {
				slog.Error("failed to create ec2 service", "region", region, "error", err)
				continue
			}

			for _, cluster := range regionScanResult.Clusters {
				// scan the cluster
				clusterScannerOpts := cs.ClusterScannerOpts{
					Region:     region,
					ClusterArn: cluster.ClusterARN,
					// never want to attempt to connect to brokers in this discovery phase
					SkipKafka: true,
				}

				kafkaService := kafka.NewKafkaService(kafka.KafkaServiceOpts{})

				clusterScanner := cs.NewClusterScanner(mskService, ec2Service, kafkaService, clusterScannerOpts)
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
					Region:     region,
					StartDate:  startDate,
					EndDate:    endDate,
					ClusterArn: cluster.ClusterARN,
				}

				cloudWatchClient, err := client.NewCloudWatchClient(metricsOpts.Region)
				if err != nil {
					slog.Error("failed to create cloudWatch client", "region", region, "error", err)
					continue
				}

				metricService := metrics.NewMetricService(cloudWatchClient, metricsOpts.StartDate, metricsOpts.EndDate)

				clusterMetrics := rrm.NewClusterMetrics(mskService, metricService, metricsOpts)
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

		regionEntry, err := d.getRegionEntry(mskService, region)
		if err != nil {
			slog.Error("failed to get region entry", "region", region, "error", err)
			continue
		}

		if len(regionEntry.Clusters) == 0 {
			regionsWithoutClusters = append(regionsWithoutClusters, region)
		} else {
			regionEntries = append(regionEntries, *regionEntry)
		}
	}

	credsFilePath := filepath.Join(outputDir, "creds.yaml")
	credentials := types.Credentials{
		Regions: regionEntries,
	}

	if err := credentials.WriteToFile(credsFilePath); err != nil {
		return fmt.Errorf("failed to write creds.yaml file: %w", err)
	}

	if len(regionsWithoutClusters) > 0 {
		for _, region := range regionsWithoutClusters {
			slog.Info("no clusters found in region", "region", region)
		}
	}

	return nil
}

func (d *Discoverer) getRegionEntry(mskService *msk.MSKService, region string) (*types.RegionEntry, error) {
	clusters, err := mskService.ListClusters(context.Background(), 100)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %v", err)
	}

	clusterEntries := []types.ClusterEntry{}

	for _, cluster := range clusters {
		clusterEntry, err := d.getClusterEntry(cluster)
		if err != nil {
			slog.Error("failed to get cluster entry", "cluster", cluster.ClusterName, "error", err)
			continue
		}
		clusterEntries = append(clusterEntries, clusterEntry)
	}

	return &types.RegionEntry{
		Name:     region,
		Clusters: clusterEntries,
	}, nil

}

func (d *Discoverer) getClusterEntry(cluster kafkatypes.Cluster) (types.ClusterEntry, error) {
	clusterEntry := types.ClusterEntry{
		Name: aws.ToString(cluster.ClusterName),
		Arn:  aws.ToString(cluster.ClusterArn),
	}

	var isSaslIamEnabled, isSaslScramEnabled, isTlsEnabled, isUnauthenticatedEnabled bool

	switch cluster.ClusterType {
	case kafkatypes.ClusterTypeProvisioned:
		if cluster.Provisioned != nil && cluster.Provisioned.ClientAuthentication != nil {
			if cluster.Provisioned.ClientAuthentication.Sasl != nil &&
				cluster.Provisioned.ClientAuthentication.Sasl.Iam != nil {
				isSaslIamEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Sasl.Iam.Enabled)
			}

			if cluster.Provisioned.ClientAuthentication.Sasl != nil &&
				cluster.Provisioned.ClientAuthentication.Sasl.Scram != nil {
				isSaslScramEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Sasl.Scram.Enabled)
			}

			if cluster.Provisioned.ClientAuthentication.Tls != nil {
				isTlsEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Tls.Enabled)
			}

			if cluster.Provisioned.ClientAuthentication.Unauthenticated != nil {
				isUnauthenticatedEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Unauthenticated.Enabled)
			}
		}

	case kafkatypes.ClusterTypeServerless:
		// For serverless clusters, typically only IAM is supported
		isSaslIamEnabled = true
	}

	// we want a SINGLE auth mech to be enabled by default
	// priority is unauthenticated > iam > sasl_scram > tls
	defaultAuthSelected := false
	if isUnauthenticatedEnabled {
		clusterEntry.AuthMethod.Unauthenticated = &types.UnauthenticatedConfig{
			Use: !defaultAuthSelected,
		}
		defaultAuthSelected = true
	}
	if isSaslIamEnabled {
		clusterEntry.AuthMethod.IAM = &types.IAMConfig{
			Use: !defaultAuthSelected,
		}
		defaultAuthSelected = true
	}
	if isSaslScramEnabled {
		clusterEntry.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:      !defaultAuthSelected,
			Username: "",
			Password: "",
		}
		defaultAuthSelected = true
	}
	if isTlsEnabled {
		clusterEntry.AuthMethod.TLS = &types.TLSConfig{
			Use:        !defaultAuthSelected,
			CACert:     "",
			ClientCert: "",
			ClientKey:  "",
		}
		defaultAuthSelected = true
	}

	return clusterEntry, nil
}
