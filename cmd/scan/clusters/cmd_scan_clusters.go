package clusters

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/iampolicy"
	jmx "github.com/confluentinc/kcp/internal/services/jmx"
	prometheussvc "github.com/confluentinc/kcp/internal/services/prometheus"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/msk"
	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile       string
	credentialsFile string
	sourceType      string
	skipTopics      bool
	skipACLs        bool
	metricsSource   string
	metricsDuration string
	metricsInterval string
	metricsRange    string
)

func scanClustersIAMAnnotation() string {
	return iampolicy.RenderStatements(
		"Only required for `--source-type msk`. OSK scans use credentials from the credentials file, not AWS IAM.",
		[]iampolicy.Statement{
			{
				Sid: "MSKClusterKafkaAccess",
				Actions: []string{
					"kafka-cluster:Connect",
					"kafka-cluster:DescribeCluster",
					"kafka-cluster:DescribeClusterDynamicConfiguration",
					"kafka-cluster:DescribeTopic",
				},
				Resources: []string{
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/*",
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>",
				},
			},
			{
				Sid:     "MSKConnectTopicAccess",
				Actions: []string{"kafka-cluster:ReadData"},
				Resources: []string{
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-configs",
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-status",
				},
			},
		},
	)
}

func NewScanClustersCmd() *cobra.Command {
	scanClustersCmd := &cobra.Command{
		Use:   "clusters",
		Short: "Scan Kafka clusters using the Kafka Admin API",
		Long:  "Scans MSK or OSK clusters to discover topics, ACLs, and other metadata via Kafka Admin API",
		Example: `  # Scan an MSK cluster (credentials from kcp discover)
  kcp scan clusters --source-type msk --state-file kcp-state.json --credentials-file msk-credentials.yaml

  # Scan an OSK cluster (hand-authored credentials)
  kcp scan clusters --source-type osk --state-file kcp-state.json --credentials-file osk-credentials.yaml

  # OSK with live Jolokia metric collection
  kcp scan clusters --source-type osk --state-file kcp-state.json \
      --credentials-file osk-credentials.yaml \
      --metrics jolokia --metrics-duration 5m --metrics-interval 10s

  # OSK with historical Prometheus metrics
  kcp scan clusters --source-type osk --state-file kcp-state.json \
      --credentials-file osk-credentials.yaml \
      --metrics prometheus --metrics-range 30d`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: scanClustersIAMAnnotation(),
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanClusters,
		RunE:          runScanClusters,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&sourceType, "source-type", "", "Source type: 'msk' or 'osk' (required)")
	requiredFlags.StringVar(&stateFile, "state-file", "kcp-state.json", "Path to the KCP state file")
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Path to credentials file (msk-credentials.yaml or osk-credentials.yaml)")
	scanClustersCmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&skipTopics, "skip-topics", false, "Skip topic discovery")
	optionalFlags.BoolVar(&skipACLs, "skip-acls", false, "Skip ACL discovery")
	scanClustersCmd.Flags().AddFlagSet(optionalFlags)

	metricsFlags := pflag.NewFlagSet("metrics", pflag.ExitOnError)
	metricsFlags.SortFlags = false
	metricsFlags.StringVar(&metricsSource, "metrics", "", "Metrics collection source: 'jolokia' or 'prometheus' (OSK only)")
	metricsFlags.StringVar(&metricsDuration, "metrics-duration", "", "Duration to poll Jolokia (e.g. 10m, 1h). Required with --metrics jolokia.")
	metricsFlags.StringVar(&metricsInterval, "metrics-interval", "10s", "Polling interval for Jolokia (e.g. 10s, 30s). Default: 10s.")
	metricsFlags.StringVar(&metricsRange, "metrics-range", "", "Time range to query from Prometheus (e.g. 7d, 30d). Required with --metrics prometheus.")
	scanClustersCmd.Flags().AddFlagSet(metricsFlags)

	_ = scanClustersCmd.MarkFlagRequired("source-type")
	_ = scanClustersCmd.MarkFlagRequired("credentials-file")

	return scanClustersCmd
}

func preRunScanClusters(cmd *cobra.Command, args []string) error {
	// Bind environment variables to flags
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// Validate source type
	if sourceType != "msk" && sourceType != "osk" {
		return fmt.Errorf("invalid source-type '%s': must be 'msk' or 'osk'", sourceType)
	}

	// Validate credentials file naming convention
	if sourceType == "msk" && filepath.Base(credentialsFile) != "msk-credentials.yaml" {
		slog.Warn("credentials file should be named 'msk-credentials.yaml' for MSK sources", "file", credentialsFile)
	}
	if sourceType == "osk" && filepath.Base(credentialsFile) != "osk-credentials.yaml" {
		slog.Warn("credentials file should be named 'osk-credentials.yaml' for OSK sources", "file", credentialsFile)
	}

	// Validate metrics flags
	if metricsSource != "" {
		if sourceType != "osk" {
			return fmt.Errorf("--metrics is only supported for OSK sources (--source-type osk)")
		}
		switch metricsSource {
		case "jolokia":
			if metricsDuration == "" {
				return fmt.Errorf("--metrics-duration is required when --metrics jolokia is set")
			}
			if _, err := time.ParseDuration(metricsDuration); err != nil {
				return fmt.Errorf("invalid --metrics-duration '%s': %w", metricsDuration, err)
			}
			if _, err := time.ParseDuration(metricsInterval); err != nil {
				return fmt.Errorf("invalid --metrics-interval '%s': %w", metricsInterval, err)
			}
			duration, _ := time.ParseDuration(metricsDuration)
			interval, _ := time.ParseDuration(metricsInterval)
			if duration <= interval {
				return fmt.Errorf("--metrics-duration (%s) must be greater than --metrics-interval (%s) to collect at least one data point", metricsDuration, metricsInterval)
			}
			if cmd.Flags().Changed("metrics-range") {
				return fmt.Errorf("--metrics-range cannot be used with --metrics jolokia")
			}
		case "prometheus":
			if metricsRange == "" {
				return fmt.Errorf("--metrics-range is required when --metrics prometheus is set")
			}
			if _, err := parseDurationDays(metricsRange); err != nil {
				return fmt.Errorf("invalid --metrics-range '%s': must be like 1d, 7d, 30d", metricsRange)
			}
			if cmd.Flags().Changed("metrics-duration") {
				return fmt.Errorf("--metrics-duration cannot be used with --metrics prometheus")
			}
			if cmd.Flags().Changed("metrics-interval") {
				return fmt.Errorf("--metrics-interval cannot be used with --metrics prometheus")
			}
		default:
			return fmt.Errorf("invalid --metrics '%s': must be 'jolokia' or 'prometheus'", metricsSource)
		}
	}

	return nil
}

func runScanClusters(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load or create state file
	state, err := loadOrCreateState(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load state file: %w", err)
	}

	// Create appropriate source based on source-type flag
	var source sources.Source
	switch sourceType {
	case "msk":
		source = msk.NewMSKSource()
	case "osk":
		source = osk.NewOSKSource()
	default:
		return fmt.Errorf("unsupported source type: %s", sourceType)
	}

	// Load credentials
	if err := source.LoadCredentials(credentialsFile); err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	// Display clusters to be scanned
	clusters := source.GetClusters()
	slog.Info("clusters to scan", "count", len(clusters), "source", sourceType)
	for _, cluster := range clusters {
		slog.Info("cluster", "name", cluster.Name, "id", cluster.UniqueID)
	}

	// Perform scan
	scanOpts := sources.ScanOptions{
		SkipTopics: skipTopics,
		SkipACLs:   skipACLs,
		State:      state,
	}

	slog.Info("starting cluster scan", "source", sourceType)
	scanResult, err := source.Scan(ctx, scanOpts)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Merge scan results into state
	if err := mergeResultsIntoState(state, scanResult); err != nil {
		return fmt.Errorf("failed to merge scan results: %w", err)
	}

	// Collect metrics if enabled
	if metricsSource != "" && sourceType == "osk" {
		if err := collectMetrics(ctx, state, credentialsFile); err != nil {
			slog.Warn("metrics collection failed", "error", err)
			fmt.Printf("\n⚠️  Metrics collection failed: %v\n", err)
		}
	}

	// Save updated state
	if err := state.PersistStateFile(stateFile); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	slog.Info("scan completed successfully", "clusters", len(scanResult.Clusters), "state_file", stateFile)
	fmt.Printf("\n✅ Scan completed successfully\n")
	fmt.Printf("   Scanned %d cluster(s)\n", len(scanResult.Clusters))
	fmt.Printf("   State file: %s\n\n", stateFile)

	return nil
}

// loadOrCreateState loads existing state or creates a new one.
// Only creates a new state when the file does not exist — all other errors
// (corrupt JSON, permission denied, etc.) are returned to the caller to
// avoid silently discarding an existing state file.
func loadOrCreateState(stateFilePath string) (*types.State, error) {
	if _, err := os.Stat(stateFilePath); os.IsNotExist(err) {
		slog.Info("creating new state file", "file", stateFilePath)
		state := types.NewStateFrom(nil)
		state.SchemaRegistries = &types.SchemaRegistriesState{}
		return state, nil
	}

	state, err := types.NewStateFromFile(stateFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load state file: %w", err)
	}
	slog.Info("loaded existing state file", "file", stateFilePath)
	return state, nil
}

// mergeResultsIntoState merges scan results into the state file
func mergeResultsIntoState(state *types.State, result *sources.ScanResult) error {
	switch result.SourceType {
	case types.SourceTypeMSK:
		return mergeMSKResults(state, result)
	case types.SourceTypeOSK:
		return mergeOSKResults(state, result)
	default:
		return fmt.Errorf("unsupported source type: %s", result.SourceType)
	}
}

// mergeMSKResults merges MSK scan results into state
func mergeMSKResults(state *types.State, result *sources.ScanResult) error {
	if state.MSKSources == nil {
		return fmt.Errorf("no MSK sources in state; run 'kcp discover' before scanning MSK clusters")
	}

	// Index scanned results by ARN for O(1) lookup
	scannedByARN := make(map[string]*types.KafkaAdminClientInformation, len(result.Clusters))
	for i := range result.Clusters {
		c := &result.Clusters[i]
		scannedByARN[c.Identifier.UniqueID] = c.KafkaAdminInfo
	}

	// Apply results into state in-place
	for i := range state.MSKSources.Regions {
		for j := range state.MSKSources.Regions[i].Clusters {
			arn := state.MSKSources.Regions[i].Clusters[j].Arn
			if info, ok := scannedByARN[arn]; ok {
				state.MSKSources.Regions[i].Clusters[j].KafkaAdminClientInformation = *info
			}
		}
	}

	slog.Info("merged MSK scan results", "clusters_scanned", len(result.Clusters))
	return nil
}

// mergeOSKResults merges OSK scan results into state
func mergeOSKResults(state *types.State, result *sources.ScanResult) error {
	if state.OSKSources == nil {
		state.OSKSources = &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{},
		}
	}

	// Build index of existing clusters by ID for efficient lookup
	existingIndex := make(map[string]int)
	for i := range state.OSKSources.Clusters {
		existingIndex[state.OSKSources.Clusters[i].ID] = i
	}

	// Separate scan results into updates and new clusters to avoid pointer
	// invalidation: appending to the slice may reallocate the backing array,
	// which would invalidate any pointers taken before the append.
	var newClusters []types.OSKDiscoveredCluster

	for _, clusterResult := range result.Clusters {
		metadata, ok := clusterResult.SourceSpecificData.(types.OSKClusterMetadata)
		if !ok {
			return fmt.Errorf("invalid source-specific data for OSK cluster")
		}

		newCluster := types.OSKDiscoveredCluster{
			ID:                          clusterResult.Identifier.UniqueID,
			BootstrapServers:            clusterResult.Identifier.BootstrapServers,
			KafkaAdminClientInformation: *clusterResult.KafkaAdminInfo,
			Metadata:                    metadata,
		}

		if idx, exists := existingIndex[newCluster.ID]; exists {
			existing := state.OSKSources.Clusters[idx]

			// Merge KafkaAdminClientInformation: preserve old topics/ACLs/connectors
			// if the new scan returned empty results (e.g. transient permission failure)
			newCluster.KafkaAdminClientInformation.MergeFrom(existing.KafkaAdminClientInformation)

			// Preserve discovered clients and metrics from prior scans
			newCluster.DiscoveredClients = existing.DiscoveredClients
			if newCluster.ClusterMetrics == nil {
				newCluster.ClusterMetrics = existing.ClusterMetrics
			}

			// Update in-place by index
			state.OSKSources.Clusters[idx] = newCluster
		} else {
			newClusters = append(newClusters, newCluster)
		}
	}

	// Append new clusters after all in-place updates are done
	state.OSKSources.Clusters = append(state.OSKSources.Clusters, newClusters...)

	slog.Info("merged OSK scan results", "clusters", len(result.Clusters))
	return nil
}

func collectMetrics(ctx context.Context, state *types.State, credentialsFilePath string) error {
	creds, errs := types.NewOSKCredentialsFromFile(credentialsFilePath)
	if len(errs) > 0 {
		return fmt.Errorf("failed to reload credentials: %v", errs)
	}

	for _, clusterCreds := range creds.Clusters {
		var metrics *types.ProcessedClusterMetrics
		var err error

		switch metricsSource {
		case "jolokia":
			metrics, err = collectJolokiaMetrics(ctx, clusterCreds)
		case "prometheus":
			metrics, err = collectPrometheusMetrics(ctx, clusterCreds)
		}

		if err != nil {
			slog.Warn("metrics collection failed", "cluster", clusterCreds.ID, "source", metricsSource, "error", err)
			continue
		}

		oskCluster, err := state.GetOSKClusterByID(clusterCreds.ID)
		if err != nil {
			slog.Warn("cluster not found in state", "cluster", clusterCreds.ID, "error", err)
			continue
		}
		oskCluster.ClusterMetrics = metrics

		fmt.Printf("   ✅ Collected %d data points for cluster '%s'\n", len(metrics.Metrics), clusterCreds.ID)
	}

	return nil
}

func collectJolokiaMetrics(ctx context.Context, clusterCreds types.OSKClusterAuth) (*types.ProcessedClusterMetrics, error) {
	if !clusterCreds.HasJolokiaConfig() {
		return nil, fmt.Errorf("no jolokia config for cluster %s", clusterCreds.ID)
	}

	duration, _ := time.ParseDuration(metricsDuration)
	interval, _ := time.ParseDuration(metricsInterval)

	slog.Info("collecting Jolokia metrics", "cluster", clusterCreds.ID, "duration", duration, "interval", interval)
	fmt.Printf("\n📊 Collecting Jolokia metrics for cluster '%s' (duration: %s, interval: %s)...\n", clusterCreds.ID, duration, interval)

	var jolokiaOpts []client.JolokiaOption
	if clusterCreds.Jolokia.Auth != nil {
		jolokiaOpts = append(jolokiaOpts, client.WithJolokiaBasicAuth(clusterCreds.Jolokia.Auth.Username, clusterCreds.Jolokia.Auth.Password))
	}
	if clusterCreds.Jolokia.TLS != nil {
		jolokiaOpts = append(jolokiaOpts, client.WithJolokiaTLS(clusterCreds.Jolokia.TLS.CACert, clusterCreds.Jolokia.TLS.InsecureSkipVerify))
	}

	jmxService := jmx.NewJMXService(clusterCreds.Jolokia.Endpoints, jolokiaOpts...)
	return jmxService.CollectOverDuration(ctx, duration, interval)
}

func collectPrometheusMetrics(ctx context.Context, clusterCreds types.OSKClusterAuth) (*types.ProcessedClusterMetrics, error) {
	if !clusterCreds.HasPrometheusConfig() {
		return nil, fmt.Errorf("no prometheus config for cluster %s", clusterCreds.ID)
	}

	queryRange, _ := parseDurationDays(metricsRange)

	slog.Info("collecting Prometheus metrics", "cluster", clusterCreds.ID, "range", metricsRange)
	fmt.Printf("\n📊 Collecting Prometheus metrics for cluster '%s' (range: %s)...\n", clusterCreds.ID, metricsRange)

	var promOpts []client.PrometheusOption
	if clusterCreds.Prometheus.Auth != nil {
		promOpts = append(promOpts, client.WithPrometheusBasicAuth(
			clusterCreds.Prometheus.Auth.Username,
			clusterCreds.Prometheus.Auth.Password,
		))
	}
	if clusterCreds.Prometheus.TLS != nil {
		promOpts = append(promOpts, client.WithPrometheusTLS(
			clusterCreds.Prometheus.TLS.CACert,
			clusterCreds.Prometheus.TLS.InsecureSkipVerify,
		))
	}

	promClient := client.NewPrometheusClient(clusterCreds.Prometheus.URL, promOpts...)
	promService := prometheussvc.NewPrometheusService(promClient)
	return promService.CollectMetrics(ctx, queryRange)
}

// parseDurationDays parses duration strings like "1d", "7d", "30d" into time.Duration
func parseDurationDays(s string) (time.Duration, error) {
	if len(s) < 2 || s[len(s)-1] != 'd' {
		return 0, fmt.Errorf("must end with 'd' (e.g. 7d, 30d)")
	}
	days, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || days <= 0 {
		return 0, fmt.Errorf("invalid number of days")
	}
	return time.Duration(days) * 24 * time.Hour, nil
}
