package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
)

const (
	githubAPIURL = "https://api.github.com/repos/confluentinc/kcp/releases/latest"
	timeout      = 30 * time.Second
)

type UpdaterOpts struct {
	Force        bool
	CheckOnly    bool
	GitHubAPIURL string
	Timeout      time.Duration
}

type Updater struct {
	force     bool
	checkOnly bool
}

type GitHubRelease struct {
	TagName string `json:"tag_name"`
}

func NewUpdater(opts UpdaterOpts) *Updater {
	return &Updater{
		force:     opts.Force,
		checkOnly: opts.CheckOnly,
	}
}

func (u *Updater) Run() error {
	// Get current version
	currentVersion := build_info.Version

	// Skip update check for dev versions. If `--force` is set, push install of latest version.
	if (currentVersion == "dev" || currentVersion == "") && !u.force {
		slog.Warn("Development version detected, skipping update check. Use `--force` to install latest version.")
		return nil
	}

	// Get latest version from GitHub
	latestVersion, err := u.getLatestVersion()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	// Compare versions
	if !u.isNewerVersion(latestVersion, currentVersion) {
		slog.Info(fmt.Sprintf("✅ Your installed version (%s) is already the latest available", currentVersion))
		return nil
	}

	slog.Info(fmt.Sprintf("🎉 New version available: %s", latestVersion))

	// If checkOnly is set, just inform about the available update and return
	if u.checkOnly {
		slog.Info(fmt.Sprintf("ℹ️ Update available from %s to %s. Run without --check-only to update.", currentVersion, latestVersion))
		return nil
	}

	// Ask for confirmation unless force flag is set
	if !u.force && !u.askForConfirmation("Do you want to update now? (y/N): ") {
		slog.Warn("Update cancelled")
		return nil
	}

	// Perform the update with backup/rollback
	if err := u.performUpdate(latestVersion); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	slog.Info(fmt.Sprintf("✅ Successfully updated to version %s", latestVersion))

	return nil
}

