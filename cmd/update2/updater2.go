package update2

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/creativeprojects/go-selfupdate"
)

const (
	slug = "confluentinc/kcp"
)

type Updater2 struct {
	opts Updater2Opts
}

type Updater2Opts struct {
	Force     bool
	CheckOnly bool
}

func NewUpdater2(opts Updater2Opts) *Updater2 {
	return &Updater2{
		opts: opts,
	}
}

func (u *Updater2) Run() error {
	// Get current version
	currentVersion := build_info.Version

	// Skip update check for dev versions. If `--force` is set, push install of latest version.
	if (currentVersion == build_info.DefaultDevVersion || currentVersion == "") && !u.opts.Force {
		slog.Info("🤖 Development version detected, skipping update check. Use `--force` to install latest version.")
		return nil
	}

	latest, found, err := selfupdate.DetectLatest(context.Background(), selfupdate.ParseSlug(slug))
	if err != nil {
		return fmt.Errorf("error occurred while detecting version: %w", err)
	}
	if !found {
		return fmt.Errorf("latest version for %s/%s could not be found from github repository", runtime.GOOS, runtime.GOARCH)
	}

	if latest.LessOrEqual(currentVersion) {
		slog.Info(fmt.Sprintf("✅ Your installed version (%s) is already the latest available", currentVersion))
		return nil
	}

	slog.Info(fmt.Sprintf("🎉 New version available: %s", latest.Version()))

	// If checkOnly is set, just inform about the available update and return
	if u.opts.CheckOnly {
		slog.Info(fmt.Sprintf("💡 Update available from %s to %s. Run without --check-only to update.", currentVersion, latest.Version()))
		return nil
	}

	if !u.opts.Force && !u.askForConfirmation("🤔 Do you want to update now? (y/N): ") {
		slog.Warn("🚫 Update aborted")
		return nil
	}

	slog.Info(fmt.Sprintf("🚀 Updating from %s --> %s", currentVersion, latest.Version()))

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("could not locate executable path: %w", err)
	}

	if err := selfupdate.UpdateTo(context.Background(), latest.AssetURL, latest.AssetName, exe); err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	return nil
}

func (u *Updater2) askForConfirmation(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))

	return response == "y" || response == "yes"
}
