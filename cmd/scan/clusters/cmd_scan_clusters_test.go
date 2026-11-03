package clusters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowPrometheusMatrix is a minimal, valid query_range matrix payload. The
// handler sleeps before writing it so a short client timeout aborts the request
// before the body arrives.
const slowPrometheusMatrix = `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"test_metric"},"values":[[1710000000,"1234.5"]]}]}}`

// newSlowPrometheusServer returns an httptest server that waits delay before
// answering every query_range request with a valid matrix.
func newSlowPrometheusServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(slowPrometheusMatrix))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestCollectPrometheusMetrics_TimeoutIsApplied proves the configured
// prometheus.timeout is plumbed from the credentials through
// collectPrometheusMetrics into the HTTP client and actually enforced: against
// the same slow server, a sub-response-time timeout collects no data (queries
// abort and are skipped), while a generous timeout collects data. Without the
// wiring, the short-timeout case would fall back to the 30s client default and
// succeed against this 200ms server, so this test fails if the field is dropped.
func TestCollectPrometheusMetrics_TimeoutIsApplied(t *testing.T) {
	const serverDelay = 200 * time.Millisecond
	server := newSlowPrometheusServer(t, serverDelay)

	// collectPrometheusMetrics reads the package-level --metrics-range flag var.
	prevRange := metricsRange
	metricsRange = "1d"
	t.Cleanup(func() { metricsRange = prevRange })

	newCreds := func(timeout time.Duration) types.OSKClusterAuth {
		return types.OSKClusterAuth{
			ID:               "prod-kafka-01",
			BootstrapServers: []string{"broker1:9092"},
			Prometheus: &types.PrometheusConfig{
				URL:     server.URL,
				Timeout: timeout,
			},
		}
	}

	t.Run("short timeout aborts collection", func(t *testing.T) {
		// 50ms << 200ms server delay, so every query aborts before responding.
		result, err := collectPrometheusMetrics(context.Background(), newCreds(50*time.Millisecond))
		require.NoError(t, err) // per-query timeouts are logged and skipped, not returned
		require.NotNil(t, result)
		assert.Empty(t, result.Metrics, "a timeout shorter than the server response must yield no collected metrics")
	})

	t.Run("generous timeout completes collection", func(t *testing.T) {
		// 5s >> 200ms server delay, so queries complete and data is collected.
		result, err := collectPrometheusMetrics(context.Background(), newCreds(5*time.Second))
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.NotEmpty(t, result.Metrics, "a timeout longer than the server response must collect metrics")
	})
}
