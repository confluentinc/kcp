package persistence

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

const (
	maxRetries     = 3
	initialBackoff = 1 * time.Second
)

// Service defines state persistence operations
type Service interface {
	Load(result interface{}) error
	Save(state interface{}) error
	SaveWithRetry(state interface{}) error
	GetFilePath() string
}

// FileService implements persistence to a local file
type FileService struct {
	filePath string
}

// NewFileService creates a new file-based persistence service
func NewFileService(filePath string) *FileService {
	return &FileService{
		filePath: filePath,
	}
}

// Load reads state from file
func (s *FileService) Load(result interface{}) error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to read state file: %w", err)
	}

	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return nil
}

// GetFilePath returns the file path managed by this service
func (s *FileService) GetFilePath() string {
	return s.filePath
}

// Save persists state to file atomically
func (s *FileService) Save(state interface{}) error {
	// Write to temporary file first for atomic operation
	tmpFile := s.filePath + ".tmp"

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename (on most filesystems)
	if err := os.Rename(tmpFile, s.filePath); err != nil {
		os.Remove(tmpFile) // Clean up temp file
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// SaveWithRetry persists state with exponential backoff retry
func (s *FileService) SaveWithRetry(state interface{}) error {
	var err error
	
	for i := 0; i < maxRetries; i++ {
		err = s.Save(state)
		if err == nil {
			return nil
		}

		if i < maxRetries-1 {
			backoff := initialBackoff * time.Duration(1<<uint(i))
			slog.Warn("Failed to persist state, retrying...",
				"attempt", i+1,
				"maxRetries", maxRetries,
				"backoff", backoff,
				"error", err,
			)
			time.Sleep(backoff)
		}
	}

	return fmt.Errorf("failed to persist state after %d retries: %w", maxRetries, err)
}
