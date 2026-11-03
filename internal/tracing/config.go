// Package tracing sets up OpenTelemetry tracing for kcp: where spans go, the
// root span of each command, and the helpers instrumented code uses to open,
// close and annotate spans. Tracing is off unless KCP_TRACING_ENABLED is true.
package tracing

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
)

const (
	// EnvEnabled is the master switch: tracing is on only when it parses true.
	EnvEnabled = "KCP_TRACING_ENABLED"
	// EnvTraceFile names the OTLP/JSON file spans are appended to.
	EnvTraceFile = "KCP_TRACE_FILE"
	// DefaultTraceFile is where spans go when tracing is on and no other
	// destination is usable, beside kcp.log in the current directory.
	DefaultTraceFile = "kcp-traces.jsonl"

	envOTLPTracesEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envOTLPEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPTracesProtocol = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	envOTLPProtocol       = "OTEL_EXPORTER_OTLP_PROTOCOL"
)

// config is where spans go, resolved from the environment once at startup.
type config struct {
	enabled  bool
	file     *os.File // nil when there is no file destination
	filePath string
	otlp     bool
	warnings []string // each becomes a slog.Warn
}

type openFunc func(path string) (*os.File, error)

// resolveConfig applies the destination rules: the file and OTLP are
// independent; a trace file that can't be opened falls back to
// DefaultTraceFile; with no usable destination at all, DefaultTraceFile is
// used; and if even that can't be opened and there is no OTLP, tracing is off.
// Nothing here ever fails the command — problems become warnings.
func resolveConfig(getenv func(string) string, open openFunc) config {
	var c config
	raw := getenv(EnvEnabled)
	if raw == "" {
		return c
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		c.warnf("%s=%q is not a boolean; tracing is off", EnvEnabled, raw)
		return c
	}
	if !enabled {
		return c
	}
	c.enabled = true

	triedDefault := false
	if path := getenv(EnvTraceFile); path != "" {
		if f, err := open(path); err == nil {
			c.file, c.filePath = f, path
		} else {
			c.warnf("cannot open %s=%q (%v); writing traces to %s instead", EnvTraceFile, path, err, DefaultTraceFile)
			c.openDefault(open)
			triedDefault = true
		}
	}

	if endpoint, key := otlpEndpoint(getenv); endpoint != "" {
		if problem := otlpProblem(getenv, endpoint); problem != "" {
			c.warnf("%s=%q %s; not exporting traces over OTLP", key, endpoint, problem)
		} else {
			c.otlp = true
		}
	}

	if c.file == nil && !c.otlp && !triedDefault {
		c.openDefault(open)
	}
	if c.file == nil && !c.otlp {
		c.warnf("no usable trace destination; tracing is off")
		c.enabled = false
	}
	return c
}

func (c *config) openDefault(open openFunc) {
	f, err := open(DefaultTraceFile)
	if err != nil {
		c.warnf("cannot open %s (%v); no trace file will be written", DefaultTraceFile, err)
		return
	}
	c.file, c.filePath = f, DefaultTraceFile
}

func (c *config) warnf(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

// otlpEndpoint returns the traces-specific endpoint when set, else the base one.
func otlpEndpoint(getenv func(string) string) (value, key string) {
	if v := getenv(envOTLPTracesEndpoint); v != "" {
		return v, envOTLPTracesEndpoint
	}
	return getenv(envOTLPEndpoint), envOTLPEndpoint
}

// otlpProblem explains why endpoint can't be used, or returns "".
func otlpProblem(getenv func(string) string, endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "is not an absolute http(s) URL"
	}
	proto := getenv(envOTLPTracesProtocol)
	if proto == "" {
		proto = getenv(envOTLPProtocol)
	}
	if proto != "" && proto != "http/protobuf" {
		return fmt.Sprintf("uses protocol %q, and only http/protobuf is supported", proto)
	}
	return ""
}
