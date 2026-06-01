package connect_topics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"

	"github.com/confluentinc/kcp/internal/services/connectdiscovery"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

type ConnectTopicsScannerOpts struct {
	Source    sources.Source
	State     *types.State
	ClusterID string
	Topics    []string
	Stdout    io.Writer
	Stderr    io.Writer
}

// ConnectTopicsScanner orchestrates the per-cluster connect-status read.
type ConnectTopicsScanner struct {
	source    sources.Source
	state     *types.State
	clusterID string
	topics    []string
	stdout    io.Writer
	stderr    io.Writer
}

func NewConnectTopicsScanner(opts ConnectTopicsScannerOpts) *ConnectTopicsScanner {
	return &ConnectTopicsScanner{
		source:    opts.Source,
		state:     opts.State,
		clusterID: opts.ClusterID,
		topics:    opts.Topics,
		stdout:    opts.Stdout,
		stderr:    opts.Stderr,
	}
}

// Run reads the configured status topics for the cluster identified by
// ClusterID and prints the unique Connect worker addresses to stdout. The
// returned error is non-nil when zero addresses were discovered (so shell
// callers can branch on the exit code) or when the underlying admin client
// could not be built.
func (s *ConnectTopicsScanner) Run(ctx context.Context) error {
	slog.Info("scanning cluster for Connect worker addresses",
		"cluster_id", s.clusterID,
		"source", s.source.Type(),
		"topics", s.topics,
	)

	admin, err := s.source.GetKafkaAdminForCluster(s.clusterID, s.state)
	if err != nil {
		return fmt.Errorf("failed to build Kafka admin for cluster %q: %w", s.clusterID, err)
	}
	defer func() { _ = admin.Close() }()

	brokers, cfg := admin.BrokerConfig()
	workers, stats, extractErr := connectdiscovery.ExtractWorkerIDs(ctx, brokers, cfg, s.topics)
	if extractErr != nil {
		return fmt.Errorf("failed to extract worker IDs for cluster %q: %w", s.clusterID, extractErr)
	}

	for topic, ts := range stats.TopicStats {
		switch {
		case ts.TopicNotFound:
			slog.Warn("topic not found on cluster", "cluster_id", s.clusterID, "topic", topic)
		case ts.WorkerIDsFound == 0:
			slog.Warn("topic produced no worker IDs",
				"cluster_id", s.clusterID, "topic", topic,
				"messages_read", ts.MessagesRead,
				"tombstones", ts.Tombstones,
				"parse_failures", ts.ParseFailures,
				"missing_field", ts.MissingField)
		}
	}

	sorted := make([]string, 0, len(workers))
	for a := range workers {
		sorted = append(sorted, a)
	}
	sort.Strings(sorted)

	for _, a := range sorted {
		_, _ = fmt.Fprintln(s.stdout, a)
	}

	_, _ = fmt.Fprintf(s.stderr, "\nDiscovered %d unique Connect worker address(es) for cluster %q\n",
		len(sorted), s.clusterID)

	if len(sorted) == 0 {
		_, _ = fmt.Fprintln(s.stderr, "No Connect worker addresses were discovered.")
		return fmt.Errorf("no Connect worker addresses discovered")
	}

	return nil
}
