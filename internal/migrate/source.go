// Package migrate wires the manifest, source, target, and reconcile engine
// together for `kcp migrate apply`.
package migrate

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/confluentinc/kcp/internal/types"
)

// Source is the live read of the migration source. Phase 1 needs only the
// cluster id (desired-state read, §8.2); bootstrap servers and auth come from
// the parsed credentials, not a live read.
type Source interface {
	ClusterID(ctx context.Context) (string, error)
}

// OSKSourceReader reads an Apache Kafka source via the Kafka admin protocol.
type OSKSourceReader struct {
	cluster types.OSKClusterAuth
}

func NewOSKSourceReader(cluster types.OSKClusterAuth) *OSKSourceReader {
	return &OSKSourceReader{cluster: cluster}
}

// ClusterID opens an admin connection and returns the live cluster id.
func (r *OSKSourceReader) ClusterID(ctx context.Context) (string, error) {
	admin, err := osk.BuildKafkaAdmin(r.cluster)
	if err != nil {
		return "", fmt.Errorf("connecting to source: %w", err)
	}
	defer func() { _ = admin.Close() }()

	meta, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return "", fmt.Errorf("reading source cluster metadata: %w", err)
	}
	if meta.ClusterID == "" {
		return "", fmt.Errorf("source did not report a cluster id")
	}
	return meta.ClusterID, nil
}
