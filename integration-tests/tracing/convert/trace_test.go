//go:build e2e

package tracing_convert_e2e

import (
	"bufio"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

// span is one decoded span from a trace file.
type span struct {
	name, traceID, spanID, parentID string
	start, end                      uint64
	failed                          bool
	attrs                           map[string]string
	resource                        map[string]string
}

// readTrace decodes every span in an OTLP/JSON trace file.
func readTrace(t *testing.T, path string) []span {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err, "kcp must have written the trace file")
	defer func() { _ = f.Close() }()
	var out []span
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		req := ptraceotlp.NewExportRequest()
		require.NoError(t, req.UnmarshalJSON(sc.Bytes()), "each line is OTLP/JSON")
		rss := req.Traces().ResourceSpans()
		for i := 0; i < rss.Len(); i++ {
			res := attrMap(rss.At(i).Resource().Attributes())
			sss := rss.At(i).ScopeSpans()
			for j := 0; j < sss.Len(); j++ {
				ss := sss.At(j).Spans()
				for k := 0; k < ss.Len(); k++ {
					s := ss.At(k)
					out = append(out, span{
						name: s.Name(), traceID: s.TraceID().String(), spanID: s.SpanID().String(),
						parentID: s.ParentSpanID().String(), start: uint64(s.StartTimestamp()), end: uint64(s.EndTimestamp()),
						failed: s.Status().Code() == ptrace.StatusCodeError,
						attrs:  attrMap(s.Attributes()), resource: res,
					})
				}
			}
		}
	}
	require.NoError(t, sc.Err())
	return out
}

func attrMap(m pcommon.Map) map[string]string {
	out := map[string]string{}
	m.Range(func(k string, v pcommon.Value) bool { out[k] = v.AsString(); return true })
	return out
}
