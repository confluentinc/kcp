package ui

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/generators/ui/api"
	"github.com/spf13/cobra"
)

func NewUICmd() *cobra.Command {
	var port string

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Start the UI",
		Long: `Starts the kcp UI.
		`,
		Example: `
		kcp ui --port 8080
		`,
		SilenceErrors: true,
		RunE:          startUI,
	}

	cmd.Flags().StringVarP(&port, "port", "p", "5556", "Port to run the UI server on")

	return cmd
}

func startUI(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetString("port")

	ui := api.StartAPI(port)

	if err := ui.Run(); err != nil {
		return fmt.Errorf("failed to start the UI: %v", err)
	}

	return nil
}
