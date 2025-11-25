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
	slog.Info("installBinary: [1] Starting installation", "newBinary", newBinary, "targetPath", targetPath)

	needsSudo := u.needsSudo(targetPath)
	slog.Info("installBinary: [2] Checked sudo requirement", "needsSudo", needsSudo, "targetPath", targetPath)

	if needsSudo {
		slog.Info("installBinary: [3] Taking sudo path")
		// For sudo case, use atomic rename via a temp file in the same directory
		dir := filepath.Dir(targetPath)
		tempPath := filepath.Join(dir, filepath.Base(targetPath)+".tmp")
		slog.Info("installBinary: [4] Created temp path for sudo install", "tempPath", tempPath, "dir", dir)

		slog.Info("installBinary: [5] Executing sudo cp command", "source", newBinary, "dest", tempPath)
		if err := exec.Command("sudo", "cp", newBinary, tempPath).Run(); err != nil {
			slog.Error("installBinary: [5] Failed to copy to temp file", "error", err, "tempPath", tempPath)
			return fmt.Errorf("failed to copy to temp file: %w", err)
		}
		slog.Info("installBinary: [5] Successfully copied to temp file", "tempPath", tempPath)

		slog.Info("installBinary: [6] Executing sudo chmod command", "tempPath", tempPath, "mode", "755")
		if err := exec.Command("sudo", "chmod", "755", tempPath).Run(); err != nil {
			slog.Error("installBinary: [6] Failed to make executable, cleaning up", "error", err, "tempPath", tempPath)
			exec.Command("sudo", "rm", tempPath).Run() // Clean up on error
			return fmt.Errorf("failed to make executable: %w", err)
		}
		slog.Info("installBinary: [6] Successfully set permissions", "tempPath", tempPath)

		// Atomic rename (works even on executing files)
		slog.Info("installBinary: [7] Executing sudo mv for atomic rename", "tempPath", tempPath, "targetPath", targetPath)
		if err := exec.Command("sudo", "mv", tempPath, targetPath).Run(); err != nil {
			slog.Error("installBinary: [7] Failed to install binary, cleaning up", "error", err, "tempPath", tempPath)
			exec.Command("sudo", "rm", tempPath).Run() // Clean up on error
			return fmt.Errorf("failed to install binary: %w", err)
		}
		slog.Info("installBinary: [7] Successfully completed atomic rename", "targetPath", targetPath)

		slog.Info("installBinary: [8] Sudo path completed successfully")
		return nil
	}

	// For non-sudo case, use atomic rename via a temp file
	slog.Info("installBinary: [3] Taking non-sudo path, calling installBinaryAtomic", "newBinary", newBinary, "targetPath", targetPath)
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
	slog.Info("needsSudo: [1] Starting sudo check", "path", path)

	dir := filepath.Dir(path)
	slog.Info("needsSudo: [2] Extracted directory", "dir", dir, "path", path)

	slog.Info("needsSudo: [3] Attempting to create test file", "dir", dir)
	testFile, err := os.CreateTemp(dir, ".kcp-test-*")
	if err != nil {
		slog.Info("needsSudo: [3] Failed to create test file, assuming sudo needed", "error", err, "dir", dir)
		return true // Assume sudo needed if we can't test
	}
	slog.Info("needsSudo: [3] Successfully created test file", "testFile", testFile.Name())

	slog.Info("needsSudo: [4] Closing test file", "testFile", testFile.Name())
	testFile.Close()

	slog.Info("needsSudo: [5] Removing test file", "testFile", testFile.Name())
	os.Remove(testFile.Name())
	slog.Info("needsSudo: [5] Successfully removed test file")

	slog.Info("needsSudo: [6] Sudo not needed, returning false")
	return false
}

