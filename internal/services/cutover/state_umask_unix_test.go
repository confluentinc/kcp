//go:build unix

package cutover

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCutoverState_WriteToFile_UmaskCannotLoosen verifies the file is written
// exactly 0600 even under a permissive umask. It lives in a unix-only file
// because syscall.Umask is not defined on Windows; on Windows the umask guard is
// moot (POSIX permission bits are not enforced) and the rest of the permission
// tests skip via skipIfWindows. umask is process-global, so this test must not
// run in parallel. (umask regression guard)
func TestCutoverState_WriteToFile_UmaskCannotLoosen(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	path := filepath.Join(t.TempDir(), ".kcp-cutover-state.json")
	require.NoError(t, NewCutoverState().WriteToFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "perms under umask 0 should be exactly 0600")
}
