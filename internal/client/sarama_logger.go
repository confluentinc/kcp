package client

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/IBM/sarama"
)

// auditedSaramaVersion pins the github.com/IBM/sarama release whose Logger and
// DebugLogger output was audited to contain no SASL/SCRAM usernames, passwords,
// tokens or auth-message bytes. If you bump sarama in go.mod, re-check its log
// strings for credential leakage before moving this constant — the bridge below
// forwards sarama's lines verbatim and relies on that guarantee.
const auditedSaramaVersion = "v1.46.3"

// saramaLogPrefix marks every line the bridge forwards, mirroring sarama's own
// dropped "[Sarama] " prefix so the lines stay greppable in kcp.log regardless
// of which slog handler is active.
const saramaLogPrefix = "sarama: "

// saramaSlogAdapter implements sarama.StdLogger, forwarding every sarama log
// call to slog.Debug. sarama's logging defaults to io.Discard, so without this
// bridge broker dials, metadata fetches, retries and coordinator lookups are
// silent in both the console and kcp.log.
//
// Debug (not Info) is deliberate: it keeps broker-address detail off the
// default Warn+ console — surfacing only under --verbose — and matches kcp's
// "diagnostic detail" log routing.
type saramaSlogAdapter struct{}

func (saramaSlogAdapter) Print(v ...any) {
	slog.Debug(saramaLogPrefix + fmt.Sprint(v...))
}

func (saramaSlogAdapter) Printf(format string, v ...any) {
	slog.Debug(saramaLogPrefix + fmt.Sprintf(format, v...))
}

func (saramaSlogAdapter) Println(v ...any) {
	slog.Debug(saramaLogPrefix + strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
}

// InstallSaramaLogging routes sarama's client logging into kcp's slog at Debug.
//
// It sets both sarama.Logger and sarama.DebugLogger explicitly: sarama's
// default DebugLogger merely forwards to Logger, so setting only Logger would
// capture the per-dial "fetching metadata ..." debug lines by that implicit
// forwarding alone — fragile to sarama internals — and those lines are exactly
// the signal this bridge exists to surface. Both point at the same adapter.
//
// The install is process-global by sarama's design, so it captures dials
// originating in every package that uses sarama. Safe to call once per
// invocation (from cmd_root PersistentPreRun, after slog.Default is set).
func InstallSaramaLogging() {
	adapter := saramaSlogAdapter{}
	sarama.Logger = adapter
	sarama.DebugLogger = adapter
	slog.Debug("sarama logging bridged into slog", "auditedSaramaVersion", auditedSaramaVersion)
}
