// Package report renders the artifacts a transaction-discovery run produces.
//
// # Encoding of hostile topic names
//
// Topic names reach the audit log as attacker-influenceable data: whoever can create a
// topic on the observed cluster chooses the bytes, and the names additionally arrive via
// decoders for broker-internal binary formats that are not part of the stable client
// protocol. Records are therefore always produced with encoding/json, never with string
// formatting, so quotes, backslashes, control characters and — critically — newlines are
// escaped. An unescaped newline would terminate the record early and let the remainder of
// a name be read as a second, forged audit record.
//
// One case is deliberately lossy. encoding/json substitutes U+FFFD for invalid UTF-8
// rather than returning an error, so a topic name containing invalid UTF-8 is recorded
// with those bytes replaced. That is accepted here:
//
//   - The alternative is dropping the line, and a missing line reads downstream as "no
//     transaction coupled these topics" — indistinguishable from a clean run, and the
//     single worst failure mode for this artifact.
//   - The coupling itself, which is what the file exists to record, survives intact; only
//     the rendering of the name is lossy.
//   - Kafka constrains topic names to [a-zA-Z0-9._-], so invalid UTF-8 cannot come from a
//     legitimate topic. Its presence means a decode went wrong upstream, which the decode
//     error counters report separately.
//
// The consequence to be aware of: grepping the audit log for the original raw bytes will
// not match such a name. Grep for the U+FFFD-substituted form, or for the transactional
// id instead.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
)

// auditFileMode is the mode the audit log is created with. The file carries topic
// names and transactional ids — customer business structure — so it matches the 0600
// used for kcp-state.json rather than the 0644 used for reports.
const auditFileMode os.FileMode = 0600

// AuditLine is the on-disk shape of one audit record: a single JSON object on a
// single line.
type AuditLine struct {
	Timestamp time.Time `json:"timestamp"`
	TxnID     string    `json:"transactional_id"`
	Source    string    `json:"source"`
	Added     []string  `json:"added"`
	Topics    []string  `json:"topics"`
}

// AuditWriter appends a JSONL line to the audit log each time a transaction's topic
// set grows.
type AuditWriter struct {
	path string
	f    *os.File

	// mu serialises the write and the error counter. Without it a concurrent source
	// silently loses increments, so the summary under-reports how much of the trace is
	// missing — the one number that distinguishes a truncated run from a clean one.
	mu sync.Mutex

	// sink is where lines are written. It is always f in production; the indirection
	// exists so tests can drive the failure paths a full disk would produce.
	sink io.Writer

	// errs counts lines that were meant to reach disk and did not, plus a failed
	// close. A truncated audit log reads downstream as "no transaction coupled these
	// topics" — indistinguishable from a clean run — so the count is the only thing
	// that lets the summary and the exit code tell the two apart.
	errs int

	// warned records that the first failure has already been logged, so a full disk
	// produces one warning rather than one per dropped line.
	warned bool
}

// NewAuditWriter opens the audit log at path, rotating any file already there.
func NewAuditWriter(path string) (*AuditWriter, error) {
	if err := rotateExisting(path); err != nil {
		return nil, err
	}
	f, err := openAuditFile(path)
	if err != nil {
		return nil, err
	}
	// sink is the *os.File itself, deliberately unbuffered: the audit log is a live
	// trace an operator tails during a run measured in hours, and a run killed by
	// SIGKILL must still leave everything observed up to that point on disk.
	return &AuditWriter{path: path, f: f, sink: f}, nil
}

// openAuditFile opens the audit log for appending.
//
// rotateExisting has already established that nothing unsuitable sits at path, but it
// checked the path and this opens the path, so the two are separated by a window an
// attacker with write access to the directory can use. Everything here re-establishes
// the invariant against the opened handle rather than trusting that pre-check.
func openAuditFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|openNoFollow, auditFileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log %s: %w", path, err)
	}
	// Stat the handle, not the path: statting the path would re-open the same TOCTOU
	// window this check exists to close. os.File.Stat is fstat(2) on the descriptor
	// already opened, so what it reports is what will actually be written to.
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to stat opened audit log %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("refusing to write the audit log: %s is %s, not a regular file; remove it or choose another path with --audit-log-out", path, describeFileType(fi.Mode()))
	}
	// auditFileMode is only applied when the open creates the file. An existing file
	// keeps whatever mode it already had, so the mode has to be verified rather than
	// assumed — the audit log carries topic names and transactional ids.
	if perm := fi.Mode().Perm(); perm&^auditFileMode != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("refusing to write the audit log: %s has mode %#o, wider than %#o, and the audit log carries topic names and transactional ids; remove it or choose another path with --audit-log-out", path, perm, auditFileMode)
	}
	return f, nil
}

// rotateExisting moves any file already at path aside to <path>.<timestamp>.
//
// Appending instead would be wrong twice over. Functionally, the file exists so an
// operator can trace how one union was built; concatenating two runs' traces makes
// that read ambiguous. Security-wise, O_APPEND|O_CREATE applies its mode only on
// creation, so appending to a file left at 0644 silently keeps it world-readable.
func rotateExisting(path string) error {
	// Lstat, not Stat: a symlink at the audit path must be seen as a symlink rather
	// than resolved to whatever it targets.
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // nothing to rotate
		}
		return fmt.Errorf("failed to inspect existing audit log %s: %w", path, err)
	}
	// Only a regular file is a plausible previous run's audit log, and only a regular
	// file is safe to rotate. Anything else is rejected and left in place rather than
	// renamed aside: a symlink or fifo at the audit path is a signal that something is
	// wrong, and quietly rotating it away hides the evidence from the operator.
	if !fi.Mode().IsRegular() {
		return errNotRegularFile(path, fi.Mode())
	}
	dst, err := freeRotationName(path)
	if err != nil {
		return err
	}
	if err := os.Rename(path, dst); err != nil {
		return fmt.Errorf("failed to rotate existing audit log %s to %s: %w", path, dst, err)
	}
	return nil
}

