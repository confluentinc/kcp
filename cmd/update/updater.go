package update

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/creativeprojects/go-selfupdate"
	"github.com/fatih/color"
)

const (
	slug = "confluentinc/kcp"
)

// assetFilter returns a regexp matching exactly this edition's release binary
// asset for the running platform. Both editions ship per-platform assets in the
// same release (kcp_{os}_{arch} and kcp-lite_{os}_{arch}), so without a filter
// the updater could install the wrong edition. The edition is fixed at compile
// time via build_info, so a kcp-lite binary always resolves the kcp-lite asset.
func assetFilter() string {
	return assetFilterFor(build_info.IsGov(), runtime.GOOS, runtime.GOARCH)
}

// assetFilterFor is the pure, testable core of assetFilter. It builds an
// anchored regexp matching goreleaser's `{name}_{os}_{arch}` binary asset (with
// an optional `.exe` on Windows). The anchors ensure "kcp" never matches
// "kcp-lite" (and vice-versa) and that the `.tar.gz`/`.zip` archive variants are
// excluded in favour of the raw binary.
func assetFilterFor(gov bool, goos, goarch string) string {
	name := "kcp"
	if gov {
		name = "kcp-lite"
	}
	// Windows binary assets carry a `.exe` extension; other platforms don't.
	ext := ""
	if goos == "windows" {
		ext = `(\.exe)?`
	}
	return fmt.Sprintf(`(?i)^%s_%s_%s%s$`,
		regexp.QuoteMeta(name), regexp.QuoteMeta(goos), regexp.QuoteMeta(goarch), ext)
}

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
		slog.Warn("⚠️ Development version detected, skipping update check. Use `--force` to install latest version.")
		return nil
	}

	// Step 2: Verify current user has write permissions to the installation directory
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

	// Step 3: Check for latest version from GitHub releases. Constrain asset
	// resolution to this edition's binary so a kcp-lite build never installs the
	// full kcp (and vice-versa).
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Filters: []string{assetFilter()},
	})
	if err != nil {
		return fmt.Errorf("could not configure updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.ParseSlug(slug))
	if err != nil {
		return fmt.Errorf("error occurred while detecting version: %w", err)
	}
	if !found {
		// No asset matched the edition-specific filter — refuse rather than risk
		// installing the wrong edition.
		return fmt.Errorf("no %q release asset found for %s/%s; not updating to avoid installing the wrong edition", build_info.Mode, runtime.GOOS, runtime.GOARCH)
	}

	// Step 4: Check if already up to date
	if latest.LessOrEqual(currentVersion) {
		fmt.Printf("✅ Your installed version (%s) is already the latest available\n", currentVersion)
		return nil
	}

	fmt.Printf("✅ New version available: %s\n", latest.Version())

	// Step 5: If --check-only flag is set, just report available update and exit
	if u.opts.CheckOnly {
		fmt.Printf("Update available from %s to %s. Run without --check-only to update.\n", currentVersion, latest.Version())
		return nil
	}

	// Step 6: Ask for user confirmation unless --force flag is set
	if !u.opts.Force && !u.askForConfirmation("Do you want to update now? (y/N): ") {
		slog.Warn("⚠️ Update aborted")
		return nil
	}

	fmt.Printf("🚀 Updating from %s --> %s\n", currentVersion, latest.Version())

	// Step 7: Download and install the latest version. Use the updater (with the
	// edition filter applied) so the resolved asset matches this edition.
	if err := updater.UpdateTo(context.Background(), latest, exePath); err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	fmt.Printf("✅ Successfully updated kcp to %s\n", latest.Version())

	return nil
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
