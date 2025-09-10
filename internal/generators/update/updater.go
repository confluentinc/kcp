package update

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blang/semver"
	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

const (
	githubRepo = "confluentinc/kcp"
)

type Updater struct {
	repo string
}

func NewUpdater() *Updater {
	return &Updater{
		repo: githubRepo,
	}
}

func (u *Updater) Run(force, checkOnly bool) error {
	fmt.Println("Checking for updates...")

	currentVersion := build_info.Version

	fmt.Println("Current version:", currentVersion)
	if currentVersion == "dev" || currentVersion == "" {
		fmt.Println("Development version detected, skipping update check")
		return nil
	}

	v, err := semver.Parse(currentVersion)
	if err != nil {
		return fmt.Errorf("failed to parse current version %s: %w", currentVersion, err)
	}

	if checkOnly {
		return u.checkForUpdates(v)
	}

	return u.performUpdate(v, force)
}

func (u *Updater) checkForUpdates(currentVersion semver.Version) error {
	latest, found, err := selfupdate.DetectLatest(u.repo)
	if err != nil {
		return fmt.Errorf("failed to detect latest version: %w", err)
	}

	if !found {
		fmt.Println("No release found")
		return nil
	}

	if latest.Version.LTE(currentVersion) {
		fmt.Printf("You are already running the latest version (%s)\n", currentVersion)
		return nil
	}

	fmt.Printf("New version available: %s (current: %s)\n", latest.Version, currentVersion)
	fmt.Printf("Release notes:\n%s\n", latest.ReleaseNotes)
	return nil
}

func (u *Updater) performUpdate(currentVersion semver.Version, force bool) error {
	// Ask user for confirmation unless force flag is set
	if !force && !u.askForConfirmation("Do you want to update now? (y/N): ") {
		fmt.Println("Update cancelled")
		return nil
	}

	// Get latest release info
	latest, found, err := selfupdate.DetectLatest(u.repo)
	if err != nil {
		return fmt.Errorf("failed to detect latest version: %w", err)
	}
	if !found {
		return fmt.Errorf("no release found")
	}

	// Download the new binary to temp location
	fmt.Printf("Downloading version %s...\n", latest.Version)
	tempBinary, err := u.downloadBinary(latest)
	if err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}
	defer os.Remove(tempBinary)

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Check if we need sudo permissions
	needsSudo := u.needsSudoPermissions(execPath)

	if needsSudo {
		fmt.Printf("\nThe binary is installed in a system directory (%s)\n", filepath.Dir(execPath))
		fmt.Printf("We will use 'sudo' to safely replace the binary with backup/rollback protection.\n")
		if !u.askForConfirmation("Continue with sudo replacement? (y/N): ") {
			fmt.Println("Update cancelled")
			return nil
		}
	}

	// Replace the binary
	if err := u.replaceBinary(tempBinary, execPath, needsSudo); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	fmt.Printf("Successfully updated to version %s\n", latest.Version)
	if latest.ReleaseNotes != "" {
		fmt.Printf("\nRelease notes:\n%s\n", latest.ReleaseNotes)
	}

	return nil
}

func (u *Updater) downloadBinary(release *selfupdate.Release) (string, error) {
	// Create temporary file
	tempFile, err := os.CreateTemp("", "kcp-update-*")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	// Use the selfupdate library to download the appropriate binary
	if err := selfupdate.UpdateTo(release.AssetURL, tempFile.Name()); err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	// Make it executable
	if err := os.Chmod(tempFile.Name(), 0755); err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	return tempFile.Name(), nil
}

func (u *Updater) needsSudoPermissions(execPath string) bool {
	// Test if we can write to the directory
	dir := filepath.Dir(execPath)
	testFile, err := os.CreateTemp(dir, ".kcp-permission-test-*")
	if err != nil {
		return true // Assume we need sudo if we can't test
	}
	testFile.Close()
	os.Remove(testFile.Name())
	return false
}

func (u *Updater) replaceBinary(newBinary, currentBinary string, useSudo bool) error {
	backupPath := currentBinary + ".backup"

	if useSudo {
		return u.replaceBinaryWithSudo(newBinary, currentBinary, backupPath)
	}

	return u.replaceBinaryDirect(newBinary, currentBinary, backupPath)
}

func (u *Updater) replaceBinaryWithSudo(newBinary, currentBinary, backupPath string) error {
	fmt.Println("Creating backup and replacing binary (you may be prompted for your password)...")

	// Create backup with sudo
	cmd := exec.Command("sudo", "cp", currentBinary, backupPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// Replace binary with sudo
	cmd = exec.Command("sudo", "cp", newBinary, currentBinary)
	if err := cmd.Run(); err != nil {
		// Try to restore backup
		restoreCmd := exec.Command("sudo", "mv", backupPath, currentBinary)
		restoreCmd.Run()
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	// Remove backup on success
	cmd = exec.Command("sudo", "rm", backupPath)
	cmd.Run()

	return nil
}

func (u *Updater) replaceBinaryDirect(newBinary, currentBinary, backupPath string) error {
	// Create backup
	if err := u.copyFile(currentBinary, backupPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// Replace binary
	if err := u.copyFile(newBinary, currentBinary); err != nil {
		// Restore backup on failure
		u.copyFile(backupPath, currentBinary)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	// Remove backup on success
	os.Remove(backupPath)

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
