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
		Long: `Starts the kcp UI — a local web app for visualising and analysing your kcp-state.json file.

The UI provides:

- An interactive dashboard for exploring discovered clusters, topics, ACLs and connectors.
- State-file upload via the browser, or pre-loaded with ` + "`--state-file`" + `.
- Cost analysis (visual reports and per-region breakdowns).
- Metrics visualisation (interactive charts for cluster metrics).
- A TCO calculator for total cost-of-ownership analysis.
- A wizard for generating target-infra, migration-infra and migration scripts.

Once started, the UI is available at ` + "`http://localhost:<port>`" + ` (default: ` + "`http://localhost:5556`" + `). Everything runs locally — your data is never sent to an external service.`,
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
