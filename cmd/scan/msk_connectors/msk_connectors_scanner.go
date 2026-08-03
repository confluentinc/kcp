package msk_connectors

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/services/msk_connect"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// MSKConnectService is the subset of the AWS kafkaconnect API this scanner needs.
type MSKConnectService interface {
	ListConnectors(ctx context.Context, params *kafkaconnect.ListConnectorsInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error)
	DescribeConnector(ctx context.Context, params *kafkaconnect.DescribeConnectorInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error)
}

type MSKConnectorsScannerOpts struct {
	StateFile          string
	State              *types.State
	Regions            []string
	ClusterArns        []string
	MetricsGranularity string // 60s|5m|1h|1d to collect CloudWatch metrics; "" = skip metrics
}

// ConnectorMetricsCollector fetches raw AWS/KafkaConnect metrics for connectors.
type ConnectorMetricsCollector interface {
	CollectConnectorMetrics(ctx context.Context, connectorNames []string, tw types.CloudWatchTimeWindow, region string) (*types.ClusterMetrics, error)
}

type MSKConnectorsScanner struct {
	stateFile          string
	state              *types.State
	regions            []string
	clusterArns        []string
	metricsGranularity string

	// newService builds the AWS MSK Connect service for a region. Overridable in tests.
	newService func(region string) (MSKConnectService, error)

	// newMetricsCollector builds the CloudWatch metrics collector for a region. Overridable in tests.
	newMetricsCollector func(region string) (ConnectorMetricsCollector, error)
}

func NewMSKConnectorsScanner(opts MSKConnectorsScannerOpts) *MSKConnectorsScanner {
	return &MSKConnectorsScanner{
		stateFile:          opts.StateFile,
		state:              opts.State,
		regions:            opts.Regions,
		clusterArns:        opts.ClusterArns,
		metricsGranularity: opts.MetricsGranularity,
		newService: func(region string) (MSKConnectService, error) {
			c, err := client.NewMSKConnectClient(region)
			if err != nil {
				return nil, err
			}
			return msk_connect.NewMSKConnectService(c), nil
		},
		newMetricsCollector: func(region string) (ConnectorMetricsCollector, error) {
			cw, err := client.NewCloudWatchClient(region)
			if err != nil {
				return nil, err
			}
			return metrics.NewMetricService(cw), nil
		},
	}
}

