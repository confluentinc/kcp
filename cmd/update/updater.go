package update

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/creativeprojects/go-selfupdate"
	"github.com/fatih/color"
	"golang.org/x/sys/unix"
)

const (
	slug = "confluentinc/kcp"
)

type Updater struct {
	opts UpdaterOpts
}

type UpdaterOpts struct {
	Force     bool
	CheckOnly bool
}

func NewUpdater(opts UpdaterOpts) *Updater {
	return &Updater{
		opts: opts,
	}
}

func (u *Updater) Run() error {
	currentVersion := build_info.Version

	// Step 1: Skip update check for dev versions unless --force is set
	if (currentVersion == "" || currentVersion == build_info.DefaultDevVersion) && !u.opts.Force {
		slog.Info("🤖 Development version detected, skipping update check. Use `--force` to install latest version.")
		return nil
	}

	// Step 2: Verify current user has write permissions to the installation directory
	exePath, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("could not locate executable path: %w", err)
	}

	if err := u.verifyWritePermissions(exePath); err != nil {
		args := os.Args[1:]
		commandStr := "sudo kcp " + strings.Join(args, " ")
		return fmt.Errorf("kcp is installed at a location that requires sudo privileges\nPlease try - %s", color.GreenString(commandStr))
	}

	// Step 3: Check for latest version from GitHub releases
	latest, found, err := selfupdate.DetectLatest(context.Background(), selfupdate.ParseSlug(slug))
	if err != nil {
		return fmt.Errorf("error occurred while detecting version: %w", err)
	}
	if !found {
		return fmt.Errorf("latest version for %s/%s could not be found from github repository", runtime.GOOS, runtime.GOARCH)
	}

	// Step 4: Check if already up to date
	if latest.LessOrEqual(currentVersion) {
		slog.Info(fmt.Sprintf("✅ Your installed version (%s) is already the latest available", currentVersion))
		return nil
	}

	slog.Info(fmt.Sprintf("🎉 New version available: %s", latest.Version()))

	// Step 5: If --check-only flag is set, just report available update and exit
	if u.opts.CheckOnly {
		slog.Info(fmt.Sprintf("💡 Update available from %s to %s. Run without --check-only to update.", currentVersion, latest.Version()))
		return nil
	}

	// Step 6: Ask for user confirmation unless --force flag is set
	if !u.opts.Force && !u.askForConfirmation("🤔 Do you want to update now? (y/N): ") {
		slog.Warn("🚫 Update aborted")
		return nil
	}

	slog.Info(fmt.Sprintf("🚀 Updating from %s --> %s", currentVersion, latest.Version()))

	// Step 7: Download and install the latest version
	if err := selfupdate.UpdateTo(context.Background(), latest.AssetURL, latest.AssetName, exePath); err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	slog.Info(fmt.Sprintf("✅ Successfully updated kcp to %s", latest.Version()))

	return nil
}

func (u *Updater) verifyWritePermissions(path string) error {
	// linux/macOS only at the moment - will need to add Windows support later
	dir := filepath.Dir(path)
	if err := unix.Access(dir, unix.W_OK); err != nil {
		return fmt.Errorf("insufficient permissions: directory %s is not writable", dir)
	}
	return nil
}

func (u *Updater) askForConfirmation(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))

	return response == "y" || response == "yes"
}