func (u *Updater) getLatestVersion() (string, error) {
	client := &http.Client{Timeout: timeout}

	resp, err := client.Get(githubAPIURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	return release.TagName, nil
}

func (u *Updater) isNewerVersion(latest, current string) bool {
	latest = strings.TrimPrefix(latest, "v")
	current = strings.TrimPrefix(current, "v")

	return latest != current
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

func (u *Updater) performUpdate(version string) error {
	// 1. Find current binary location
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current binary path: %w", err)
	}

	slog.Info(fmt.Sprintf("Current binary: %s", currentBinary))

	// 2. Create backup
	backupPath := currentBinary + ".backup"
	if err := u.createBackup(currentBinary, backupPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// 3. Download and install new version
	if err := u.downloadAndInstall(version, currentBinary); err != nil {
		// Rollback on failure
		slog.Error("Update failed, rolling back...")
		u.rollback(backupPath, currentBinary)
		return err
	}

	// 4. Clean up backup on success
	os.Remove(backupPath)

	return nil
}

func (u *Updater) createBackup(source, backup string) error {
	fmt.Println("Creating backup...")

	// Check if we need sudo
	if u.needsSudo(source) {
		return exec.Command("sudo", "cp", source, backup).Run()
	}

	return u.copyFile(source, backup)
}

func (u *Updater) downloadAndInstall(version, targetPath string) error {
	// Construct download URL for tar.gz
	platform := runtime.GOOS
	arch := runtime.GOARCH
	fileName := fmt.Sprintf("kcp_%s_%s.tar.gz", platform, arch)
	downloadURL := fmt.Sprintf("https://github.com/confluentinc/kcp/releases/download/%s/%s", version, fileName)

	slog.Info(fmt.Sprintf("Downloading %s...", downloadURL))

	// Download the tar.gz file
	resp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Extract and install
	return u.extractAndInstall(resp.Body, targetPath)
}

func (u *Updater) extractAndInstall(gzipReader io.Reader, targetPath string) error {
	slog.Info("Extracting and installing...")

	// Open gzip reader
	gzr, err := gzip.NewReader(gzipReader)
	if err != nil {
		return fmt.Errorf("failed to open gzip: %w", err)
	}
	defer gzr.Close()

	// Open tar reader
	tr := tar.NewReader(gzr)

	// Find the binary in the tar
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		// Look for the kcp binary
		if header.Name == "kcp" || strings.HasSuffix(header.Name, "/kcp") {
			// Create temp file for the new binary
			tempFile, err := os.CreateTemp("", "kcp-new-*")
			if err != nil {
				return fmt.Errorf("failed to create temp file: %w", err)
			}
			defer os.Remove(tempFile.Name())

			// Copy binary content
			if _, err := io.Copy(tempFile, tr); err != nil {
				tempFile.Close()
				return fmt.Errorf("failed to extract binary: %w", err)
			}
			tempFile.Close()

			// Make executable
			if err := os.Chmod(tempFile.Name(), 0755); err != nil {
				return fmt.Errorf("failed to make executable: %w", err)
			}

			// Install the new binary
			return u.installBinary(tempFile.Name(), targetPath)
		}
	}

	return fmt.Errorf("kcp binary not found in archive")
}

func (u *Updater) installBinary(newBinary, targetPath string) error {
	slog.Info("Installing new binary...")

	if u.needsSudo(targetPath) {
		// For sudo case, use atomic rename via a temp file in the same directory
		dir := filepath.Dir(targetPath)
		tempPath := filepath.Join(dir, filepath.Base(targetPath)+".tmp")

		if err := exec.Command("sudo", "cp", newBinary, tempPath).Run(); err != nil {
			return fmt.Errorf("failed to copy to temp file: %w", err)
		}

		if err := exec.Command("sudo", "chmod", "755", tempPath).Run(); err != nil {
			exec.Command("sudo", "rm", tempPath).Run() // Clean up on error
			return fmt.Errorf("failed to make executable: %w", err)
		}

		// Atomic rename (works even on executing files)
		if err := exec.Command("sudo", "mv", tempPath, targetPath).Run(); err != nil {
			exec.Command("sudo", "rm", tempPath).Run() // Clean up on error
			return fmt.Errorf("failed to install binary: %w", err)
		}

		return nil
	}

	// For non-sudo case, use atomic rename via a temp file
	return u.installBinaryAtomic(newBinary, targetPath)
}

func (u *Updater) rollback(backupPath, targetPath string) {
	slog.Warn("Rolling back to previous version...")

	if u.needsSudo(targetPath) {
		// mv works atomically even on executing files
		exec.Command("sudo", "mv", backupPath, targetPath).Run()
	} else {
		// Use atomic rename for rollback too (works even if target is executing)
		if err := os.Rename(backupPath, targetPath); err != nil {
			u.copyFile(backupPath, targetPath)
			os.Remove(backupPath)
		}
	}
}

func (u *Updater) needsSudo(path string) bool {
	dir := filepath.Dir(path)
	testFile, err := os.CreateTemp(dir, ".kcp-test-*")
	if err != nil {
		return true // Assume sudo needed if we can't test
	}
	testFile.Close()
	os.Remove(testFile.Name())
	return false
}

func (u *Updater) installBinaryAtomic(newBinary, targetPath string) error {
	// Create temp file in the same directory as target (required for atomic rename)
	dir := filepath.Dir(targetPath)
	tempPath := filepath.Join(dir, filepath.Base(targetPath)+".tmp")

	sourceFile, err := os.Open(newBinary)
	if err != nil {
		return fmt.Errorf("failed to open source: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	renameSucceeded := false
	defer func() {
		destFile.Close()
		if !renameSucceeded {
			os.Remove(tempPath)
		}
	}()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy to temp file: %w", err)
	}

	sourceInfo, err := os.Stat(newBinary)
	if err != nil {
		return fmt.Errorf("failed to stat source: %w", err)
	}

	if err := os.Chmod(tempPath, sourceInfo.Mode()); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := destFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename - this works even if target is currently executing
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	renameSucceeded = true
	return nil
}

func (u *Updater) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Copy permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, sourceInfo.Mode())
}
