//go:build unix

// Mode bits, symlink semantics and directory-permission enforcement are unix concepts.
// Windows' os.Chmod only toggles the read-only bit, so these assertions are meaningless
// there — and a runtime GOOS skip cannot save a package that will not compile, hence a
// build tag rather than t.Skip.
package report

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAuditWriterCreatesTheFile0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit log: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Fatalf("audit log mode: want 0600, got %#o — the file carries topic names and transactional ids", got)
	}
}

func TestNewAuditWriterRotatesAnExistingWorldReadableAuditFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "txn-discovery-audit.jsonl")

	// A previous run (or a planted file) left a world-readable audit log behind.
	// O_APPEND|O_CREATE applies its mode only on creation, so appending here would
	// both keep 0644 and concatenate two runs' traces.
	const stale = `{"timestamp":"2020-01-01T00:00:00Z","transactional_id":"stale","source":"x","added":["old"],"topics":["old"]}` + "\n"
	if err := os.WriteFile(path, []byte(stale), 0644); err != nil {
		t.Fatalf("plant stale audit log: %v", err)
	}

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	// The live audit log is a fresh, empty, 0600 file.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if strings.Contains(string(body), "stale") {
		t.Errorf("audit log was appended to rather than rotated; it still carries the previous run's trace")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit log: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("audit log mode after rotation: want 0600, got %#o", got)
	}

	// The previous run's trace is preserved alongside, not destroyed.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var rotated string
	for _, e := range entries {
		if n := e.Name(); n != "txn-discovery-audit.jsonl" && strings.HasPrefix(n, "txn-discovery-audit.jsonl.") {
			rotated = filepath.Join(dir, n)
		}
	}
	if rotated == "" {
		t.Fatalf("no rotated file found in %v; the previous run's trace was destroyed", entries)
	}
	kept, err := os.ReadFile(rotated)
	if err != nil {
		t.Fatalf("read rotated file: %v", err)
	}
	if string(kept) != stale {
		t.Errorf("rotated file content: want %q, got %q", stale, string(kept))
	}
}

func TestRotationInTheSameSecondDoesNotDestroyTheEarlierTrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "txn-discovery-audit.jsonl")

	// Two runs started back to back land on the same second-precision timestamp. The
	// rotated name must still be unique, or the second rotation silently overwrites
	// the first run's trace.
	for _, body := range []string{"first-run\n", "second-run\n"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatalf("plant %q: %v", body, err)
		}
		w, err := NewAuditWriter(path)
		if err != nil {
			t.Fatalf("NewAuditWriter for %q: %v", body, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		if e.Name() == "txn-discovery-audit.jsonl" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		found[strings.TrimSpace(string(b))] = true
	}
	if !found["first-run"] || !found["second-run"] {
		t.Fatalf("both rotated traces should survive; found %v across %d rotated files", found, len(found))
	}
}

func TestNewAuditWriterRejectsASymlinkAtTheAuditPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "txn-discovery-audit.jsonl")
	target := filepath.Join(dir, "victim")

	if err := os.WriteFile(target, []byte("untouched\n"), 0600); err != nil {
		t.Fatalf("create victim: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}

	w, err := NewAuditWriter(path)
	if err == nil {
		_ = w.Close()
		t.Fatalf("want an error for a symlink at the audit path, got nil")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("error should name the symlink so an operator can act on it; got: %v", err)
	}

	// The symlink's target must be neither appended to nor truncated...
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if string(body) != "untouched\n" {
		t.Errorf("symlink was followed; victim content is now %q", string(body))
	}
	// ...and the planted link is left in place rather than quietly rotated away, so
	// the operator sees what the command refused to write through.
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat audit path: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the planted symlink should be left untouched for inspection, got mode %v", fi.Mode())
	}
}

// The rotate step checks the path and the open step opens the path, so an attacker with
// write access to the directory can act in between. These two exercise openAuditFile
// directly — i.e. the state as it would be after that pre-check has already passed.

func TestOpenAuditFileRejectsASymlinkPlantedAfterThePreCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "txn-discovery-audit.jsonl")
	target := filepath.Join(dir, "victim")

	if err := os.WriteFile(target, []byte("untouched\n"), 0600); err != nil {
		t.Fatalf("create victim: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}

	f, err := openAuditFile(path)
	if err == nil {
		_ = f.Close()
		t.Fatalf("want an error, got nil — the open followed a symlink planted in the race window")
	}

	body, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read victim: %v", readErr)
	}
	if string(body) != "untouched\n" {
		t.Errorf("symlink was followed; victim content is now %q", string(body))
	}
}

func TestOpenAuditFileRejectsAWiderThan0600FilePlantedAfterThePreCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "txn-discovery-audit.jsonl")

	// O_APPEND|O_CREATE applies auditFileMode only when it creates the file, so a
	// group/world-readable file planted here would be appended to at its own mode.
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("plant 0644 file: %v", err)
	}

	f, err := openAuditFile(path)
	if err == nil {
		_ = f.Close()
		t.Fatalf("want an error, got nil — opened a 0644 file that would carry topic names")
	}
	if !strings.Contains(err.Error(), "0644") {
		t.Errorf("error should name the offending mode; got: %v", err)
	}
}

func TestNewAuditWriterFailsUpFrontOnAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "txn-discovery-audit.jsonl")

	// The failure must land here, at construction, so the preflight can abort before
	// the run starts. Discovering it on the first Record means the run has already
	// spent its observation window producing a trace that was never written.
	w, err := NewAuditWriter(path)
	if err == nil {
		_ = w.Close()
		t.Fatalf("want an error for an unwritable directory, got nil")
	}
	if w != nil {
		t.Errorf("want a nil writer alongside the error, got %#v", w)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("the underlying cause must survive wrapping so callers can classify it; got: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the path the operator has to fix; got: %v", err)
	}
}
