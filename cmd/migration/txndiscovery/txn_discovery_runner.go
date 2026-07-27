package txndiscovery

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
)

// Opts is the resolved configuration of one discovery run: everything the
// runner needs, with no dependency on the flag variables it came from.
type Opts struct {
	// Brokers are the source cluster's bootstrap addresses and Region the AWS
	// region IAM signing needs.
	Brokers []string
	Region  string
	Auth    authResolution

	// Duration is the observation window; Interval the cadence at which
	// enrichment refreshes and buffered producer-id sightings are resolved.
	Duration time.Duration
	Interval time.Duration

	TxnStateTopic         string
	EnrichConsumerGroups  bool
	TailConsumerOffsets   bool
	IncludeInternalTopics bool

	// OutPath is the groups YAML. StatsOutPath and AuditLogPath are empty when
	// their artifact is not wanted — AuditLogPath is empty for --no-audit-log,
	// which the runner turns into a nil AuditWriter whose methods are all no-ops.
	OutPath      string
	StatsOutPath string
	AuditLogPath string

	// DryRun suppresses every artifact write. It does not suppress kcp.log.
	DryRun bool

	// Verbose mirrors the root --verbose flag and gates the detailed keep-up block.
	Verbose bool

	// Stdout is where the terminal narrative goes.
	Stdout io.Writer
}

// topicDescriber is the one cluster-admin call the preflight makes.
//
// DescribeTopics is used rather than the client's offset lookup because it
// returns a per-topic Kafka error code, which is what separates a mistyped
// --txn-state-topic from a missing ACL. The client's Exists path collapses both
// into "not in the topic list".
type topicDescriber interface {
	DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error)
}

// probeTxnStateTopic verifies the transaction-state log is there and readable.
//
// No error it returns carries the topic name: main slog.Error's every command
// error, so all of them land in kcp.log, and --txn-state-topic is
// operator-supplied.
func probeTxnStateTopic(d topicDescriber, topic string) error {
	md, err := d.DescribeTopics([]string{topic})
	if err != nil {
		return fmt.Errorf("could not read the transaction-state topic's metadata: check --source-bootstrap, the source authentication flags and that the credentials carry an ACL granting DESCRIBE and READ on it: %w", err)
	}
	if len(md) == 0 || md[0] == nil {
		return fmt.Errorf("the broker returned no metadata for the transaction-state topic named by --txn-state-topic, so it could not be confirmed readable")
	}

	switch md[0].Err {
	case sarama.ErrNoError:
		return nil
	case sarama.ErrUnknownTopicOrPartition:
		// kcp disables auto topic creation, so an unknown name stays unknown
		// rather than being created by the probe.
		return fmt.Errorf("the topic named by --txn-state-topic does not exist on this cluster: check the flag for a typo, and note that managed offerings such as Confluent Cloud and MSK Serverless do not expose it at all")
	default:
		return fmt.Errorf("the transaction-state topic named by --txn-state-topic is not readable: check the source authentication flags' credentials and that they carry an ACL granting DESCRIBE and READ on it: %w", md[0].Err)
	}
}

// fanOut copies every batch from the tail's single channel to each reader's own.
//
// tail.Tail deliberately exposes ONE channel carrying every partition of both
// topics, because both readers need the same lifecycle and shutdown ordering.
// A Go channel delivers each value to exactly one receiver, so handing that
// channel to both readers would split the stream between them: half the
// transaction-state records would arrive at the offsets tail, which drops
// anything not on its topic, and vice versa. Nothing would error — the run
// would simply observe half of what it read. Hence a copy per destination, and
// hence both readers also filter on Batch.Topic.
//
// Every destination is closed on the way out, whichever way that happens. Those
// closes are load-bearing: they are what makes the transaction-state reader
// return and what triggers the offsets tail's final flush.
func fanOut(ctx context.Context, src <-chan tail.Batch, dests []chan tail.Batch) {
	defer func() {
		for _, d := range dests {
			close(d)
		}
	}()
	for b := range src {
		for _, d := range dests {
			select {
			case d <- b:
			case <-ctx.Done():
				// A reader that has stopped receiving must not wedge shutdown.
				// The tail's own emit is cancellable, so abandoning the source
				// here leaves nothing blocked behind us.
				return
			}
		}
	}
}

// parseOpts snapshots the flag variables into an Opts.
func parseOpts() Opts {
	return Opts{
		Brokers:               splitCSV(sourceBootstrap),
		Region:                awsRegion,
		Auth:                  resolveAuth(),
		Duration:              duration,
		Interval:              interval,
		TxnStateTopic:         txnStateTopic,
		EnrichConsumerGroups:  enrichConsumerGroups,
		TailConsumerOffsets:   tailConsumerOffsets,
		IncludeInternalTopics: includeInternalTopics,
		OutPath:               outPath,
		StatsOutPath:          statsOutPath,
		AuditLogPath:          resolveAuditLogPath(outPath, auditLogPath, noAuditLog),
		DryRun:                dryRun,
		Stdout:                os.Stdout,
	}
}

// resolveAuditLogPath decides where the audit trail is written, or that it is
// not. An explicit path wins; otherwise the trail lands beside --out, because
// an operator reading txn-discovery.yaml is the one who needs the trail that
// explains it.
func resolveAuditLogPath(out, explicit string, disabled bool) string {
	if disabled {
		return ""
	}
	if explicit != "" {
		return explicit
	}
	return filepath.Join(filepath.Dir(out), DefaultAuditBasename)
}

// splitCSV parses a comma-separated bootstrap list, dropping blanks so a
// trailing comma does not become an empty broker address.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
