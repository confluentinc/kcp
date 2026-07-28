package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shared loader reports the real path in its read error (previously MSK
// hardcoded "creds.yaml file" regardless of the path passed in).
func TestNewCredentialsFromFile_ReadErrorIncludesPath(t *testing.T) {
	missing := "/nonexistent/dir/my-creds.yaml"
	_, errs := NewCredentialsFromFile(missing)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), missing)
}

func TestNewOSKCredentialsFromFile_ReadErrorIncludesPath(t *testing.T) {
	missing := "/nonexistent/dir/my-apache-kafka-credentials.yaml"
	_, errs := NewOSKCredentialsFromFile(missing)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), missing)
}
