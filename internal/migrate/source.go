// Package migrate wires the manifest, source, target, and reconcile engine
// together for `kcp migrate apply`.
package migrate

import (
	"context"
	"fmt"
	"sort"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// buildSourceAdmin opens a Kafka admin connection to the source cluster. It is a
// package-level var so tests can substitute a mock admin.
var buildSourceAdmin = buildKafkaSourceAdmin

// buildKafkaSourceAdmin builds a Kafka admin for a migrate source connection,
// dispatching auth through the shared client.AdminOptionForAuth mapper. For IAM
// the region comes from AuthMethod.IAM.Region (SigV4). InsecureSkipTLSVerify is
// applied last so it overrides the per-auth default (needed for test envs with
// self-signed certs). The encryption-in-transit arg is inert in NewKafkaAdmin
// (the auth option determines TLS); ClientBrokerTls is passed for parity.
func buildKafkaSourceAdmin(conn types.KafkaSourceConn) (client.KafkaAdmin, error) {
	authType, err := conn.GetSelectedAuthType()
	if err != nil {
		return nil, fmt.Errorf("determining source auth type: %w", err)
	}
	region := ""
	if authType == types.AuthTypeIAM && conn.AuthMethod.IAM != nil {
		region = conn.AuthMethod.IAM.Region
	}
	// The mapper threads conn.InsecureSkipTLSVerify into every TLS path, so no
	// separate WithInsecureSkipVerify() override is needed.
	authOpt, err := client.AdminOptionForAuthMethod(authType, conn.AuthMethod, conn.InsecureSkipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("resolving source auth option: %w", err)
	}
	opts := []client.AdminOption{authOpt}
	return client.NewKafkaAdmin(conn.BootstrapServers, kafkatypes.ClientBrokerTls, region, "3.6.0", opts...)
}

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

// KafkaSourceReader reads a migration source via the Kafka admin protocol. The
// source may be Apache Kafka, MSK, or Confluent — auth is in the connection.
type KafkaSourceReader struct {
	conn types.KafkaSourceConn

	// cachedID memoizes the source cluster id for the lifetime of the reader
	// (one apply). The id is immutable per cluster, and ClusterID is read more
	// than once per apply (the clusterLink reconciler probes it in
	// CheckPreconditions, then reads it again in Plan); without this each read
	// would open a fresh admin connection — a full TLS+SASL handshake.
	cachedID string
	idCached bool
}

func NewKafkaSourceReader(conn types.KafkaSourceConn) *KafkaSourceReader {
	return &KafkaSourceReader{conn: conn}
}

// ClusterID returns the live source cluster id, opening an admin connection on
// the first call and memoizing the result for subsequent calls. Only a
// successful read is cached, so a failed probe still retries on the next call.
func (r *KafkaSourceReader) ClusterID(ctx context.Context) (string, error) {
	if r.idCached {
		return r.cachedID, nil
	}
	admin, err := buildSourceAdmin(r.conn)
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
	r.cachedID = meta.ClusterID
	r.idCached = true
	return r.cachedID, nil
}

// ListTopics opens an admin connection and returns the source topic names, sorted.
func (r *KafkaSourceReader) ListTopics(ctx context.Context) ([]string, error) {
	admin, err := buildSourceAdmin(r.conn)
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
func (r *KafkaSourceReader) DescribeTopics(ctx context.Context, names []string) ([]TopicSpec, error) {
	admin, err := buildSourceAdmin(r.conn)
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
