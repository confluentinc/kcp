package cli

import (
	"github.com/confluentinc/kcp/internal/cli/create_asset"
	"github.com/confluentinc/kcp/internal/cli/discover"
	i "github.com/confluentinc/kcp/internal/cli/init"
	"github.com/confluentinc/kcp/internal/cli/report"
	"github.com/confluentinc/kcp/internal/cli/scan"
	"github.com/confluentinc/kcp/internal/cli/version"
	"github.com/spf13/cobra"
)

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
		discover.NewDiscoverCmd(),
		version.NewVersionCmd(),
	)
}
