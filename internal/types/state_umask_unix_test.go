//go:build unix

package types

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriteToFile_UmaskCannotLoosen verifies that even under a permissive umask
// the state file is written exactly 0600 and never more permissive. It lives in
// a unix-only file because syscall.Umask is not defined on Windows; on Windows
// the umask guard is moot (POSIX permission bits are not enforced) and the rest
// of the permission tests skip via skipIfWindows. umask is process-global, so
// this test must not run in parallel. (umask regression guard)
func TestWriteToFile_UmaskCannotLoosen(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	path := filepath.Join(t.TempDir(), "kcp-state.json")
	if err := (&State{}).WriteToFile(path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state file perms under umask 0 = %#o, want exactly 0600", got)
	}
}
