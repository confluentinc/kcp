package cli

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/cli/create_asset"
	i "github.com/confluentinc/kcp/internal/cli/init"
	"github.com/confluentinc/kcp/internal/cli/report"
	"github.com/confluentinc/kcp/internal/cli/scan"
	"github.com/confluentinc/kcp/internal/cli/ui"
	"github.com/spf13/cobra"
)

// Default values for build information during local development. Real values injected at release time by GoReleaser.
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

func SetBuildInfo(version, commit, date string) {
	buildVersion = version
	buildCommit = commit
	buildDate = date
}

func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Long:  "Display version, commit, and build date information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version: %s\n", buildVersion)
			fmt.Printf("Commit:  %s\n", buildCommit)
			fmt.Printf("Date:    %s\n", buildDate)
		},
	}
}

var RootCmd = &cobra.Command{
	Use:   "kcp",
	Short: "A CLI tool for kafka cluster planning and migration",
	Long:  "A comprehensive CLI tool for planning and executing kafka cluster migrations to confluent cloud.",
}

func init() {
	RootCmd.AddCommand(
		i.NewInitCmd(),
		create_asset.NewCreateAssetCmd(),
		scan.NewScanCmd(),
		report.NewReportCmd(),
		ui.NewUICmd(),
		NewVersionCmd(),
	)
}