func (u *Updater) installBinaryAtomic(newBinary, targetPath string) error {
	slog.Info("installBinaryAtomic: [1] Starting atomic install", "newBinary", newBinary, "targetPath", targetPath)

	// Create temp file in the same directory as target (required for atomic rename)
	dir := filepath.Dir(targetPath)
	tempPath := filepath.Join(dir, filepath.Base(targetPath)+".tmp")
	slog.Info("installBinaryAtomic: [2] Created temp path", "tempPath", tempPath, "dir", dir)

	slog.Info("installBinaryAtomic: [3] Opening source file", "newBinary", newBinary)
	sourceFile, err := os.Open(newBinary)
	if err != nil {
		slog.Error("installBinaryAtomic: [3] Failed to open source", "error", err, "newBinary", newBinary)
		return fmt.Errorf("failed to open source: %w", err)
	}
	defer sourceFile.Close()
	slog.Info("installBinaryAtomic: [3] Successfully opened source file")

	slog.Info("installBinaryAtomic: [4] Creating temp file", "tempPath", tempPath)
	destFile, err := os.Create(tempPath)
	if err != nil {
		slog.Error("installBinaryAtomic: [4] Failed to create temp file", "error", err, "tempPath", tempPath)
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	slog.Info("installBinaryAtomic: [4] Successfully created temp file")

	renameSucceeded := false
	defer func() {
		slog.Info("installBinaryAtomic: [defer] Closing dest file and cleaning up if needed", "renameSucceeded", renameSucceeded, "tempPath", tempPath)
		destFile.Close()
		if !renameSucceeded {
			slog.Info("installBinaryAtomic: [defer] Removing temp file due to failed rename", "tempPath", tempPath)
			os.Remove(tempPath)
		}
	}()

	slog.Info("installBinaryAtomic: [5] Copying content from source to temp file")
	if _, err := io.Copy(destFile, sourceFile); err != nil {
		slog.Error("installBinaryAtomic: [5] Failed to copy to temp file", "error", err, "tempPath", tempPath)
		return fmt.Errorf("failed to copy to temp file: %w", err)
	}
	slog.Info("installBinaryAtomic: [5] Successfully copied content to temp file")

	slog.Info("installBinaryAtomic: [6] Getting source file info for permissions", "newBinary", newBinary)
	sourceInfo, err := os.Stat(newBinary)
	if err != nil {
		slog.Error("installBinaryAtomic: [6] Failed to stat source", "error", err, "newBinary", newBinary)
		return fmt.Errorf("failed to stat source: %w", err)
	}
	slog.Info("installBinaryAtomic: [6] Successfully got source file info", "mode", sourceInfo.Mode())

	slog.Info("installBinaryAtomic: [7] Setting permissions on temp file", "tempPath", tempPath, "mode", sourceInfo.Mode())
	if err := os.Chmod(tempPath, sourceInfo.Mode()); err != nil {
		slog.Error("installBinaryAtomic: [7] Failed to set permissions", "error", err, "tempPath", tempPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}
	slog.Info("installBinaryAtomic: [7] Successfully set permissions")

	slog.Info("installBinaryAtomic: [8] Closing dest file", "tempPath", tempPath)
	if err := destFile.Close(); err != nil {
		slog.Error("installBinaryAtomic: [8] Failed to close temp file", "error", err, "tempPath", tempPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	slog.Info("installBinaryAtomic: [8] Successfully closed dest file")

	// Atomic rename - this works even if target is currently executing
	slog.Info("installBinaryAtomic: [9] Performing atomic rename", "tempPath", tempPath, "targetPath", targetPath)
	if err := os.Rename(tempPath, targetPath); err != nil {
		slog.Error("installBinaryAtomic: [9] Failed to rename temp file", "error", err, "tempPath", tempPath, "targetPath", targetPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	slog.Info("installBinaryAtomic: [9] Successfully completed atomic rename", "targetPath", targetPath)

	renameSucceeded = true
	slog.Info("installBinaryAtomic: [10] Atomic install completed successfully")
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
