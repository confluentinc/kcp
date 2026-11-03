package tracing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOpen opens real temp files for every path except those in failing.
func fakeOpen(t *testing.T, failing ...string) openFunc {
	dir := t.TempDir()
	return func(path string) (*os.File, error) {
		for _, f := range failing {
			if path == f {
				return nil, errors.New("permission denied")
			}
		}
		return os.Create(filepath.Join(dir, filepath.Base(path)))
	}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveConfig(t *testing.T) {
	cases := []struct {
		name         string
		env          map[string]string
		failing      []string
		wantEnabled  bool
		wantFile     string // "" = no file destination
		wantOTLP     bool
		wantWarnings int
	}{
		{name: "unset is off", env: map[string]string{}},
		{name: "false is off", env: map[string]string{EnvEnabled: "false", EnvTraceFile: "x.jsonl"}},
		{name: "unparseable is off with a warning", env: map[string]string{EnvEnabled: "yes"}, wantWarnings: 1},
		{name: "enabled alone writes the default file", env: map[string]string{EnvEnabled: "true"}, wantEnabled: true, wantFile: DefaultTraceFile},
		{name: "valid file", env: map[string]string{EnvEnabled: "1", EnvTraceFile: "run.jsonl"}, wantEnabled: true, wantFile: "run.jsonl"},
		{name: "unopenable file falls back to the default", env: map[string]string{EnvEnabled: "true", EnvTraceFile: "/ro/run.jsonl"}, failing: []string{"/ro/run.jsonl"}, wantEnabled: true, wantFile: DefaultTraceFile, wantWarnings: 1},
		{name: "valid endpoint alone is OTLP only", env: map[string]string{EnvEnabled: "true", envOTLPEndpoint: "http://localhost:4318"}, wantEnabled: true, wantOTLP: true},
		{name: "traces endpoint wins over the base endpoint", env: map[string]string{EnvEnabled: "true", envOTLPEndpoint: "::bad", envOTLPTracesEndpoint: "https://collector:4318/v1/traces"}, wantEnabled: true, wantOTLP: true},
		{name: "file and endpoint together", env: map[string]string{EnvEnabled: "true", EnvTraceFile: "run.jsonl", envOTLPEndpoint: "http://localhost:4318"}, wantEnabled: true, wantFile: "run.jsonl", wantOTLP: true},
		{name: "malformed endpoint falls back to the default file", env: map[string]string{EnvEnabled: "true", envOTLPEndpoint: "localhost:4318"}, wantEnabled: true, wantFile: DefaultTraceFile, wantWarnings: 1},
		{name: "unsupported protocol skips OTLP", env: map[string]string{EnvEnabled: "true", envOTLPEndpoint: "http://localhost:4317", envOTLPProtocol: "grpc"}, wantEnabled: true, wantFile: DefaultTraceFile, wantWarnings: 1},
		{name: "unopenable default with no endpoint is off", env: map[string]string{EnvEnabled: "true"}, failing: []string{DefaultTraceFile}, wantWarnings: 2},
		{name: "unopenable file and default but a valid endpoint stays on", env: map[string]string{EnvEnabled: "true", EnvTraceFile: "/ro/run.jsonl", envOTLPEndpoint: "http://localhost:4318"}, failing: []string{"/ro/run.jsonl", DefaultTraceFile}, wantEnabled: true, wantOTLP: true, wantWarnings: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := resolveConfig(envOf(tc.env), fakeOpen(t, tc.failing...))
			if c.file != nil {
				t.Cleanup(func() { _ = c.file.Close() })
			}
			assert.Equal(t, tc.wantEnabled, c.enabled, "enabled")
			assert.Equal(t, tc.wantFile, c.filePath, "file")
			assert.Equal(t, tc.wantFile != "", c.file != nil, "file handle present iff a file destination")
			assert.Equal(t, tc.wantOTLP, c.otlp, "otlp")
			require.Len(t, c.warnings, tc.wantWarnings, "warnings: %v", c.warnings)
		})
	}
}

func TestResolveConfig_OffNeverOpensAFile(t *testing.T) {
	opened := false
	open := func(string) (*os.File, error) { opened = true; return nil, errors.New("unused") }
	resolveConfig(envOf(map[string]string{EnvTraceFile: "run.jsonl"}), open)
	assert.False(t, opened, "tracing off must not create or open any file")
}
