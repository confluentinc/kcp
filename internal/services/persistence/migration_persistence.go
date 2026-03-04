package persistence

import (
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/types"
)

// MigrationService defines persistence operations for migration state
type MigrationService interface {
	LoadMigrationState(filePath string) (*types.MigrationState, error)
	SaveMigrationState(filePath string, state *types.MigrationState) error
}

// FileSystemService implements persistence using the filesystem
type FileSystemService struct{}

// NewFileSystemService creates a new filesystem-based persistence service
func NewFileSystemService() *FileSystemService {
	return &FileSystemService{}
}

// LoadMigrationState loads migration state from a JSON file
func (s *FileSystemService) LoadMigrationState(filePath string) (*types.MigrationState, error) {
	// Check if file exists
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("migration state file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to check migration state file: %w", err)
	}

	state, err := types.NewMigrationStateFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load migration state: %w", err)
	}

	return state, nil
}

// SaveMigrationState saves migration state to a JSON file
func (s *FileSystemService) SaveMigrationState(filePath string, state *types.MigrationState) error {
	if err := state.WriteToFile(filePath); err != nil {
		return fmt.Errorf("failed to save migration state: %w", err)
	}
	return nil
}
