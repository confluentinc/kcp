// Command txn-discovery observes a Kafka cluster over a window and derives the
// groups of topics that are coupled by transactions and must migrate atomically.
//
// It is the standalone POC for KCP's `migration group-discovery` command.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/config"
	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/discovery"
	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/grouping"
	kfk "github.com/confluentinc/kcp/utils/transaction-discovery/internal/kafka"
	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/report"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		cfg      config.Config
		brokers  string
		saslStr  string
		logLevel string
	)

	cmd := &cobra.Command{
		Use:   "txn-discovery",
		Short: "Discover transactional topic groups on a Kafka cluster (KCP POC)",
		Long: "Observes a running Kafka cluster over --duration by reading the\n" +
			"__transaction_state internal log from the start as the source of truth,\n" +
			"reconstructing each transaction's footprint and grouping the topics that are\n" +
			"coupled by a transaction so they can be migrated atomically.\n\n" +
			"Requires read access to __transaction_state, so it works against self-managed,\n" +
			"Confluent Platform, and AWS MSK Provisioned clusters — but not Confluent Cloud\n" +
			"or MSK Serverless, which do not expose the topic. Auth is configurable\n" +
			"(PLAIN, SCRAM-SHA-256/512, and mutual TLS) so one binary targets AWS MSK,\n" +
			"Confluent Platform, and self-managed Kafka alike.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg.Brokers = splitCSV(brokers)
			cfg.SASL = config.SASLMechanism(strings.ToLower(saslStr))
			if cfg.Password == "" {
				cfg.Password = os.Getenv("TXN_DISCOVERY_PASSWORD")
			}
			return run(cmd.Context(), cfg, newLogger(logLevel))
		},
	}

	f := cmd.Flags()
	f.StringVar(&brokers, "brokers", "", "comma-separated bootstrap brokers (required)")
	f.StringVar(&saslStr, "sasl", "scram-sha-512", "SASL mechanism: none|plain|scram-sha-256|scram-sha-512")
	f.StringVar(&cfg.Username, "username", "", "SASL username")
	f.StringVar(&cfg.Password, "password", "", "SASL password (prefer the TXN_DISCOVERY_PASSWORD env var)")
	f.BoolVar(&cfg.TLS, "tls", true, "connect over TLS (SASL_SSL)")
	f.BoolVar(&cfg.TLSInsecure, "tls-insecure", false, "skip TLS certificate verification (dev only)")
	f.StringVar(&cfg.CACertFile, "ca-cert", "", "PEM CA bundle for self-signed clusters (defaults to system roots)")
	f.StringVar(&cfg.ClientCertFile, "tls-cert", "", "client certificate (PEM) for mutual TLS (needs --tls-key)")
	f.StringVar(&cfg.ClientKeyFile, "tls-key", "", "client private key (PEM) for mutual TLS (needs --tls-cert)")
	f.DurationVar(&cfg.Duration, "duration", 5*time.Minute, "how long to observe the cluster")
	f.DurationVar(&cfg.Interval, "interval", 30*time.Second, "cadence at which consumer-group enrichment refreshes and flushes")
	f.StringVar(&cfg.TxnStateTopic, "txn-state-topic", discovery.DefaultTxnStateTopic, "name of the transaction-state internal topic (the source of truth, read from the start)")
	f.BoolVar(&cfg.EnrichConsumerGroups, "enrich-consumer-groups", true, "recover the consumed input topics of read-process-write apps via consumer-group offsets, correlated by the Kafka Streams transactional.id<->group.id naming convention")
	f.BoolVar(&cfg.TailConsumerOffsets, "tail-consumer-offsets", true, "recover consumed inputs of arbitrary (non-Streams) EOS apps by exact producer-id correlation, tailing __consumer_offsets (needs internal-topic read; falls back to the naming heuristic where inaccessible)")
	f.BoolVar(&cfg.IncludeInternalTopics, "include-internal-topics", false, "keep __-prefixed topics in grouping (debug)")
	f.StringVar(&cfg.OutputPath, "out", "migration.yaml", "path to write the discovered groups")
	f.StringVar(&cfg.StatsOutputPath, "stats-out", "", "also write a JSON diagnostics report (per-source overlap, keep-up metrics, per-txn footprints) to this path")
	f.BoolVar(&cfg.DryRun, "dry-run", false, "print the summary but do not write the output file")
	f.StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error")
	_ = cmd.MarkFlagRequired("brokers")

	return cmd
}

