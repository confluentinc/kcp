package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/cli/create_asset"
	"github.com/confluentinc/kcp/internal/cli/discover_v2"
	i "github.com/confluentinc/kcp/internal/cli/init"
	"github.com/confluentinc/kcp/internal/cli/report"
	"github.com/confluentinc/kcp/internal/cli/scan"
	"github.com/confluentinc/kcp/internal/cli/update"
	"github.com/confluentinc/kcp/internal/cli/version"
	"github.com/spf13/cobra"
	"gopkg.in/natefinch/lumberjack.v2"
)

var RootCmd = &cobra.Command{
	Use:   "kcp",
	Short: "A CLI tool for kafka cluster planning and migration",
	Long:  "A comprehensive CLI tool for planning and executing kafka cluster migrations to confluent cloud.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		slog.Info("Executing kcp with build", "version", build_info.Version, "commit", build_info.Commit, "date", build_info.Date)
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
		i.NewInitCmd(),
		create_asset.NewCreateAssetCmd(),
		scan.NewScanCmd(),
		report.NewReportCmd(),
		// discover.NewDiscoverCmd(),
		discover_v2.NewDiscoverV2Cmd(),
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
