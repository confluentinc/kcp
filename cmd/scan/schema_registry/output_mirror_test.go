package schema_registry

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/output"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// mirrorRecorder captures the slog records the output mirror emits so this
// spot-check can assert the narrative also reaches kcp.log.
type mirrorRecorder struct{ records []slog.Record }

func (h *mirrorRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *mirrorRecorder) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *mirrorRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *mirrorRecorder) WithGroup(string) slog.Handler      { return h }

func captureStdoutAndMirror(t *testing.T, fn func()) (string, []slog.Record) {
	t.Helper()

	h := &mirrorRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	require.NoError(t, w.Close())
	return <-done, h.records
}

func hasMirroredLine(records []slog.Record, msg string) bool {
	for _, r := range records {
		if r.Message != msg {
			continue
		}
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == output.MirrorMarkerKey {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// TestGlueScanner_NarrativeMirrored is the scan-group spot-check: the
// representative scanner-start narrative must (a) still reach stdout
// byte-for-byte and (b) appear plain in a mirrored kcp.log record.
func TestGlueScanner_NarrativeMirrored(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(`{"msk_sources":{"regions":[]},"schema_registries":null,"kcp_build_info":{"version":"0.0.0-localdev","commit":"unknown","date":"unknown"}}`)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	service := &mockGlueService{
		getRegistryInfoFn: func(context.Context, string) (string, error) {
			return "arn:aws:glue:us-east-1:123:registry/my-registry", nil
		},
		getAllSchemasWithVersionsFn: func(context.Context, string) ([]types.GlueSchema, error) {
			return []types.GlueSchema{}, nil
		},
	}

	state, err := types.NewStateFromFile(tmpFile.Name())
	require.NoError(t, err)

	scanner := NewGlueSchemaRegistryScanner(service, GlueSchemaRegistryScannerOpts{
		StateFile:    tmpFile.Name(),
		State:        *state,
		Region:       "us-east-1",
		RegistryName: "my-registry",
	})

	stdout, records := captureStdoutAndMirror(t, func() {
		require.NoError(t, scanner.Run(context.Background()))
	})

	require.Contains(t, stdout, "🚀 Starting Glue Schema Registry scanner\n",
		"stdout narrative must be unchanged")
	require.True(t, hasMirroredLine(records, "🚀 Starting Glue Schema Registry scanner"),
		"narrative must also appear (plain) in a mirrored record")
}