// errNotRegularFile is the refusal both the pre-check and the post-open check return, so
// the operator sees the same wording whichever of the two caught it.
func errNotRegularFile(path string, m fs.FileMode) error {
	return fmt.Errorf("refusing to write the audit log: %s is %s, not a regular file; remove it or choose another path with --audit-log-out", path, describeFileType(m))
}

// describeFileType renders a file mode's type in words. fs.FileMode.String() encodes
// the type as a single letter, which is not something to put in front of an operator.
func describeFileType(m fs.FileMode) string {
	switch {
	case m&fs.ModeSymlink != 0:
		return "a symbolic link"
	case m.IsDir():
		return "a directory"
	case m&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case m&fs.ModeSocket != 0:
		return "a socket"
	case m&fs.ModeDevice != 0:
		return "a device"
	default:
		return "an irregular file"
	}
}

// maxRotationAttempts bounds the same-second collision search so a directory that
// somehow already holds every candidate name fails loudly instead of spinning.
const maxRotationAttempts = 100

// freeRotationName returns an unused <path>.<timestamp> name, adding a counter suffix
// when two runs rotate within the same second. Without the counter the second rotation
// renames over the first, destroying exactly the trace rotation exists to preserve.
func freeRotationName(path string) (string, error) {
	ts := time.Now().UTC().Format("20060102T150405Z")
	for i := 0; i < maxRotationAttempts; i++ {
		candidate := path + "." + ts
		if i > 0 {
			candidate = fmt.Sprintf("%s.%s-%d", path, ts, i)
		}
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("failed to find a free rotation name for audit log %s after %d attempts", path, maxRotationAttempts)
}

// Record appends one audit line describing what obs did to its transaction's topic set.
func (w *AuditWriter) Record(obs discovery.Observation, ch discovery.Change) {
	// A nil writer is --no-audit-log (R18). Tolerating it here means the caller wires
	// one branch instead of a nil check at every call site, where a forgotten one would
	// panic partway through a run measured in hours.
	if w == nil {
		return
	}
	// R19: the audit trail records couplings, so an observation that re-reports a
	// footprint already seen is not an event. Logging it would bury the growth events
	// the file exists to make findable.
	if !ch.Grew() {
		return
	}
	line := AuditLine{
		Timestamp: obs.ObservedAt,
		TxnID:     obs.TxnID,
		Source:    obs.Source,
		Added:     ch.Added,
		Topics:    ch.Topics,
	}
	// encoding/json, never hand-rolled formatting. A topic name is attacker-influenced:
	// whoever can create a topic chooses the bytes that land here, and an unescaped
	// newline would end the record early and let the rest of the name be read as a
	// second, forged audit record.
	data, err := json.Marshal(line)

	// Marshalling is done above, outside the lock; only the write and the counter need
	// serialising. KTD7 puts this write under the accumulator's decision, but
	// Accumulator.Add releases its own lock before returning the Change, so by the time
	// Record is called the three sources are once again running concurrently.
	w.mu.Lock()
	defer w.mu.Unlock()

	if err != nil {
		w.errs++
		w.warnOnce(err)
		return
	}
	// One Write for the record and its newline together. Splitting them would let a
	// crash land between the two and leave a line with no terminator, which merges two
	// records into one unparseable line instead of losing only the trailing one.
	if _, err := w.sink.Write(append(data, '\n')); err != nil {
		w.errs++
		w.warnOnce(err)
	}
}

// warnOnce logs the first failure and stays quiet thereafter. Callers hold w.mu.
//
// Once, because the realistic cause is a full disk or an unmounted volume, which fails
// every subsequent write too — thousands of identical lines would bury the rest of the
// run. The running total is Errors(), which the summary reports at the end.
//
// The line carries the path and the cause and nothing else. kcp.log's file leg is
// unconditionally Debug+ and is what operators attach to support tickets, so no topic
// name or transactional id may appear in it at any level — the audit file itself is the
// artifact that carries those.
func (w *AuditWriter) warnOnce(cause error) {
	if w.warned {
		return
	}
	w.warned = true
	slog.Warn("❌ failed to write to the transaction discovery audit log, the trace will be incomplete", "path", w.path, "error", cause)
}

// Errors returns the number of audit lines that failed to reach disk, plus a failed
// close. A non-zero count means the trace is incomplete and must be surfaced as a
// warning and a non-zero exit code rather than passing as a clean run.
func (w *AuditWriter) Errors() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.errs
}

// Path returns the path the audit log is being written to, or "" when the audit log is
// disabled.
func (w *AuditWriter) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// Close closes the audit log, counting a failure the same way a failed write is
// counted: on buffered and network filesystems the deferred write error surfaces here
// rather than at the write, so a failed close also means lines are missing.
func (w *AuditWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Close(); err != nil {
		w.errs++
		return fmt.Errorf("failed to close audit log %s: %w", w.path, err)
	}
	return nil
}
