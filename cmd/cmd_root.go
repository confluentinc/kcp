package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/confluentinc/kcp/cmd/create_asset"
	"github.com/confluentinc/kcp/cmd/discover"
	"github.com/confluentinc/kcp/cmd/migration"
	"github.com/confluentinc/kcp/cmd/report"
	"github.com/confluentinc/kcp/cmd/scan"
	"github.com/confluentinc/kcp/cmd/ui"
	"github.com/confluentinc/kcp/cmd/update"
	"github.com/confluentinc/kcp/cmd/version"
	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"gopkg.in/natefinch/lumberjack.v2"
)

var RootCmd = &cobra.Command{
	Use:   "kcp",
	Short: "A CLI tool for kafka cluster planning and migration",
	Long:  "A comprehensive CLI tool for planning and executing kafka cluster migrations to confluent cloud. Docs: " + getDocURL(),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if build_info.Version == "dev" {
			fmt.Printf("\n%s\n%s\n%s\n%s\n\n",
				color.RedString("┌─────────────────────────────────────────────────────────────────────────┐"),
				color.RedString("│ ⚠️  WARNING: This is a development build                                │"),
				color.RedString("│ Official releases: https://github.com/confluentinc/kcp/releases         │"),
				color.RedString("└─────────────────────────────────────────────────────────────────────────┘"))
		}

		fmt.Printf("%s %s %s %s\n",
			color.CyanString("Executing kcp with build"),
			color.GreenString("version=%s", build_info.Version),
			color.YellowString("commit=%s", build_info.Commit),
			color.BlueString("date=%s", build_info.Date))

		if err := checkWritePermissions(); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", color.RedString("Error: %v", err))
			os.Exit(1)
		}
	},
}

func init() {
	cobra.EnableTraverseRunHooks = true

	lumberjackLogger := &lumberjack.Logger{
		Filename: "kcp.log",
		MaxSize:  25,
		Compress: true,
	}
	opts := PrettyHandlerOptions{
		SlogOpts: slog.HandlerOptions{
			Level: slog.LevelDebug,
		},
	}
	handler := NewPrettyHandler(io.MultiWriter(lumberjackLogger, os.Stdout), opts)
	logger := slog.New(handler)

	slog.SetDefault(logger)

	RootCmd.AddCommand(
		create_asset.NewCreateAssetCmd(),
		scan.NewScanCmd(),
		report.NewReportCmd(),
		ui.NewUICmd(),
		discover.NewDiscoverCmd(),
		migration.NewMigrationCmd(),
		version.NewVersionCmd(),
		update.NewUpdateCmd(),
	)
}

type PrettyHandlerOptions struct {
	SlogOpts slog.HandlerOptions
}

type PrettyHandler struct {
	slog.Handler
	l *log.Logger
}

func getDocURL() string {
	if build_info.Version == "dev" {
		return "https://github.com/confluentinc/kcp/tree/latest/docs"
	}
	return "https://github.com/confluentinc/kcp/tree/v" + build_info.Version + "/docs"

}

func (h *PrettyHandler) Handle(ctx context.Context, r slog.Record) error {
	time := r.Time.Format("2006/01/02 15:04:05")
	level := r.Level.String()
	message := r.Message

	values := []string{}
	r.Attrs(func(a slog.Attr) bool {
		values = append(values, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
		return true
	})

	h.l.Printf("%s %s %s %s", time, level, message, strings.Join(values, " "))

	return nil
}

func NewPrettyHandler(
	out io.Writer,
	opts PrettyHandlerOptions,
) *PrettyHandler {
	h := &PrettyHandler{
		Handler: slog.NewTextHandler(out, &opts.SlogOpts),
		l:       log.New(out, "", 0),
	}

	return h
}

func checkWritePermissions() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}

	testFile, err := os.CreateTemp(cwd, ".kcp-write-test-*")
	if err != nil {
		return fmt.Errorf("current working directory '%s' does not have write permissions for the current user", cwd)
	}

	// Defer works on a LIFO execution order.
	defer os.Remove(testFile.Name())
	defer testFile.Close()

	return nil
}
