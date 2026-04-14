package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOutputDir(t *testing.T) {
	t.Run("dir does not exist returns nil", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nonexistent")
		err := ValidateOutputDir(dir)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	})

	t.Run("dir exists returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := ValidateOutputDir(dir)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected 'already exists' in error, got: %v", err)
		}
	})

	t.Run("file path that exists returns error", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "somefile")
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
		err := ValidateOutputDir(filePath)
		if err == nil {
			t.Fatal("expected error for existing path, got nil")
		}
	})
}
