package utils

import (
	"fmt"
	"os"
)

// ValidateOutputDir checks if the output directory already exists.
// If it exists, it returns an error.
func ValidateOutputDir(outputDir string) error {
	_, err := os.Stat(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to check output directory %q: %w", outputDir, err)
	}

	return fmt.Errorf("output directory %q already exists; remove or rename it before running again", outputDir)
}