// matchConnectorsForCluster lists MSK Connect connectors and returns those whose
// bootstrap servers match this cluster. Sensitive config values are redacted
// before the connector summary is built, so raw secrets never enter the state
// file or logs. All failures are non-fatal: a warning is logged and connector
// discovery is skipped rather than aborting the run (R3).
func (s *MSKConnectorsScanner) matchConnectorsForCluster(ctx context.Context, svc MSKConnectService, awsClientInfo *types.AWSClientInformation) []types.ConnectorSummary {
	fmt.Printf("  🔍 Scanning for matching connectors\n")
	var matchingConnectors []types.ConnectorSummary

	totalRedacted := 0
	var input kafkaconnect.ListConnectorsInput
	for {
		mskConnectResult, err := svc.ListConnectors(ctx, &input)
		if err != nil {
			slog.Warn("failed to list MSK Connect connectors; skipping remaining connector discovery", "error", err)
			return matchingConnectors
		}

		for _, connector := range mskConnectResult.Connectors {
			if connector.KafkaClusterClientAuthentication == nil ||
				connector.KafkaCluster == nil || connector.KafkaCluster.ApacheKafkaCluster == nil ||
				connector.Capacity == nil || connector.CreationTime == nil {
				slog.Warn("skipping connector with incomplete connector summary", "connectorArn", aws.ToString(connector.ConnectorArn))
				continue
			}

			authType, err := connectorAuthType(connector)
			if err != nil {
				slog.Warn("skipping connector with unsupported auth/encryption type", "connectorArn", aws.ToString(connector.ConnectorArn), "error", err)
				continue
			}

			brokerAddresses, err := awsClientInfo.GetAllBootstrapBrokersForAuthType(authType)
			if err != nil {
				slog.Warn("failed to resolve bootstrap brokers; skipping connector", "authType", authType, "error", err)
				continue
			}

			connectorBootstrap := aws.ToString(connector.KafkaCluster.ApacheKafkaCluster.BootstrapServers)
			if !bootstrapMatches(connectorBootstrap, brokerAddresses) {
				continue
			}

			describeConnector, err := svc.DescribeConnector(ctx, &kafkaconnect.DescribeConnectorInput{
				ConnectorArn: connector.ConnectorArn,
			})
			if err != nil {
				slog.Warn("failed to describe connector; skipping", "connectorArn", aws.ToString(connector.ConnectorArn), "error", err)
				continue
			}

			redactedConfig, redactedCount := redact.RedactStringMap(describeConnector.ConnectorConfiguration)
			totalRedacted += redactedCount

			fmt.Printf("    ✅ Found connector %s\n", aws.ToString(connector.ConnectorName))
			matchingConnectors = append(matchingConnectors, types.ConnectorSummary{
				ConnectorArn:                     aws.ToString(connector.ConnectorArn),
				ConnectorName:                    aws.ToString(connector.ConnectorName),
				ConnectorState:                   string(connector.ConnectorState),
				CreationTime:                     connector.CreationTime.Format(time.RFC3339),
				KafkaCluster:                     *connector.KafkaCluster.ApacheKafkaCluster,
				KafkaClusterClientAuthentication: *connector.KafkaClusterClientAuthentication,
				Capacity:                         *connector.Capacity,
				Plugins:                          describeConnector.Plugins,
				ConnectorConfiguration:           redactedConfig,
			})
		}

		if mskConnectResult.NextToken == nil {
			break
		}
		input.NextToken = mskConnectResult.NextToken
	}

	if totalRedacted > 0 {
		slog.Info("redacted sensitive connector config fields", "redacted_fields", totalRedacted, "connectors", len(matchingConnectors))
	}

	return matchingConnectors
}

func connectorAuthType(connector kafkaconnecttypes.ConnectorSummary) (types.AuthType, error) {
	switch connector.KafkaClusterClientAuthentication.AuthenticationType {
	case kafkaconnecttypes.KafkaClusterClientAuthenticationTypeIam:
		return types.AuthTypeIAM, nil
	case kafkaconnecttypes.KafkaClusterClientAuthenticationTypeNone:
		if connector.KafkaClusterEncryptionInTransit == nil {
			return "", fmt.Errorf("connector has no encryption-in-transit information")
		}
		switch connector.KafkaClusterEncryptionInTransit.EncryptionType {
		case kafkaconnecttypes.KafkaClusterEncryptionInTransitTypeTls:
			return types.AuthTypeUnauthenticatedTLS, nil
		case kafkaconnecttypes.KafkaClusterEncryptionInTransitTypePlaintext:
			return types.AuthTypeUnauthenticatedPlaintext, nil
		default:
			return "", fmt.Errorf("unsupported connector encryption type: %s", connector.KafkaClusterEncryptionInTransit.EncryptionType)
		}
	default:
		return "", fmt.Errorf("unsupported connector auth type: %s", connector.KafkaClusterClientAuthentication.AuthenticationType)
	}
}

func bootstrapMatches(connectorBootstrap string, brokerAddresses []string) bool {
	connectorHosts := make(map[string]struct{})
	for _, addr := range strings.Split(connectorBootstrap, ",") {
		if trimmed := strings.TrimSpace(addr); trimmed != "" {
			connectorHosts[trimmed] = struct{}{}
		}
	}
	for _, brokerAddress := range brokerAddresses {
		if _, ok := connectorHosts[strings.TrimSpace(brokerAddress)]; ok {
			return true
		}
	}
	return false
}

