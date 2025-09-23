package ui

import (
	"fmt"

	"github.com/confluentinc/kcp/cmd/ui/api"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/spf13/cobra"
)

var (
	port string
)

func NewUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ui",
		Short:         "Start the UI",
		Long:          `Starts the kcp UI.`,
		Example:       `kcp ui --port 8080`,
		SilenceErrors: true,
		RunE:          runStartUI,
	}

	cmd.Flags().StringVarP(&port, "port", "p", "5556", "Port to run the UI server on")

	return cmd
}

func runStartUI(cmd *cobra.Command, args []string) error {
	opts, err := parseUICmdOpts()
	if err != nil {
		return fmt.Errorf("failed to parse UI cmd opts: %v", err)
	}

	ui := api.NewUI(report.NewReportService(), *opts)

	if err := ui.Run(); err != nil {
		return fmt.Errorf("failed to start the UI: %v", err)
	}

	return nil
}

func parseUICmdOpts() (*api.UICmdOpts, error) {
	opts := api.UICmdOpts{
		Port: port,
	}

	return &opts, nil
}
