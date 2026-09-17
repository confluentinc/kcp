package testsupport

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// RecordingSlogHandler is a slog.Handler test double that records every
// slog.Record it is handed, at any level, so a test can assert on what was
// logged.
type RecordingSlogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *RecordingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *RecordingSlogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *RecordingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *RecordingSlogHandler) WithGroup(string) slog.Handler      { return h }

// Records returns every record captured so far.
func (h *RecordingSlogHandler) Records() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records
}

// Find returns the first captured record with the given message, if any.
func (h *RecordingSlogHandler) Find(msg string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

// WithRecordingSlog installs a RecordingSlogHandler as slog.Default() and
// restores the previous default when the test finishes.
func WithRecordingSlog(t *testing.T) *RecordingSlogHandler {
	t.Helper()
	prev := slog.Default()
	h := &RecordingSlogHandler{}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}
