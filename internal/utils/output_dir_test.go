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
		err := ValidateOutputDir(dir, false)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	})

	t.Run("dir exists and force is false returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := ValidateOutputDir(dir, false)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected 'already exists' in error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "--force") {
			t.Fatalf("expected '--force' hint in error, got: %v", err)
		}
	})

	t.Run("dir exists and force is true returns nil", func(t *testing.T) {
		dir := t.TempDir()
		err := ValidateOutputDir(dir, true)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	})

	t.Run("dir is a file returns nil for non-existent", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "somefile")
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
		// A file path that exists should also trigger the check
		err := ValidateOutputDir(filePath, false)
		if err == nil {
			t.Fatal("expected error for existing path, got nil")
		}
	})
}
