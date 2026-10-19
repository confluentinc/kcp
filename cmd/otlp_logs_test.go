package cmd

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/confluentinc/kcp/internal/logging"
	"github.com/stretchr/testify/assert"
)

// collect is a slog handler that keeps every record's message.
type collect struct {
	mu   *sync.Mutex
	msgs *[]string
}

func newCollect() collect { return collect{mu: &sync.Mutex{}, msgs: &[]string{}} }

func (c collect) Enabled(context.Context, slog.Level) bool { return true }
func (c collect) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.msgs = append(*c.msgs, r.Message)
	return nil
}
func (c collect) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c collect) WithGroup(string) slog.Handler      { return c }

// Everything that reaches kcp.log reaches the OTLP logs: the default logger's lines and the
// file-only lines the migration reporters mirror from their terminal narrative.
func TestSendLogsOverOTLPCoversEveryKcpLogLine(t *testing.T) {
	prevDefault, prevFile := slog.Default(), logging.File()
	t.Cleanup(func() {
		slog.SetDefault(prevDefault)
		logging.SetFile(prevFile)
	})
	file, console, otlp := newCollect(), newCollect(), newCollect()

	sendLogsOverOTLP(file, console, otlp)
	slog.Debug("default line")
	logging.File().Info("reporter line")

	assert.Equal(t, []string{"default line", "reporter line"}, *file.msgs, "kcp.log")
	assert.Equal(t, []string{"default line", "reporter line"}, *otlp.msgs, "OTLP logs")
	assert.Equal(t, []string{"default line"}, *console.msgs, "the console never gets the reporter's mirror")
}
