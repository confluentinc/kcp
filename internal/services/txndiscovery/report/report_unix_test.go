//go:build unix

// Mode bits are a unix concept. Windows' os.Chmod only toggles the read-only bit, so
// these assertions are meaningless there — and a runtime GOOS skip cannot save a package
// that will not compile, hence a build tag rather than t.Skip.
package report

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// withPermissiveUmask clears the process umask for the duration of a test, so a mode
// assertion proves the writer asked for 0600 rather than proving the ambient umask
// happened to strip the wider bits. Tests using it must not run in parallel: the umask
// is process-wide.
func withPermissiveUmask(t *testing.T) {
	t.Helper()
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
}

// requireMode0600 fails unless path is on disk at exactly mode 0600.
func requireMode0600(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("%s has mode %#o, want %#o", path, got, 0600)
	}
}

func TestStatsJSONIsWritten0600(t *testing.T) {
	withPermissiveUmask(t)

	// The stats document is NOT a counts-only file: it carries every observed
	// transaction's id and topic set, so the POC's 0644 must not port across.
	path := filepath.Join(t.TempDir(), "txn-discovery-stats.json")
	if err := WriteStatsJSON(path, distinctiveRun()); err != nil {
		t.Fatalf("WriteStatsJSON: %v", err)
	}
	requireMode0600(t, path)
}

func TestYAMLIsWritten0600(t *testing.T) {
	withPermissiveUmask(t)

	// The document carries every topic name and transactional id the run observed —
	// customer business structure — so it matches kcp-state.json's 0600 rather than
	// the 0644 a report would use.
	path := filepath.Join(t.TempDir(), "txn-discovery.yaml")
	if err := WriteYAML(path, Summarize(distinctiveRun())); err != nil {
		t.Fatalf("WriteYAML: %v", err)
	}
	requireMode0600(t, path)
}
