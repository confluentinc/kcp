package ui

import (
	"fmt"

	"github.com/confluentinc/kcp/cmd/ui/api"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	port                    string
	clusterLinkRestEndpoint string
	clusterLinkClusterID    string
	clusterLinkName         string
	clusterLinkAPIKey       string
	clusterLinkAPISecret    string
)

func NewUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ui",
		Short:         "Start the UI",
		Long:          `Starts the kcp UI.`,
		Example:       `kcp ui --port 8080`,
		SilenceErrors: true,
		PreRunE:       preRunUI,
		RunE:          runStartUI,
	}

	cmd.Flags().StringVarP(&port, "port", "p", "5556", "Port to run the UI server on")

	// Optional cluster link flags for lag monitoring
	cmd.Flags().StringVar(&clusterLinkRestEndpoint, "rest-endpoint", "", "Cluster link REST endpoint (optional, for lag monitoring)")
	cmd.Flags().StringVar(&clusterLinkClusterID, "cluster-id", "", "Cluster link cluster ID (optional, for lag monitoring)")
	cmd.Flags().StringVar(&clusterLinkName, "cluster-link-name", "", "Cluster link name (optional, for lag monitoring)")
	cmd.Flags().StringVar(&clusterLinkAPIKey, "cluster-api-key", "", "Cluster link API key (optional, for lag monitoring)")
	cmd.Flags().StringVar(&clusterLinkAPISecret, "cluster-api-secret", "", "Cluster link API secret (optional, for lag monitoring)")

	return cmd
}

func preRunUI(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runStartUI(cmd *cobra.Command, args []string) error {
	opts, err := parseUICmdOpts()
	if err != nil {
		return fmt.Errorf("failed to parse UI cmd opts: %v", err)
	}

	reportService := report.NewReportService()
	targetInfraHCLService := hcl.NewTargetInfraHCLService()
	migrationInfraHCLService := hcl.NewMigrationInfraHCLService()
	migrationScriptsHCLService := hcl.NewMigrationScriptsHCLService()
	clusterLinkService := clusterlink.NewConfluentCloudService(nil)

	ui := api.NewUI(reportService, *targetInfraHCLService, *migrationInfraHCLService, *migrationScriptsHCLService, clusterLinkService, *opts)
	if err := ui.Run(); err != nil {
		return fmt.Errorf("failed to start the UI: %v", err)
	}

	return nil
}

func parseUICmdOpts() (*api.UICmdOpts, error) {
	opts := api.UICmdOpts{
		Port:                    port,
		ClusterLinkRestEndpoint: clusterLinkRestEndpoint,
		ClusterLinkClusterID:    clusterLinkClusterID,
		ClusterLinkName:         clusterLinkName,
		ClusterLinkAPIKey:       clusterLinkAPIKey,
		ClusterLinkAPISecret:    clusterLinkAPISecret,
	}

	return &opts, nil
}
