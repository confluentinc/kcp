package ui

import (
	"fmt"

	"github.com/confluentinc/kcp/cmd/ui/api"
	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	port      string
	stateFile string
)

func NewUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Start the UI",
		Long: `Starts the kcp UI — a local web app for visualising and analysing kcp-state.json (clusters, costs, metrics, TCO) and for generating migration assets via a guided wizard.

Runs entirely locally on ` + "`http://localhost:<port>`" + ` (default ` + "`5556`" + `); no data leaves your machine.`,
		Example: `  # Default port (5556)
  kcp ui

  # Custom port
  kcp ui --port 8080

  # Pre-load a state file on launch
  kcp ui --state-file kcp-state.json`,
		SilenceErrors: true,
		PreRunE:       preRunUI,
		RunE:          runStartUI,
	}

	cmd.Flags().StringVarP(&port, "port", "p", "5556", "Port to run the UI server on")
	cmd.Flags().StringVar(&stateFile, "state-file", "", "Path to a KCP state file to pre-load")

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

	ui, err := api.NewUI(reportService, targetInfraHCLService, migrationInfraHCLService, migrationScriptsHCLService, *opts)
	if err != nil {
		return err
	}
	if err := ui.Run(); err != nil {
		return fmt.Errorf("failed to start the UI: %v", err)
	}

	return nil
}

func parseUICmdOpts() (*api.UICmdOpts, error) {
	opts := api.UICmdOpts{
		Port:      port,
		StateFile: stateFile,
	}

	return &opts, nil
}
