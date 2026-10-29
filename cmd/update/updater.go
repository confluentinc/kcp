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
)

const (
	slug = "confluentinc/kcp"

	// imageRepo is the public Docker Hub image a containerised kcp is upgraded from.
	imageRepo = "confluentinc/kcp"

	// containerEnv is set to "true" by the official image (see Dockerfile). A
	// containerised kcp can't replace its own binary: it runs as a non-root user
	// in a read-only image, and is upgraded by pulling a newer tag instead.
	containerEnv = "KCP_IN_CONTAINER"
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
	if build_info.IsDev() && !u.opts.Force {
		slog.Warn("⚠️ Development version detected, skipping update check. Use `--force` to install latest version.")
		return nil
	}

	// Step 2: Check for latest version from GitHub releases
	latest, found, err := selfupdate.DetectLatest(context.Background(), selfupdate.ParseSlug(slug))
	if err != nil {
		return fmt.Errorf("error occurred while detecting version: %w", err)
	}
	if !found {
		return fmt.Errorf("latest version for %s/%s could not be found from github repository", runtime.GOOS, runtime.GOARCH)
	}

	// Step 3: Check if already up to date
	if latest.LessOrEqual(currentVersion) {
		fmt.Printf("✅ Your installed version (%s) is already the latest available\n", currentVersion)
		return nil
	}

	fmt.Printf("✅ New version available: %s\n", latest.Version())

	// Step 4: In a container the binary is never replaced in place: point to the new image.
	if inContainer() {
		msg, err := containerUpdate(latest.Version(), u.opts.CheckOnly)
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	}

	// Step 5: If --check-only flag is set, just report available update and exit
	if u.opts.CheckOnly {
		fmt.Printf("Update available from %s to %s. Run without --check-only to update.\n", currentVersion, latest.Version())
		return nil
	}

	// Step 6: Everything below installs in place, so verify the current user can
	// write to the installation directory before asking to proceed.
	exePath, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("could not locate executable path: %w", err)
	}

	if err := u.verifyWritePermissions(exePath); err != nil {
		args := os.Args[1:]
		// if linux or mac
		switch runtime.GOOS {
		case "linux", "darwin":
			commandStr := "sudo kcp " + strings.Join(args, " ")
			return fmt.Errorf("%w\nPlease try - %s", err, color.GreenString(commandStr))
		case "windows":
			// temp error message for windows - will revisit
			return fmt.Errorf("%w\nPlease run as administrator", err)
		default:
			return fmt.Errorf("%w", err)
		}
	}

	// Step 7: Ask for user confirmation unless --force flag is set
	if !u.opts.Force && !u.askForConfirmation("Do you want to update now? (y/N): ") {
		slog.Warn("⚠️ Update aborted")
		return nil
	}

	fmt.Printf("🚀 Updating from %s --> %s\n", currentVersion, latest.Version())

	// Step 8: Download and install the latest version
	if err := selfupdate.UpdateTo(context.Background(), latest.AssetURL, latest.AssetName, exePath); err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	fmt.Printf("✅ Successfully updated kcp to %s\n", latest.Version())

	return nil
}

// inContainer reports whether kcp is running from the official Docker image.
func inContainer() bool {
	return os.Getenv(containerEnv) == "true"
}

// containerUpdate tells a containerised user how to get latestVersion. With
// --check-only that is the answer; otherwise the requested update can't happen,
// so it is returned as an error. The image is published after the GitHub release,
// so the tag can lag behind it briefly.
func containerUpdate(latestVersion string, checkOnly bool) (string, error) {
	pull := fmt.Sprintf("docker pull %s:%s", imageRepo, latestVersion)
	const lag = "(the image is published shortly after the release; if the tag isn't there yet, try again later)"
	if checkOnly {
		return fmt.Sprintf("kcp is running in a container. To upgrade, pull the new image: %s %s", pull, lag), nil
	}
	return "", fmt.Errorf("kcp is running in a container and can't replace its own binary. Pull the new image instead: %s %s", pull, lag)
}

func (u *Updater) verifyWritePermissions(path string) error {
	dir := filepath.Dir(path)

	// cross-platform: can we write to the directory?
	testFile := filepath.Join(dir, ".kcp_write_test")
	f, err := os.Create(testFile)
	if err != nil {
		return fmt.Errorf("insufficient permissions: directory %s is not writable", dir)
	}
	defer func() { _ = f.Close() }()
	defer func() { _ = os.Remove(testFile) }()
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
