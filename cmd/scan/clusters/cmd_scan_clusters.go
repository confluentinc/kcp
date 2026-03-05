package clusters

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/msk"
	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/confluentinc/kcp/internal/types"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile       string
	credentialsFile string
	sourceType      string
	skipTopics      bool
	skipACLs        bool
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

	scanClustersCmd.MarkFlagRequired("source-type")
	scanClustersCmd.MarkFlagRequired("credentials-file")

	return scanClustersCmd
}

func preRunScanClusters(cmd *cobra.Command, args []string) error {
	// Validate source type
	if sourceType != "msk" && sourceType != "osk" {
		return fmt.Errorf("invalid source-type '%s': must be 'msk' or 'osk'", sourceType)
	}

	// Validate credentials file naming convention
	if sourceType == "msk" && credentialsFile != "msk-credentials.yaml" {
		slog.Warn("credentials file should be named 'msk-credentials.yaml' for MSK sources", "file", credentialsFile)
	}
	if sourceType == "osk" && credentialsFile != "osk-credentials.yaml" {
		slog.Warn("credentials file should be named 'osk-credentials.yaml' for OSK sources", "file", credentialsFile)
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

// loadOrCreateState loads existing state or creates a new one
func loadOrCreateState(stateFilePath string) (*types.State, error) {
	state, err := types.NewStateFromFile(stateFilePath)
	if err != nil {
		// File doesn't exist - create new state
		slog.Info("creating new state file", "file", stateFilePath)
		return &types.State{
			MSKSources:       &types.MSKSourcesState{Regions: []types.DiscoveredRegion{}},
			OSKSources:       &types.OSKSourcesState{Clusters: []types.OSKDiscoveredCluster{}},
			SchemaRegistries: []types.SchemaRegistryInformation{},
			KcpBuildInfo:     types.KcpBuildInfo{},
			Timestamp:        time.Now(),
		}, nil
	}
	slog.Info("loaded existing state file", "file", stateFilePath)
	return state, nil
}

// mergeResultsIntoState merges scan results into the state file
func mergeResultsIntoState(state *types.State, result *sources.ScanResult) error {
	switch result.SourceType {
	case sources.SourceTypeMSK:
		return mergeMSKResults(state, result)
	case sources.SourceTypeOSK:
		return mergeOSKResults(state, result)
	default:
		return fmt.Errorf("unsupported source type: %s", result.SourceType)
	}
}

// mergeMSKResults merges MSK scan results into state
func mergeMSKResults(state *types.State, result *sources.ScanResult) error {
	if state.MSKSources == nil {
		state.MSKSources = &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{},
		}
	}

	// TODO: Implement MSK-specific merge logic
	// For now, just log
	slog.Info("merging MSK scan results", "clusters", len(result.Clusters))
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
			// Merge with existing cluster (preserve discovered clients, etc.)
			newCluster.DiscoveredClients = existingCluster.DiscoveredClients

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
