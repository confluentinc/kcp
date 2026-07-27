package txndiscovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/report"
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

// cluster is the broker-facing surface one run needs, behind one seam so the
// orchestration is testable without a broker.
type cluster struct {
	// Describer answers the preflight.
	Describer topicDescriber
	// Tail is the fetch seam both readers share.
	Tail tail.Client
	// Admin is the narrow consumer-group slice of sarama.ClusterAdmin. U7
	// deliberately did not widen kcp's KafkaAdmin interface for it, so it is
	// built here from the same sarama.Client the tail holds — and closed here.
	Admin discovery.ConsumerGroupAdmin
	// OffsetsProbe reports whether the consumer-offsets log can be read (R13).
	OffsetsProbe discovery.TopicProbe
	// Close releases the sarama client and the cluster admin built over it.
	Close func() error
}

// Runner orchestrates one discovery run.
type Runner struct {
	opts Opts

	// connect builds the broker-facing surface.
	connect func(Opts) (*cluster, error)

	// window blocks until the observation window should end — after the
	// configured duration, or as soon as ctx is done, which is how SIGINT ends
	// a run early.
	window func(ctx context.Context, d time.Duration)
}

// NewRunner builds a runner over a real cluster.
func NewRunner(opts Opts) *Runner {
	return &Runner{opts: opts, connect: connectSarama, window: waitWindow}
}

