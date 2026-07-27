package txndiscovery

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLog redirects the default slog logger — the one that feeds kcp.log's
// unconditionally Debug+ file leg — into a buffer for the duration of a test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// Distinctive fixture names, chosen so a leak cannot hide behind a common word.
const (
	leakTopicProduced = "zqx-acme-payments-settlements-out"
	leakTopicConsumed = "zqx-acme-payments-settlements-in"
	leakTxnID         = "zqx-acme-eos-writer-txn"
	leakGroupID       = "zqx-acme-settlements-consumer"
	leakCredential    = "zqx-not-a-real-credential-value"
)

// leakHarness scripts a run over the distinctive fixtures above.
func leakHarness(t *testing.T, dir string) (*harness, Opts) {
	t.Helper()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey(leakTxnID)},
		[][]byte{txnValue(9001, leakTopicProduced, "__consumer_offsets")},
	))
	h.tailClient.script(discovery.DefaultConsumerOffsetsTopic, commitRecordBatch(0, 9001,
		commitKey(leakGroupID, leakTopicConsumed, 0),
	))
	h.admin.groups = map[string]string{leakGroupID: "consumer"}
	h.admin.offsets = map[string][]string{leakGroupID: {leakTopicConsumed}}

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")
	opts.Auth = authResolution{
		AuthType: types.AuthTypeSASLSCRAM,
		Method: types.AuthMethodConfig{
			SASLScram: &types.SASLScramConfig{
				Use:       true,
				Username:  "zqx-reader",
				Password:  leakCredential,
				Mechanism: "SHA512",
			},
		},
	}
	return h, opts
}

// Security: kcp.log's file leg is unconditionally Debug+ and is the file
// operators attach to support tickets. Topic names and transactional ids
// identify customer business structure, so the counts-only terminal buys
// nothing if a Debug line carries them — and under --verbose those same lines
// reach the console too.
func TestRun_KcpLogCarriesNoTopicNameOrTransactionalID(t *testing.T) {
	dir := t.TempDir()
	logs := captureLog(t)
	h, opts := leakHarness(t, dir)
	opts.Verbose = true

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	// The run really did observe the fixtures — otherwise this asserts nothing.
	require.Contains(t, readFile(t, opts.OutPath), leakTopicProduced)
	require.Contains(t, readFile(t, opts.OutPath), leakTopicConsumed)

	for _, name := range []string{leakTopicProduced, leakTopicConsumed, leakTxnID, leakGroupID} {
		assert.NotContains(t, logs.String(), name, "kcp.log must carry no customer identifier")
	}
}

// Security: the same must hold on the failure paths, which is where a
// well-meaning diagnostic is most tempting.
func TestRun_KcpLogStaysCleanWhenTheEnrichmentPathsFail(t *testing.T) {
	dir := t.TempDir()
	logs := captureLog(t)
	h, opts := leakHarness(t, dir)
	opts.Verbose = true
	h.probeErr = sarama.ErrTopicAuthorizationFailed
	h.admin.err = errors.New("broker rejected the group listing")

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	require.Contains(t, logs.String(), "consumer-offsets", "the unreadable offsets log must actually have warned")
	for _, name := range []string{leakTopicProduced, leakTopicConsumed, leakTxnID, leakGroupID} {
		assert.NotContains(t, logs.String(), name)
	}
}

// The log narrative: a run measured in hours must leave a record in kcp.log of
// having started and finished, and what it found — as counts, never names.
// Without it a support ticket's kcp.log is silent about the command entirely.
func TestRun_KcpLogRecordsTheRunAsCounts(t *testing.T) {
	dir := t.TempDir()
	logs := captureLog(t)
	h, opts := leakHarness(t, dir)

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	assert.Contains(t, logs.String(), "🚀", "the command's start is a top-level entry point")
	assert.Contains(t, logs.String(), "✅", "and its completion")
	assert.Contains(t, logs.String(), "transactions=1", "the counts are what the log carries")
}

// Security: the SASL credential reaches the command by flag or environment and
// must not be persisted anywhere — not in kcp.log, not on the terminal, and not
// in any of the three artifacts.
func TestRun_TheCredentialReachesNoLogLineAndNoArtifact(t *testing.T) {
	dir := t.TempDir()
	logs := captureLog(t)
	h, opts := leakHarness(t, dir)
	terminal := &bytes.Buffer{}
	opts.Stdout = terminal
	opts.Verbose = true

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	assert.NotContains(t, logs.String(), leakCredential, "kcp.log")
	assert.NotContains(t, terminal.String(), leakCredential, "the terminal")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the run must have written artifacts for this to assert anything")
	for _, e := range entries {
		assert.NotContains(t, readFile(t, filepath.Join(dir, e.Name())), leakCredential, e.Name())
	}
}

// Security: the same holds when the run fails at preflight, where the error
// text is assembled from the connection parameters.
func TestRun_TheCredentialReachesNoErrorMessage(t *testing.T) {
	dir := t.TempDir()
	logs := captureLog(t)
	h, opts := leakHarness(t, dir)
	h.describer = &fakeDescriber{err: errors.New("SASL authentication failed for user zqx-reader")}

	err := h.runner(t, opts).Run(context.Background())

	require.Error(t, err)
	assert.NotContains(t, err.Error(), leakCredential)
	assert.NotContains(t, logs.String(), leakCredential)
}

// R16: the terminal summary reports counts, never names. The artifacts carry
// the names; a real run produces hundreds of topics and printing them buries
// the numbers that matter.
func TestRun_TheTerminalSummaryCarriesNoNames(t *testing.T) {
	dir := t.TempDir()
	h, opts := leakHarness(t, dir)
	terminal := &bytes.Buffer{}
	opts.Stdout = terminal

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	require.Contains(t, readFile(t, opts.OutPath), leakTopicProduced, "the run observed the fixture")
	for _, name := range []string{leakTopicProduced, leakTopicConsumed, leakTxnID, leakGroupID} {
		assert.NotContains(t, terminal.String(), name)
	}
	assert.NotEmpty(t, strings.TrimSpace(terminal.String()), "the summary is not simply empty")
}
