package client

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaramaSlogAdapter(t *testing.T) {
	t.Run("Printf folds the format and emits one Debug record with the sarama marker", func(t *testing.T) {
		h := testsupport.WithRecordingSlog(t)

		saramaSlogAdapter{}.Printf("dialing %s", "broker:9092")

		recs := h.Records()
		require.Len(t, recs, 1)
		assert.Equal(t, slog.LevelDebug, recs[0].Level)
		assert.Contains(t, recs[0].Message, "dialing broker:9092")
		assert.Contains(t, recs[0].Message, saramaLogPrefix)
	})

	t.Run("Print folds args with fmt.Sprint spacing into one Debug record", func(t *testing.T) {
		h := testsupport.WithRecordingSlog(t)

		saramaSlogAdapter{}.Print("x", "y")

		recs := h.Records()
		require.Len(t, recs, 1)
		// fmt.Sprint adds no space between two strings.
		assert.Equal(t, saramaLogPrefix+"xy", recs[0].Message)
	})

	t.Run("Println emits space-separated args with no trailing newline", func(t *testing.T) {
		h := testsupport.WithRecordingSlog(t)

		saramaSlogAdapter{}.Println("a", "b")

		recs := h.Records()
		require.Len(t, recs, 1)
		assert.Equal(t, saramaLogPrefix+"a b", recs[0].Message)
		assert.NotContains(t, recs[0].Message, "\n")
	})

	// Security invariant (i): sarama output must never reach the default Warn+
	// console. All three methods emit at exactly LevelDebug, never >= LevelInfo.
	t.Run("every method emits at LevelDebug, never at or above LevelInfo", func(t *testing.T) {
		h := testsupport.WithRecordingSlog(t)
		a := saramaSlogAdapter{}

		a.Print("p")
		a.Printf("%s", "f")
		a.Println("l")

		recs := h.Records()
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
		h := testsupport.WithRecordingSlog(t)

		saramaSlogAdapter{}.Printf("connecting %s", "password=hunter2")

		recs := h.Records()
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

// saramaRequireLine matches go.mod's "github.com/IBM/sarama vX.Y.Z" require
// line, standalone or inside a require ( ... ) block.
var saramaRequireLine = regexp.MustCompile(`(?m)^\s*github\.com/IBM/sarama\s+(v\S+)`)

// TestAuditedSaramaVersionMatchesGoMod turns a silent drift into a build
// failure: auditedSaramaVersion's whole point is "this exact sarama release's
// Logger/DebugLogger output was checked for credential leakage." A future
// sarama bump (a CVE fix, a new feature) must not compile and pass every
// other test while quietly invalidating that guarantee. Reads go.mod directly
// (rather than runtime/debug.ReadBuildInfo) since a single-package test binary
// is not guaranteed to embed its full module dependency list.
func TestAuditedSaramaVersionMatchesGoMod(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	require.NoError(t, err, "reading the repo's go.mod")

	m := saramaRequireLine.FindStringSubmatch(string(data))
	require.NotNil(t, m, "github.com/IBM/sarama require line not found in go.mod")

	assert.Equal(t, auditedSaramaVersion, m[1],
		"go.mod pins sarama at %s; re-audit Logger/DebugLogger output for credential leakage, then update auditedSaramaVersion", m[1])
}
