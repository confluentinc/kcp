// Package atomicwrite provides a single atomic file write, shared by every
// state/report file kcp rewrites repeatedly as a workflow progresses.
package atomicwrite

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes data to filePath via a uniquely-named temp file in the
// same directory, then renames it onto the target.
//
// The mode is pinned explicitly rather than left to os.CreateTemp's 0600, so
// callers that need a different mode get it deterministically and callers that
// want 0600 are not relying on an implementation detail. The real file is only
// ever replaced by the rename and is never truncated or deleted directly, so a
// crash before the rename leaves the previous contents intact — which is what
// makes it safe for a file that gets rewritten repeatedly as a workflow
// progresses.
func WriteFile(filePath string, data []byte, perm os.FileMode) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(filePath), "."+filepath.Base(filePath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, filePath); err != nil {
		_ = os.Remove(tmpName) // best-effort cleanup of temp file on error
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
