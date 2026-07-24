// Package config holds the runtime configuration for a discovery run.
package config

import "time"

// SASLMechanism enumerates the SASL mechanisms the tool can authenticate with.
// Keeping this configurable (rather than hard-coding SCRAM) is what lets a single
// binary target AWS MSK (typically SCRAM-SHA-512), Confluent Platform, and
// self-managed Kafka (SCRAM, PLAIN, or mutual TLS) alike.
type SASLMechanism string

const (
	SASLNone        SASLMechanism = "none"
	SASLPlain       SASLMechanism = "plain"
	SASLScramSHA256 SASLMechanism = "scram-sha-256"
	SASLScramSHA512 SASLMechanism = "scram-sha-512"
)

// Config is the full runtime configuration for a single discovery run.
type Config struct {
	// Connection.
	Brokers  []string
	SASL     SASLMechanism
	Username string
	Password string

	// TLS. MSK public endpoints use a public CA, so the system trust store is
	// enough; CACertFile is only needed for self-signed clusters (common on
	// self-managed Kafka / Confluent Platform). ClientCertFile + ClientKeyFile
	// enable mutual TLS (client-certificate auth), where the broker authenticates
	// the client by its certificate instead of (or alongside) SASL.
	TLS            bool
	TLSInsecure    bool
	CACertFile     string
	ClientCertFile string
	ClientKeyFile  string

	// Discovery loop.
	Duration time.Duration // total observation window
	Interval time.Duration // cadence at which the Phase 3/4 enrichment refreshes and flushes

	// Source of truth: the __transaction_state internal topic, read from the earliest
	// offset in a continuous fetch loop. TxnStateTopic names it. This is required — the
	// run fails fast if the topic is not readable (Confluent Cloud / MSK Serverless),
	// as there is no admin-sampling fallback.
	TxnStateTopic string

	// Phase 3: recover the CONSUMED input topics of read-process-write / EOS apps
	// (invisible in a transaction footprint) via the consumer-group admin APIs
	// (ListGroups / FetchOffsets). Correlation to a transaction uses the Kafka Streams
	// transactional.id<->group.id convention; the transactional ids come from the
	// __transaction_state reader via the shared catalog.
	EnrichConsumerGroups bool

	// Phase 4: recover those same consumed input topics by EXACT producer-id
	// correlation — tail __consumer_offsets, tie each transactional offset commit to
	// its transaction by producer id (the __transaction_state reader supplies the
	// producerID->txnID map). This covers arbitrary non-Streams consumer+producer EOS
	// apps that Phase 3's naming heuristic misses. It reads an internal topic, so it is
	// gated by an availability probe and falls back to Phase 3 where __consumer_offsets
	// is inaccessible.
	TailConsumerOffsets bool

	// Grouping.
	IncludeInternalTopics bool // debug: keep __-prefixed topics in grouping

	// Output.
	OutputPath string
	DryRun     bool

	// StatsOutputPath, if set, writes a machine-readable JSON diagnostics report
	// (reader keep-up metrics and the full per-transaction footprints) alongside
	// migration.yaml. Used by the stress harness to measure recall.
	StatsOutputPath string
}
