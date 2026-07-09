package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/confluentinc/kcp/cmd/create_asset"
	"github.com/confluentinc/kcp/cmd/discover"
	"github.com/confluentinc/kcp/cmd/docs"
	"github.com/confluentinc/kcp/cmd/healthcheck"
	"github.com/confluentinc/kcp/cmd/migration"
	"github.com/confluentinc/kcp/cmd/report"
	"github.com/confluentinc/kcp/cmd/scan"
	"github.com/confluentinc/kcp/cmd/state"
	"github.com/confluentinc/kcp/cmd/ui"
	"github.com/confluentinc/kcp/cmd/update"
	"github.com/confluentinc/kcp/cmd/version"
	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/logging"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"gopkg.in/natefinch/lumberjack.v2"
)

var verbose bool

var RootCmd = &cobra.Command{
	Use:           "kcp",
	Short:         "A CLI tool for kafka cluster planning and migration",
	Long:          "A comprehensive CLI tool for planning and executing kafka cluster migrations to confluent cloud. Docs: " + build_info.DocsURL(),
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// --- Logging setup (must be here so --verbose flag is parsed) ---
		lumberjackLogger := &lumberjack.Logger{
			Filename: "kcp.log",
			MaxSize:  25,
			Compress: true,
		}

		// File handler: always writes everything (Debug+)
		fileHandler := NewPrettyHandler(lumberjackLogger, PrettyHandlerOptions{
			SlogOpts: slog.HandlerOptions{
				Level: slog.LevelDebug,
			},
		})

		// Console handler: Info+ by default (user-facing narrative), Debug+ with --verbose
		consoleLevel := slog.LevelInfo
		if verbose {
			consoleLevel = slog.LevelDebug
		}
		consoleHandler := NewPrettyHandler(os.Stdout, PrettyHandlerOptions{
			SlogOpts: slog.HandlerOptions{
				Level: consoleLevel,
			},
			Console: true,
		})

		// Fan out to both handlers
		logger := slog.New(NewFanOutHandler(fileHandler, consoleHandler))
		slog.SetDefault(logger)

		// File-only mirror sink for components that own rich terminal output
		// (e.g. the migration reporter) and must still be captured in kcp.log.
		logging.SetFile(slog.New(fileHandler))

		// --- End logging setup ---

		if build_info.IsDev() {
			fmt.Printf("\n%s\n%s\n%s\n%s\n%s\n\n",
				color.RedString("┌─────────────────────────────────────────────────────────────────────────────────────────────┐"),
				color.RedString("│ ⚠️  WARNING: This is a development build — not a defined release.                            │"),
				color.RedString("│ This build and any state files it generates should NOT be used for production or live use.  │"),
				color.RedString("│ Install a released binary: https://github.com/confluentinc/kcp/releases                     │"),
				color.RedString("└─────────────────────────────────────────────────────────────────────────────────────────────┘"))
		}

		fmt.Printf("%s %s %s %s\n",
			color.CyanString("Executing kcp with build"),
			color.GreenString("version=%s", build_info.Version),
			color.YellowString("commit=%s", build_info.Commit),
			color.BlueString("date=%s", build_info.Date))

		// Detailed, structured build provenance for support diagnostics.
		// Logged at Debug so it lands in kcp.log (file handler is Debug+)
		// without doubling the coloured banner above on the console (Info+).
		slog.Debug("build provenance",
			"version", build_info.Version,
			"commit", build_info.Commit,
			"date", build_info.Date,
			"dev_build", build_info.IsDev(),
			"go", runtime.Version(),
			"os", runtime.GOOS,
			"arch", runtime.GOARCH,
		)

		if err := checkWritePermissions(); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", color.RedString("Error: %v", err))
			os.Exit(1)
		}
	},
}

func init() {
	cobra.EnableTraverseRunHooks = true

	RootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose logging to console")

	RootCmd.AddCommand(
		create_asset.NewCreateAssetCmd(),
		scan.NewScanCmd(),
		report.NewReportCmd(),
		ui.NewUICmd(),
		discover.NewDiscoverCmd(),
		healthcheck.NewHealthcheckCmd(),
		migration.NewMigrationCmd(),
		state.NewStateCmd(),
		version.NewVersionCmd(),
		update.NewUpdateCmd(),
		docs.NewDocsCmd(),
	)
}

type PrettyHandlerOptions struct {
	SlogOpts slog.HandlerOptions

	// Console renders INFO records as clean, human-facing narrative (no
	// time/level prefix) and colours the level on WARN/ERROR. The file
	// handler leaves this false so kcp.log keeps the full structured form.
	Console bool
}

type PrettyHandler struct {
	slog.Handler
	l       *log.Logger
	console bool
}

func (h *PrettyHandler) Handle(ctx context.Context, r slog.Record) error {
	values := []string{}
	r.Attrs(func(a slog.Attr) bool {
		values = append(values, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
		return true
	})

	var parts []string
	switch {
	case h.console && r.Level == slog.LevelInfo:
		// User-facing narrative: message + attrs only.
		parts = append(parts, r.Message)
	case h.console:
		// Console WARN/ERROR (and DEBUG under --verbose): coloured level +
		// message, no timestamp, so problems pop inline with the narrative.
		parts = append(parts, colorizeLevel(r.Level), r.Message)
	default:
		// File: full structured line for support.
		parts = append(parts,
			r.Time.Format("2006/01/02 15:04:05"),
			r.Level.String(),
			r.Message,
		)
	}
	parts = append(parts, values...)

	h.l.Printf("%s", strings.Join(parts, " "))

	return nil
}

func colorizeLevel(level slog.Level) string {
	s := level.String()
	switch {
	case level >= slog.LevelError:
		return color.RedString(s)
	case level >= slog.LevelWarn:
		return color.YellowString(s)
	default:
		return s
	}
}

func NewPrettyHandler(
	out io.Writer,
	opts PrettyHandlerOptions,
) *PrettyHandler {
	h := &PrettyHandler{
		Handler: slog.NewTextHandler(out, &opts.SlogOpts),
		l:       log.New(out, "", 0),
		console: opts.Console,
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
	defer func() { _ = os.Remove(testFile.Name()) }()
	defer func() { _ = testFile.Close() }()

	return nil
}
