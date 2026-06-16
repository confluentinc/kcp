package apachekafka

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// TestLoadCredentials_DoesNotLogSecrets locks the security property that loading
// Apache Kafka credentials never emits the SASL password to logs — only a count.
// The loader logs by construction without secret values; this is a regression
// guard against a future change that adds credential values to a log line.
func TestLoadCredentials_DoesNotLogSecrets(t *testing.T) {
	const secret = "sup3r-s3cret-passw0rd"

	creds := &types.ApacheKafkaCredentials{
		Clusters: []types.ApacheKafkaClusterAuth{{
			ID:               "prod-kafka-01",
			BootstrapServers: []string{"broker1:9092"},
			AuthMethod: types.AuthMethodConfig{
				SASLScram: &types.SASLScramConfig{Use: true, Username: "admin", Password: secret},
			},
		}},
	}
	path := filepath.Join(t.TempDir(), "apache-kafka-credentials.yaml")
	require.NoError(t, creds.WriteToFile(path))

	// Capture all slog output produced during the load.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	src := NewApacheKafkaSource()
	require.NoError(t, src.LoadCredentials(path))

	require.NotContains(t, buf.String(), secret, "credential password must never be logged")
}
