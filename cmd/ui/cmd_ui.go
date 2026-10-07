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
	host      string
	port      string
	stateFile string
)

func NewUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Start the UI",
		Long: `Starts the kcp UI — a local web app for visualising and analysing kcp-state.json (clusters, costs, metrics, TCO) and for generating migration assets via a guided wizard.

Runs entirely locally on ` + "`http://localhost:<port>`" + ` (default ` + "`5556`" + `); no data leaves your machine.

By default the server binds to localhost and is reachable only from this machine. To reach it from another host or from inside a container, bind a wider interface with ` + "`--host 0.0.0.0`" + `. The UI has no authentication, so only do this intentionally and restrict exposure — for example publish the container port on loopback only: ` + "`docker run -p 127.0.0.1:5556:5556 ... kcp ui --host 0.0.0.0`" + `.`,
		Example: `  # Default port (5556), localhost only
  kcp ui

  # Custom port
  kcp ui --port 8080

  # Bind all interfaces so the UI is reachable from outside (e.g. a container).
  # No auth — restrict exposure (e.g. Docker: -p 127.0.0.1:5556:5556).
  kcp ui --host 0.0.0.0

  # Pre-load a state file on launch
  kcp ui --state-file kcp-state.json`,
		SilenceErrors: true,
		PreRunE:       preRunUI,
		RunE:          runStartUI,
	}

	cmd.Flags().StringVar(&host, "host", "localhost", "Host/interface to bind the UI server to. Use 0.0.0.0 to allow access from other machines or from inside a container; the UI has no authentication, so restrict exposure (e.g. Docker: -p 127.0.0.1:<port>:<port>)")
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
		Host:      host,
		Port:      port,
		StateFile: stateFile,
	}

	return &opts, nil
}
