package utils

import (
	"fmt"
	"log/slog"
	"os"
)

// ValidateOutputDir checks if the output directory already exists.
// If it exists and force is false, it returns an error suggesting --force.
// If it exists and force is true, it logs a warning and returns nil.
func ValidateOutputDir(outputDir string, force bool) error {
	_, err := os.Stat(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to check output directory %q: %w", outputDir, err)
	}

	if force {
		slog.Warn("output directory already exists, overwriting due to --force", "directory", outputDir)
		return nil
	}

	return fmt.Errorf("output directory %q already exists; use --force to overwrite", outputDir)
}
