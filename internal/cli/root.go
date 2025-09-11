package cli

import (
	"io"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/cli/create_asset"
	"github.com/confluentinc/kcp/internal/cli/discover"
	i "github.com/confluentinc/kcp/internal/cli/init"
	"github.com/confluentinc/kcp/internal/cli/report"
	"github.com/confluentinc/kcp/internal/cli/scan"
	"github.com/confluentinc/kcp/internal/cli/version"
	"github.com/spf13/cobra"
	"gopkg.in/natefinch/lumberjack.v2"
)

var RootCmd = &cobra.Command{
	Use:   "kcp",
	Short: "A CLI tool for kafka cluster planning and migration",
	Long:  "A comprehensive CLI tool for planning and executing kafka cluster migrations to confluent cloud.",
}

func init() {

	lumberjackLogger := &lumberjack.Logger{
		Filename:   "kcp.log",
		MaxSize:    25, 
		Compress:   true,
	}

	logger := slog.New(slog.NewTextHandler(io.MultiWriter(lumberjackLogger, os.Stdout), nil))
	slog.SetDefault(logger)

	slog.Info("Initializing KCP", "version", build_info.Version, "commit", build_info.Commit, "date", build_info.Date)

	RootCmd.AddCommand(
		i.NewInitCmd(),
		create_asset.NewCreateAssetCmd(),
		scan.NewScanCmd(),
		report.NewReportCmd(),
		discover.NewDiscoverCmd(),
		version.NewVersionCmd(),
	)
}