func run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	admin, cl, err := kfk.NewAdmin(cfg)
	if err != nil {
		return fmt.Errorf("build kafka client: %w", err)
	}
	defer cl.Close()

	// Preflight: __transaction_state is the source of truth, so require read access to
	// it up front. This both catches bad brokers/auth (the probe is a metadata call that
	// fails on a bad connection) and rejects clusters that don't expose the topic —
	// Confluent Cloud / MSK Serverless — where there is no admin-sampling fallback.
	pfCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	ok, reason := discovery.TxnStateAvailable(pfCtx, admin, cfg.TxnStateTopic)
	cancel()
	switch {
	case reason != nil:
		return fmt.Errorf("preflight failed reaching %s "+
			"(check --brokers/--sasl/--username/--password and ACLs): %w", cfg.TxnStateTopic, reason)
	case !ok:
		return fmt.Errorf("the %s topic is not readable on this cluster; this tool reads it as the "+
			"source of truth, so discovery cannot run here (managed offerings like Confluent Cloud "+
			"and MSK Serverless do not expose it)", cfg.TxnStateTopic)
	}

	log.Info("starting discovery",
		"brokers", cfg.Brokers, "duration", cfg.Duration, "interval", cfg.Interval)

	// The run ends when --duration elapses or on SIGINT/SIGTERM.
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	runCtx, cancelDur := context.WithTimeout(runCtx, cfg.Duration)
	defer cancelDur()

	acc := discovery.NewAccumulator()
	obs := make(chan discovery.Observation, 256)

	var consumeWG sync.WaitGroup
	consumeWG.Add(1)
	go func() {
		defer consumeWG.Done()
		for o := range obs {
			acc.Add(o)
		}
	}()

	// The shared catalog is populated by the __transaction_state reader and read by the
	// consumer-group phases (transactional ids + producerID->txnID), so no source calls
	// the transaction admin APIs.
	catalog := discovery.NewTxnCatalog()

	// Source of truth: read __transaction_state from the earliest offset in a continuous
	// fetch loop. Availability was confirmed by preflight, so this always runs.
	consumer, cerr := kfk.NewClient(cfg,
		kgo.ConsumeTopics(cfg.TxnStateTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if cerr != nil {
		return fmt.Errorf("build transaction-state consumer: %w", cerr)
	}
	defer consumer.Close()
	tail := &discovery.TxnStateLogTail{
		Consumer: consumer, Topic: cfg.TxnStateTopic, Catalog: catalog, Log: log,
	}
	sources := []discovery.Source{tail}

	// Phase 3: recover the consumed input topics of read-process-write apps via the
	// consumer-group admin APIs, correlating on the transactional ids the reader
	// registered in the catalog.
	var enricher *discovery.ConsumerGroupEnricher
	if cfg.EnrichConsumerGroups {
		enricher = &discovery.ConsumerGroupEnricher{Admin: admin, Catalog: catalog, Interval: cfg.Interval, Log: log}
		sources = append(sources, enricher)
	}

	// Phase 4: exact correlation for arbitrary (non-Streams) EOS apps. Tail
	// __consumer_offsets and tie each transactional offset commit to its transaction by
	// producer id (from the catalog), recovering consumed inputs even when
	// transactional.id bears no relation to group.id. Reads an internal topic, so it is
	// gated by an availability probe; where inaccessible the tool relies on Phase 3.
	var offsetsTail *discovery.ConsumerOffsetsTail
	offsetsTailActive := false
	if cfg.TailConsumerOffsets {
		ok, reason := discovery.ConsumerOffsetsAvailable(runCtx, admin, discovery.DefaultConsumerOffsetsTopic)
		switch {
		case ok:
			offsetsConsumer, oerr := kfk.NewClient(cfg,
				kgo.ConsumeTopics(discovery.DefaultConsumerOffsetsTopic),
				kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
			)
			if oerr != nil {
				return fmt.Errorf("build consumer-offsets consumer: %w", oerr)
			}
			defer offsetsConsumer.Close()
			offsetsTail = &discovery.ConsumerOffsetsTail{
				Consumer: offsetsConsumer, Catalog: catalog, Topic: discovery.DefaultConsumerOffsetsTopic,
				Interval: cfg.Interval, Log: log,
			}
			sources = append(sources, offsetsTail)
			offsetsTailActive = true
		default:
			log.Warn("consumer-offsets log not accessible; relying on Phase 3 naming heuristic for EOS input recovery",
				"topic", discovery.DefaultConsumerOffsetsTopic, "reason", reason)
		}
	}

	var srcWG sync.WaitGroup
	for _, s := range sources {
		srcWG.Add(1)
		go func(s discovery.Source) {
			defer srcWG.Done()
			if err := s.Run(runCtx, obs); err != nil {
				log.Error("source failed", "source", s.Name(), "err", err)
			}
		}(s)
	}
	srcWG.Wait()
	close(obs)
	consumeWG.Wait()

	footprints := acc.Snapshot()
	txns := make([]grouping.Transaction, 0, len(footprints))
	for _, fp := range footprints {
		txns = append(txns, grouping.Transaction{
			ID:               fp.TxnID,
			Topics:           fp.Topics,
			ReadProcessWrite: fp.ReadProcessWrite,
		})
	}
	result := grouping.Build(txns, grouping.Options{IncludeInternalTopics: cfg.IncludeInternalTopics})

	withData := map[string]struct{}{}
	for _, fp := range footprints {
		for _, s := range fp.Sources {
			withData[s] = struct{}{}
		}
	}

	summary := report.Summary{
		Duration:          cfg.Duration,
		Interval:          cfg.Interval,
		ActiveSources:     sourceNames(sources),
		SourcesWithData:   sortedKeys(withData),
		EnrichmentActive:  cfg.EnrichConsumerGroups,
		OffsetsTailActive: offsetsTailActive,
		TxnCount:          len(footprints),
		Result:            result,
	}
	// The consumed inputs recovered by Phase 3 (naming) and Phase 4 (producer id) are
	// unioned — either phase may fold in a topic the other missed — but kept separately
	// too so the report can attribute each topic to the mechanism that actually found it.
	var byNaming, byOffsets []string
	if enricher != nil {
		byNaming = enricher.Stats().RecoveredTopics
	}
	if offsetsTail != nil {
		byOffsets = offsetsTail.Stats().RecoveredTopics
	}
	recovered := map[string]struct{}{}
	for _, t := range byNaming {
		recovered[t] = struct{}{}
	}
	for _, t := range byOffsets {
		recovered[t] = struct{}{}
	}
	summary.RecoveredInputTopics = sortedKeys(recovered)
	summary.RecoveredByNaming = byNaming
	summary.RecoveredByOffsets = byOffsets
	tailStats := tail.Stats()
	summary.TxnCommitted = tailStats.Committed
	summary.TxnAborted = tailStats.Aborted
	report.PrintTerminal(os.Stdout, summary)

	stats := report.Stats{
		Duration:          cfg.Duration,
		Interval:          cfg.Interval,
		TxnStateActive:    true, // the __transaction_state reader is required and always runs
		OffsetsTailActive: offsetsTailActive,
		Footprints:        footprints,
		Tail:              tailStats,
	}
	if offsetsTail != nil {
		stats.OffsetsTail = offsetsTail.Stats()
	}
	report.PrintStats(os.Stdout, stats)

	if cfg.DryRun {
		log.Info("dry-run: not writing output file")
		return nil
	}
	if cfg.StatsOutputPath != "" {
		if err := report.WriteStatsJSON(cfg.StatsOutputPath, stats); err != nil {
			return fmt.Errorf("write stats: %w", err)
		}
	}
	if err := report.WriteMigrationYAML(cfg.OutputPath, summary); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	fmt.Printf("\nWrote %d group(s) to %s (review topics, then set each bootstrap_url).\n",
		len(result.Groups), cfg.OutputPath)
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sourceNames(ss []discovery.Source) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name()
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