// waitWindow is the production observation window: the configured duration, or
// as soon as ctx is done — which is how SIGINT ends a run early.
func waitWindow(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// observationBuffer decouples the three sources from the single accumulator
// goroutine, so a burst on one source does not stall the fetch loops feeding
// the others.
const observationBuffer = 256

// Run performs one discovery run.
//
// The ordering is load-bearing: start the sources, hold the window, snapshot the
// tail's stats, cancel, drain the sources (which is where the offsets tail's
// final flush happens, on a fresh context), and only then write the artifacts.
// Writing before the final flush ships a YAML missing the late producer-id
// resolutions that phase exists to produce.
func (r *Runner) Run(ctx context.Context) error {
	cl, err := r.connect(r.opts)
	if err != nil {
		return err
	}
	defer func() { _ = cl.Close() }()

	// R12: fail before anything is created on disk or observed.
	if err := probeTxnStateTopic(cl.Describer, r.opts.TxnStateTopic); err != nil {
		return err
	}

	// R18/R20. A nil writer is the --no-audit-log (and --dry-run) case; every
	// AuditWriter method tolerates nil, so this is one branch rather than a nil
	// check at each call site.
	var audit *report.AuditWriter
	if !r.opts.DryRun && r.opts.AuditLogPath != "" {
		audit, err = report.NewAuditWriter(r.opts.AuditLogPath)
		if err != nil {
			return err
		}
	}
	closeAudit := sync.OnceValue(audit.Close)
	defer func() { _ = closeAudit() }()

	// SIGINT and SIGTERM end the window early; the artifacts are still written.
	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The catalog is populated by the transaction-state reader and read by both
	// enrichment phases, so neither calls the transaction admin APIs.
	catalog := discovery.NewTxnCatalog()
	acc := discovery.NewAccumulator()

	txnReader := discovery.NewTxnStateReader(r.opts.TxnStateTopic, catalog)
	specs := []tail.TopicSpec{{Topic: r.opts.TxnStateTopic, Start: tail.StartEarliest}}
	activeSources := []string{discovery.SourceTxnStateLog}

	// R9/R13: the offsets tail is gated on an availability probe. When the topic
	// is unreadable the object is kept anyway — its Stats carry the flag the
	// report needs to distinguish "ran and found nothing" from "never ran".
	var offsets *discovery.ConsumerOffsetsTail
	offsetsActive := false
	if r.opts.TailConsumerOffsets {
		offsets = discovery.NewConsumerOffsetsTail(catalog, discovery.ConsumerOffsetsOptions{
			Topic:    discovery.DefaultConsumerOffsetsTopic,
			Interval: r.opts.Interval,
			Probe:    cl.OffsetsProbe,
		})
		if offsets.Available(runCtx) {
			specs = append(specs, tail.TopicSpec{Topic: discovery.DefaultConsumerOffsetsTopic, Start: tail.StartLatest})
			activeSources = append(activeSources, discovery.SourceConsumerOffsets)
			offsetsActive = true
		}
	}
	if r.opts.EnrichConsumerGroups {
		activeSources = append(activeSources, discovery.SourceConsumerGroups)
	}

	tl := tail.New(cl.Tail, tail.Options{})
	batches, err := tl.Start(runCtx, specs)
	if err != nil {
		if errors.Is(err, tail.ErrFetchVersionUnsupported) {
			return fmt.Errorf("the source cluster cannot serve the reads this command needs: %w", err)
		}
		return fmt.Errorf("failed to start reading the source cluster: %w", err)
	}

	obs := make(chan discovery.Observation, observationBuffer)
	var accWG sync.WaitGroup
	accWG.Add(1)
	go func() {
		defer accWG.Done()
		// KTD7: the accumulator is the only component that knows whether an
		// observation grew a transaction's topic set, so the audit line
		// originates here rather than in the sources.
		for o := range obs {
			audit.Record(o, acc.Add(o))
		}
	}()

	dests := []chan tail.Batch{make(chan tail.Batch)}
	if offsetsActive {
		dests = append(dests, make(chan tail.Batch))
	}
	go fanOut(runCtx, batches, dests)

	var srcWG sync.WaitGroup
	srcWG.Add(1)
	go func() {
		defer srcWG.Done()
		_ = txnReader.Run(runCtx, dests[0], obs)
	}()
	if offsetsActive {
		srcWG.Add(1)
		go func() {
			defer srcWG.Done()
			// Run performs the final flush itself when its input closes, on a
			// fresh context, because runCtx is already cancelled by then and
			// that flush is where most resolutions land.
			_ = offsets.Run(runCtx, dests[1], obs)
		}()
	}
	if r.opts.EnrichConsumerGroups {
		enricher := &discovery.ConsumerGroupEnricher{
			Admin:    cl.Admin,
			Catalog:  catalog,
			Interval: r.opts.Interval,
			// Log must be set: the enricher dereferences it on a failed pass.
			Log: slog.Default(),
		}
		srcWG.Add(1)
		go func() {
			defer srcWG.Done()
			_ = enricher.Run(runCtx, obs)
		}()
	}

	r.window(sigCtx, r.opts.Duration)

	// BEFORE cancel. Every partition loop clears its running flag as it exits,
	// so a snapshot taken after shutdown reports zero partitions live and the
	// health line condemns a perfectly healthy run as failed.
	tailStats := tl.Stats()

	cancel()
	srcWG.Wait()
	close(obs)
	accWG.Wait()
	tl.Wait()

	footprints := acc.Snapshot()
	txns := make([]grouping.Transaction, 0, len(footprints))
	for _, fp := range footprints {
		txns = append(txns, grouping.Transaction{
			ID:               fp.TxnID,
			Topics:           fp.Topics,
			ReadProcessWrite: fp.ReadProcessWrite,
		})
	}

	// Closed before the counters are read: on a buffered or network filesystem
	// the deferred write error surfaces at close, and a failed close means lines
	// are missing just as a failed write does.
	_ = closeAudit()

	run := report.Run{
		Duration:         r.opts.Duration,
		Interval:         r.opts.Interval,
		ActiveSources:    activeSources,
		Footprints:       footprints,
		Result:           grouping.Build(txns, grouping.Options{IncludeInternalTopics: r.opts.IncludeInternalTopics}),
		Tail:             tailStats,
		TxnState:         txnReader.Stats(),
		AuditErrors:      audit.Errors(),
		EnrichmentActive: r.opts.EnrichConsumerGroups,
	}
	if offsets != nil {
		run.Offsets = offsets.Stats()
	}

	summary := report.Summarize(run)
	report.PrintTerminal(r.opts.Stdout, summary)
	if r.opts.Verbose {
		// KTD4: the detailed keep-up block is behind --verbose; the summary
		// carries one health line.
		report.PrintKeepUp(r.opts.Stdout, run)
	}

	if err := r.writeArtifacts(summary, run); err != nil {
		return err
	}

	// The exit code is this command's, not the report's: a truncated audit log
	// reads downstream as "no transaction coupled these topics", which is
	// indistinguishable from a clean run unless the run itself fails.
	if n := audit.Errors(); n > 0 {
		return fmt.Errorf("the audit trail is incomplete: %d line(s) could not be written, so it cannot be relied on to explain a grouping", n)
	}
	return nil
}

// writeArtifacts writes the YAML and, when asked for, the stats JSON. R20:
// --dry-run writes nothing at all — it does not suppress kcp.log.
func (r *Runner) writeArtifacts(summary report.Summary, run report.Run) error {
	if r.opts.DryRun {
		_, _ = fmt.Fprintf(r.opts.Stdout, "\nDry run: no files written.\n")
		return nil
	}
	if err := report.WriteYAML(r.opts.OutPath, summary); err != nil {
		return err
	}
	if r.opts.StatsOutPath != "" {
		if err := report.WriteStatsJSON(r.opts.StatsOutPath, run); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(r.opts.Stdout, "\nWrote %d group(s) to %s\n", len(summary.Result.Groups), r.opts.OutPath)
	return nil
}

// connectSarama builds the real cluster surface: one sarama.Client, the tail
// seam over it, and a cluster admin built from the same client.
//
// AdminOptionForAuthMethod is the erroring variant. AdminOptionForAuth, the
// other one, logs a warning and falls back to IAM when it cannot resolve the
// auth type — which against a non-MSK cluster is a puzzling authentication
// failure rather than the configuration error it actually is.
func connectSarama(opts Opts) (*cluster, error) {
	authOpt, err := client.AdminOptionForAuthMethod(opts.Auth.AuthType, opts.Auth.Method, opts.Auth.SkipTLSVerify)
	if err != nil {
		return nil, err
	}

	sc, err := client.NewKafkaClient(opts.Brokers, opts.Region, authOpt)
	if err != nil {
		return nil, err
	}

	// U7 deliberately did not widen kcp's KafkaAdmin interface for the two
	// consumer-group calls, so the admin is built here over the same client.
	// NewClusterAdminFromClient does not take ownership of it, which is why the
	// close below releases both, admin first.
	admin, err := sarama.NewClusterAdminFromClient(sc)
	if err != nil {
		_ = sc.Close()
		return nil, fmt.Errorf("failed to build a cluster admin for the source cluster: %w", err)
	}

	return &cluster{
		Describer: admin,
		Tail:      tail.NewSaramaClient(sc, 0),
		Admin:     admin,
		OffsetsProbe: func(context.Context) error {
			return probeReadableTopic(admin, discovery.DefaultConsumerOffsetsTopic)
		},
		Close: func() error {
			// Closing the admin closes the client it was built from, so the
			// client is not closed again here.
			return admin.Close()
		},
	}, nil
}

// probeReadableTopic reports whether an internal topic can be described, which
// is R13's availability check for the consumer-offsets log.
//
// The reason it returns reaches the console and kcp.log verbatim, so it names
// the failure without naming the topic.
func probeReadableTopic(d topicDescriber, topic string) error {
	md, err := d.DescribeTopics([]string{topic})
	if err != nil {
		return err
	}
	if len(md) == 0 || md[0] == nil {
		return fmt.Errorf("the broker returned no metadata for the consumer-offsets log")
	}
	if md[0].Err != sarama.ErrNoError {
		return md[0].Err
	}
	return nil
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
