package report

import (
	"log/slog"

	"github.com/spf13/cobra"
)

func NewReportCmd() *cobra.Command {
	reportCmd := &cobra.Command{
		Use:   "report",
		Short: "Generate reports on migration planning",
		Long:  "Generate reports on migration planning",

		SilenceErrors: true,
		PreRunE:       preRunReport,
		RunE:          runReport,
	}

	return reportCmd
}

func preRunReport(cmd *cobra.Command, args []string) error {
	slog.Info("🔍 pre-running report")
	return nil
}

func runReport(cmd *cobra.Command, args []string) error {
	slog.Info("🔍 running report")
	return nil
}