// Run scans MSK-managed connectors for the targeted clusters and updates state.
// Region mode targets every cluster in each region; cluster-arn mode narrows to
// the named ARNs (already validated present in state). A per-region MSK Connect
// service is created once and reused across that region's clusters.
func (s *MSKConnectorsScanner) Run() error {
	if s.state == nil || s.state.MSKSources == nil {
		return fmt.Errorf("no MSK sources found in state file")
	}

	targetArns := map[string]bool{}
	for _, a := range s.clusterArns {
		targetArns[a] = true
	}
	regionSet := map[string]bool{}
	for _, r := range s.regions {
		regionSet[r] = true
	}

	fmt.Printf("🚀 Starting managed connector scan\n")

	updated := 0
	for ri := range s.state.MSKSources.Regions {
		region := &s.state.MSKSources.Regions[ri]
		if !regionSet[region.Name] {
			continue
		}

		var svc MSKConnectService
		for ci := range region.Clusters {
			cluster := &region.Clusters[ci]
			if len(targetArns) > 0 && !targetArns[cluster.Arn] {
				continue
			}

			if svc == nil {
				var err error
				svc, err = s.newService(region.Name)
				if err != nil {
					slog.Warn("failed to create MSK Connect service; skipping region", "region", region.Name, "error", err)
					break
				}
			}

			fmt.Printf("  🔍 Scanning connectors for cluster %s\n", cluster.Arn)
			matched := s.matchConnectorsForCluster(context.Background(), svc, &cluster.AWSClientInformation)
			cluster.AWSClientInformation.Connectors = types.MergeConnectors(matched, cluster.AWSClientInformation.Connectors)
			updated++

			if s.metricsGranularity != "" && len(cluster.AWSClientInformation.Connectors) > 0 {
				if err := s.collectAndStoreConnectorMetrics(region.Name, cluster); err != nil {
					// Non-fatal (R3): metrics are best-effort; connectors are already persisted.
					slog.Warn("failed to collect connector metrics; continuing", "cluster", cluster.Arn, "error", err)
				}
			}
		}
	}

	if err := s.state.PersistStateFile(s.stateFile); err != nil {
		return fmt.Errorf("failed to save state file: %v", err)
	}

	fmt.Printf("✅ Managed connector scan complete (%d clusters)\n", updated)
	return nil
}

// collectAndStoreConnectorMetrics fetches CloudWatch metrics for the cluster's
// matched connectors, flattens and aggregates them, and stores the result on
// the cluster's AWSClientInformation. Metrics collection is best-effort: any
// failure is returned to the caller, which logs a warning and continues (R3) —
// connectors are already persisted by the time this is called.
func (s *MSKConnectorsScanner) collectAndStoreConnectorMetrics(region string, cluster *types.DiscoveredCluster) error {
	collector, err := s.newMetricsCollector(region)
	if err != nil {
		return fmt.Errorf("failed to create metrics collector: %v", err)
	}
	names := make([]string, 0, len(cluster.AWSClientInformation.Connectors))
	for _, c := range cluster.AWSClientInformation.Connectors {
		names = append(names, c.ConnectorName)
	}

	// Same granularity→window model as `kcp discover` (period + CloudWatch
	// retention-bounded range): 60s→15d, 5m→63d, 1h/1d→365d.
	tw, err := metrics.GetTimeWindowForGranularity(time.Now().UTC(), s.metricsGranularity)
	if err != nil {
		return err
	}
	raw, err := collector.CollectConnectorMetrics(context.Background(), names, tw, region)
	if err != nil {
		return err
	}

	pcm := report.FlattenClusterMetrics(*raw)
	pcm.Aggregates = report.CalculateMetricsAggregates(pcm.Metrics)

	cluster.AWSClientInformation.ConnectorMetrics = &types.ConnectClusterMetrics{
		Metadata: types.ConnectMetricMetadata{
			StartDate:     raw.MetricMetadata.StartDate,
			EndDate:       raw.MetricMetadata.EndDate,
			Period:        raw.MetricMetadata.Period,
			MetricsSource: types.MetricBackendCloudWatch,
		},
		Metrics:    pcm.Metrics,
		Aggregates: pcm.Aggregates,
		QueryInfo:  pcm.QueryInfo,
	}
	fmt.Printf("  ✅ Collected connector metrics for cluster %s\n", cluster.Arn)
	return nil
}
