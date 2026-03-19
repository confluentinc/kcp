package clusters

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	jmx "github.com/confluentinc/kcp/internal/services/jmx"
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
	jmxEnabled      bool
	jmxScanDuration string
	jmxPollInterval string
)

func NewScanClustersCmd() *cobra.Command {
	scanClustersCmd := &cobra.Command{
		Use:           "clusters",
		Short:         "Scan Kafka clusters using the Kafka Admin API",
		Long:          "Scans MSK or OSK clusters to discover topics, ACLs, and other metadata via Kafka Admin API",
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

	jmxFlags := pflag.NewFlagSet("jmx", pflag.ExitOnError)
	jmxFlags.SortFlags = false
	jmxFlags.BoolVar(&jmxEnabled, "jmx", false, "Enable JMX metrics collection via Jolokia (OSK only)")
	jmxFlags.StringVar(&jmxScanDuration, "jmx-scan-duration", "", "Duration to collect JMX metrics (e.g. 5m, 30m, 1h). Required when --jmx is set.")
	jmxFlags.StringVar(&jmxPollInterval, "jmx-poll-interval", "10s", "Polling interval for JMX metrics collection (e.g. 5s, 10s, 30s)")
	scanClustersCmd.Flags().AddFlagSet(jmxFlags)

	scanClustersCmd.MarkFlagRequired("source-type")
	scanClustersCmd.MarkFlagRequired("credentials-file")

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

	// Validate JMX flags
	if jmxEnabled {
		if sourceType != "osk" {
			return fmt.Errorf("--jmx is only supported for OSK sources (--source-type osk)")
		}
		if jmxScanDuration == "" {
			return fmt.Errorf("--jmx-scan-duration is required when --jmx is set")
		}
		if _, err := time.ParseDuration(jmxScanDuration); err != nil {
			return fmt.Errorf("invalid --jmx-scan-duration '%s': %w", jmxScanDuration, err)
		}
		if _, err := time.ParseDuration(jmxPollInterval); err != nil {
			return fmt.Errorf("invalid --jmx-poll-interval '%s': %w", jmxPollInterval, err)
		}
	}
	if !jmxEnabled && cmd.Flags().Changed("jmx-scan-duration") {
		return fmt.Errorf("--jmx-scan-duration requires --jmx to be set")
	}
	if !jmxEnabled && cmd.Flags().Changed("jmx-poll-interval") {
		return fmt.Errorf("--jmx-poll-interval requires --jmx to be set")
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

	// Collect JMX metrics if enabled
	if jmxEnabled && sourceType == "osk" {
		if err := collectJMXMetrics(ctx, state, credentialsFile); err != nil {
			slog.Warn("JMX metrics collection failed", "error", err)
			fmt.Printf("\n⚠️  JMX metrics collection failed: %v\n", err)
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
		return &types.State{
			MSKSources:       &types.MSKSourcesState{Regions: []types.DiscoveredRegion{}},
			OSKSources:       &types.OSKSourcesState{Clusters: []types.OSKDiscoveredCluster{}},
			SchemaRegistries: []types.SchemaRegistryInformation{},
			KcpBuildInfo:     types.KcpBuildInfo{},
			Timestamp:        time.Now(),
		}, nil
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

	// Build map of existing clusters by ID for efficient lookup
	existingClusters := make(map[string]*types.OSKDiscoveredCluster)
	for i := range state.OSKSources.Clusters {
		cluster := &state.OSKSources.Clusters[i]
		existingClusters[cluster.ID] = cluster
	}

	// Merge new scan results
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

		if existingCluster, exists := existingClusters[newCluster.ID]; exists {
			// Merge with existing cluster (preserve discovered clients, JMX metrics, etc.)
			newCluster.DiscoveredClients = existingCluster.DiscoveredClients
			if newCluster.JMXMetrics == nil {
				newCluster.JMXMetrics = existingCluster.JMXMetrics
			}

			// Replace in-place
			*existingCluster = newCluster
		} else {
			// New cluster - append
			state.OSKSources.Clusters = append(state.OSKSources.Clusters, newCluster)
		}
	}

	slog.Info("merged OSK scan results", "clusters", len(result.Clusters))
	return nil
}

func collectJMXMetrics(ctx context.Context, state *types.State, credentialsFilePath string) error {
	creds, errs := types.NewOSKCredentialsFromFile(credentialsFilePath)
	if len(errs) > 0 {
		return fmt.Errorf("failed to reload credentials for JMX: %v", errs)
	}

	duration, _ := time.ParseDuration(jmxScanDuration)
	interval, _ := time.ParseDuration(jmxPollInterval)

	for _, clusterCreds := range creds.Clusters {
		if !clusterCreds.HasJMXConfig() {
			slog.Info("no JMX config for cluster, skipping", "cluster", clusterCreds.ID)
			continue
		}

		slog.Info("collecting JMX metrics", "cluster", clusterCreds.ID, "duration", duration, "interval", interval)
		fmt.Printf("\n📊 Collecting JMX metrics for cluster '%s' (duration: %s, interval: %s)...\n", clusterCreds.ID, duration, interval)

		var jolokiaOpts []client.JolokiaOption
		if clusterCreds.JMX.Auth != nil {
			jolokiaOpts = append(jolokiaOpts, client.WithJolokiaBasicAuth(clusterCreds.JMX.Auth.Username, clusterCreds.JMX.Auth.Password))
		}
		if clusterCreds.JMX.TLS != nil {
			jolokiaOpts = append(jolokiaOpts, client.WithJolokiaTLS(clusterCreds.JMX.TLS.CACert, clusterCreds.JMX.TLS.InsecureSkipVerify))
		}

		jmxService := jmx.NewJMXService(clusterCreds.JMX.Endpoints, jolokiaOpts...)
		metrics, err := jmxService.CollectOverDuration(ctx, duration, interval)
		if err != nil {
			slog.Warn("JMX collection failed for cluster", "cluster", clusterCreds.ID, "error", err)
			continue
		}

		oskCluster, err := state.GetOSKClusterByID(clusterCreds.ID)
		if err != nil {
			slog.Warn("cluster not found in state for JMX metrics", "cluster", clusterCreds.ID, "error", err)
			continue
		}
		oskCluster.JMXMetrics = metrics

		fmt.Printf("   ✅ Collected %d snapshots for cluster '%s'\n", len(metrics.Snapshots), clusterCreds.ID)
	}

	return nil
}
