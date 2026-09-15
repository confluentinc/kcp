package client

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureHandler records every slog.Record it is handed, at any level, so tests
// can assert on the message, level and count of what the adapter emitted.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) captured() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records
}

// withCapturedSlog installs a capturing handler as slog.Default() and restores
// the previous default when the test finishes.
func withCapturedSlog(t *testing.T) *captureHandler {
	t.Helper()
	prev := slog.Default()
	h := &captureHandler{}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

func TestSaramaSlogAdapter(t *testing.T) {
	t.Run("Printf folds the format and emits one Debug record with the sarama marker", func(t *testing.T) {
		h := withCapturedSlog(t)

		saramaSlogAdapter{}.Printf("dialing %s", "broker:9092")

		recs := h.captured()
		require.Len(t, recs, 1)
		assert.Equal(t, slog.LevelDebug, recs[0].Level)
		assert.Contains(t, recs[0].Message, "dialing broker:9092")
		assert.Contains(t, recs[0].Message, saramaLogPrefix)
	})

	t.Run("Print folds args with fmt.Sprint spacing into one Debug record", func(t *testing.T) {
		h := withCapturedSlog(t)

		saramaSlogAdapter{}.Print("x", "y")

		recs := h.captured()
		require.Len(t, recs, 1)
		// fmt.Sprint adds no space between two strings.
		assert.Equal(t, saramaLogPrefix+"xy", recs[0].Message)
	})

	t.Run("Println emits space-separated args with no trailing newline", func(t *testing.T) {
		h := withCapturedSlog(t)

		saramaSlogAdapter{}.Println("a", "b")

		recs := h.captured()
		require.Len(t, recs, 1)
		assert.Equal(t, saramaLogPrefix+"a b", recs[0].Message)
		assert.NotContains(t, recs[0].Message, "\n")
	})

	// Security invariant (i): sarama output must never reach the default Warn+
	// console. All three methods emit at exactly LevelDebug, never >= LevelInfo.
	t.Run("every method emits at LevelDebug, never at or above LevelInfo", func(t *testing.T) {
		h := withCapturedSlog(t)
		a := saramaSlogAdapter{}

		a.Print("p")
		a.Printf("%s", "f")
		a.Println("l")

		recs := h.captured()
		require.Len(t, recs, 3)
		for _, r := range recs {
			assert.Equal(t, slog.LevelDebug, r.Level)
			assert.Less(t, int(r.Level), int(slog.LevelInfo), "sarama output must stay below Info so it never surfaces on the default console")
		}
	})

	// Security invariant (ii): the adapter forwards verbatim and introduces no
	// new exposure path — a credential-shaped substring is emitted unchanged in
	// exactly one record and duplicated to no second sink.
	t.Run("forwards a credential-shaped substring unchanged, once, at Debug", func(t *testing.T) {
		h := withCapturedSlog(t)

		saramaSlogAdapter{}.Printf("connecting %s", "password=hunter2")

		recs := h.captured()
		require.Len(t, recs, 1, "the adapter must not duplicate the line to a second sink")
		assert.Equal(t, slog.LevelDebug, recs[0].Level)
		assert.Contains(t, recs[0].Message, "password=hunter2")
	})
}

func TestInstallSaramaLogging(t *testing.T) {
	prevLogger, prevDebug := sarama.Logger, sarama.DebugLogger
	t.Cleanup(func() {
		sarama.Logger = prevLogger
		sarama.DebugLogger = prevDebug
	})

	InstallSaramaLogging()

	_, okLogger := sarama.Logger.(saramaSlogAdapter)
	assert.True(t, okLogger, "sarama.Logger must be set to the slog adapter")
	_, okDebug := sarama.DebugLogger.(saramaSlogAdapter)
	assert.True(t, okDebug, "sarama.DebugLogger must be set to the slog adapter")
	assert.Equal(t, sarama.Logger, sarama.DebugLogger, "both loggers must be the same adapter")
}
