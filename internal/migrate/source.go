// Package migrate wires the manifest, source, target, and reconcile engine
// together for `kcp migrate apply`.
package migrate

import (
	"context"
	"fmt"
	"sort"

	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/confluentinc/kcp/internal/types"
)

// buildSourceAdmin opens a Kafka admin connection to the source cluster. It is a
// package-level var so tests can substitute a mock admin.
var buildSourceAdmin = osk.BuildKafkaAdmin

// Source is the live read of the migration source. The cluster id supports the
// desired-state read (§8.2); ListTopics/DescribeTopics support topic mirroring
// and recreation. Bootstrap servers and auth come from the parsed credentials,
// not a live read.
type Source interface {
	ClusterID(ctx context.Context) (string, error)
	ListTopics(ctx context.Context) ([]string, error)
	DescribeTopics(ctx context.Context, names []string) ([]TopicSpec, error)
}

// TopicSpec is a source topic's recreate-relevant shape.
type TopicSpec struct {
	Name              string
	Partitions        int
	ReplicationFactor int
	Configs           map[string]string // explicitly-set (non-default) source topic configs
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
	admin, err := buildSourceAdmin(r.cluster)
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

// ListTopics opens an admin connection and returns the source topic names, sorted.
func (r *OSKSourceReader) ListTopics(ctx context.Context) ([]string, error) {
	admin, err := buildSourceAdmin(r.cluster)
	if err != nil {
		return nil, fmt.Errorf("connecting to source: %w", err)
	}
	defer func() { _ = admin.Close() }()

	topics, err := admin.ListTopicsWithConfigs()
	if err != nil {
		return nil, fmt.Errorf("listing source topics: %w", err)
	}

	names := make([]string, 0, len(topics))
	for name := range topics {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// DescribeTopics opens an admin connection and returns the recreate-relevant
// shape of the requested topics, with only explicitly-set (non-default) configs.
// Topics in names that are absent from the source are silently skipped. Results
// are sorted by name.
func (r *OSKSourceReader) DescribeTopics(ctx context.Context, names []string) ([]TopicSpec, error) {
	admin, err := buildSourceAdmin(r.cluster)
	if err != nil {
		return nil, fmt.Errorf("connecting to source: %w", err)
	}
	defer func() { _ = admin.Close() }()

	topics, err := admin.ListTopicsWithNonDefaultConfigs()
	if err != nil {
		return nil, fmt.Errorf("describing source topics: %w", err)
	}

	wanted := make(map[string]struct{}, len(names))
	for _, n := range names {
		wanted[n] = struct{}{}
	}

	specs := make([]TopicSpec, 0, len(names))
	for name, detail := range topics {
		if _, ok := wanted[name]; !ok {
			continue
		}
		configs := make(map[string]string, len(detail.ConfigEntries))
		for k, v := range detail.ConfigEntries {
			if v == nil {
				continue
			}
			configs[k] = *v
		}
		specs = append(specs, TopicSpec{
			Name:              name,
			Partitions:        int(detail.NumPartitions),
			ReplicationFactor: int(detail.ReplicationFactor),
			Configs:           configs,
		})
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs, nil
}
