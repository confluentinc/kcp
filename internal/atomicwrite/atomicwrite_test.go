package atomicwrite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFile_WritesContentAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")

	require.NoError(t, WriteFile(path, []byte(`{"a":1}`), 0600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestWriteFile_NoLeftoverTempFileAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	require.NoError(t, WriteFile(path, []byte("data"), 0600))

	matches, err := filepath.Glob(filepath.Join(dir, "."+filepath.Base(path)+".tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "expected no temp file to remain after a successful write")
}

func TestWriteFile_OverwritesExistingFileAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")

	require.NoError(t, WriteFile(path, []byte("first"), 0600))
	require.NoError(t, WriteFile(path, []byte("second"), 0600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "second", string(data))
}

func TestWriteFile_FailsWhenDirectoryDoesNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "out.json")

	err := WriteFile(path, []byte("data"), 0600)

	require.Error(t, err)
}
