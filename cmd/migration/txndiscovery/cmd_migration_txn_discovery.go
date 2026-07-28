// Package txndiscovery wires `kcp migration txn-discovery`: the flags, the
// preflight, and the run orchestration that drives the txndiscovery services.
package txndiscovery

import (
	"context"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// DefaultOutPath is where the discovered groups land unless --out says otherwise.
	DefaultOutPath = "txn-discovery.yaml"
	// DefaultAuditBasename is the audit trail's filename. It is resolved against
	// --out's directory, so pointing --out at another directory takes the trail
	// with it rather than stranding it in the working directory.
	DefaultAuditBasename = "txn-discovery-audit.jsonl"
)

var (
	sourceBootstrap string
	awsRegion       string

	useSaslIam                  bool
	useSaslScram                bool
	useSaslPlain                bool
	useTls                      bool
	useUnauthenticatedTLS       bool
	useUnauthenticatedPlaintext bool

	saslScramUsername  string
	saslScramPassword  string
	saslScramMechanism string

	saslPlainUsername string
	saslPlainPassword string

	tlsCaCert             string
	tlsClientCert         string
	tlsClientKey          string
	insecureSkipTLSVerify bool

	duration              time.Duration
	interval              time.Duration
	txnStateTopic         string
	enrichConsumerGroups  bool
	tailConsumerOffsets   bool
	includeInternalTopics bool

	outPath      string
	statsOutPath string
	auditLogPath string
	noAuditLog   bool
	dryRun       bool
)

// NewMigrationTxnDiscoveryCmd builds the `kcp migration txn-discovery` command.
func NewMigrationTxnDiscoveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "txn-discovery",
		Short: "Discover which topics are coupled by Kafka transactions and must migrate together",
		Long: `Observe a source cluster for a window and reconstruct which topics are coupled by transactions.

Topics written inside one transaction must migrate together: move one without
the others and an in-flight transaction spans two clusters, which no cluster
link can reconcile.

The command reads ` + "`__transaction_state`" + ` from the beginning as its source of truth,
reconstructing each transaction's produced footprint. Two optional phases then
recover the topics those transactions CONSUMED, which the footprint never names:
consumer-group enrichment correlates by the Kafka Streams naming convention, and
the consumer-offsets tail correlates by record-batch producer id, which needs no
naming assumption. Both are on by default and each can be turned off.

Nothing is written to the cluster and no message payloads are read — only the
keys and values of internal metadata topics.

Requires READ on the transaction-state topic, and optionally READ on the
consumer-offsets log and DESCRIBE on consumer groups. Preflight fails fast when
the transaction-state topic cannot be read, so managed offerings that do not
expose it (Confluent Cloud, MSK Serverless) are rejected rather than silently
producing an empty result.

The terminal summary reports counts only. Topic names and transactional ids
identify customer business structure, so they live in the artifacts:

  txn-discovery.yaml         the groups, one entry per set of coupled topics
  txn-discovery-audit.jsonl  a live JSONL trace of every grouping decision, so
                             "which transaction coupled these two topics?" can
                             be answered after the run
  --stats-out                keep-up metrics and per-transaction footprints

All three are written 0600. The command never deletes them; remove them when the
engagement ends.

Nothing in kcp reads txn-discovery.yaml — it is for a human to review.

Credentials come from the flags below or their uppercase, underscored
environment equivalents. Prefer the environment: a value passed by flag is
visible in the process list.`,
		Example: `  # MSK with IAM, observing for ten minutes
  kcp migration txn-discovery \
      --source-bootstrap b-1.cluster.kafka.us-east-1.amazonaws.com:9098 \
      --use-sasl-iam --aws-region us-east-1 \
      --duration 10m

  # Apache Kafka with SASL/SCRAM, password from the environment
  export SASL_SCRAM_PASSWORD=...
  kcp migration txn-discovery \
      --source-bootstrap broker1:9096,broker2:9096 \
      --use-sasl-scram --sasl-scram-username kcp-reader --sasl-scram-mechanism SHA256 \
      --out ./engagement/txn-discovery.yaml

  # See what would be produced without writing anything
  kcp migration txn-discovery \
      --source-bootstrap broker1:9092 \
      --use-unauthenticated-plaintext \
      --duration 2m --dry-run`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunTxnDiscovery,
		RunE:          runTxnDiscovery,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&sourceBootstrap, "source-bootstrap", "", "Bootstrap server(s) of the source Kafka cluster (e.g. broker1:9092,broker2:9092).")
	cmd.Flags().AddFlagSet(requiredFlags)

	observationFlags := pflag.NewFlagSet("observation", pflag.ExitOnError)
	observationFlags.SortFlags = false
	observationFlags.DurationVar(&duration, "duration", 5*time.Minute, "How long to observe the cluster. A longer window observes more transactions; a workload whose footprint records were compacted before the window opened is unrecoverable.")
	observationFlags.DurationVar(&interval, "interval", 30*time.Second, "Cadence at which consumer-group enrichment refreshes and buffered producer-id sightings are resolved.")
	observationFlags.StringVar(&txnStateTopic, "txn-state-topic", discovery.DefaultTxnStateTopic, "Name of the transaction-state internal topic — the source of truth, read from the beginning.")
	observationFlags.BoolVar(&enrichConsumerGroups, "enrich-consumer-groups", true, "Recover the consumed input topics of read-process-write applications from consumer-group offsets, correlated by the Kafka Streams transactional.id/group.id naming convention. Needs describe on consumer groups.")
	observationFlags.BoolVar(&tailConsumerOffsets, "tail-consumer-offsets", true, "Recover consumed inputs of arbitrary (non-Streams) exactly-once applications by exact producer-id correlation, tailing the consumer-offsets log. Needs read on that internal topic; the run warns and continues on the naming convention alone where it is unreadable.")
	observationFlags.BoolVar(&includeInternalTopics, "include-internal-topics", false, "Keep internal (__-prefixed) topics in the grouping. Debug only: the consumer-offsets log is shared by every exactly-once application, so including it chains unrelated workloads into one group.")
	cmd.Flags().AddFlagSet(observationFlags)

	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication for the source cluster.")
	authFlags.BoolVar(&useSaslPlain, "use-sasl-plain", false, "Use SASL/PLAIN authentication for the source cluster.")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS (mutual TLS) authentication for the source cluster.")
	authFlags.BoolVar(&useUnauthenticatedTLS, "use-unauthenticated-tls", false, "Use unauthenticated (TLS encryption) for the source cluster.")
	authFlags.BoolVar(&useUnauthenticatedPlaintext, "use-unauthenticated-plaintext", false, "Use unauthenticated (plaintext) for the source cluster.")
	cmd.Flags().AddFlagSet(authFlags)

	iamFlags := pflag.NewFlagSet("iam", pflag.ExitOnError)
	iamFlags.SortFlags = false
	iamFlags.StringVar(&awsRegion, "aws-region", "", "AWS region of the source MSK cluster (e.g. us-east-1).")
	cmd.Flags().AddFlagSet(iamFlags)

	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username for the source cluster.")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password for the source cluster. Prefer the SASL_SCRAM_PASSWORD environment variable: a flag value is visible in the process list.")
	saslScramFlags.StringVar(&saslScramMechanism, "sasl-scram-mechanism", "SHA512", "SASL/SCRAM mechanism (SHA256 or SHA512). Defaults to SHA512 for MSK compatibility.")
	cmd.Flags().AddFlagSet(saslScramFlags)

	saslPlainFlags := pflag.NewFlagSet("sasl-plain", pflag.ExitOnError)
	saslPlainFlags.SortFlags = false
	saslPlainFlags.StringVar(&saslPlainUsername, "sasl-plain-username", "", "SASL/PLAIN username for the source cluster.")
	saslPlainFlags.StringVar(&saslPlainPassword, "sasl-plain-password", "", "SASL/PLAIN password for the source cluster. Prefer the SASL_PLAIN_PASSWORD environment variable: a flag value is visible in the process list.")
	cmd.Flags().AddFlagSet(saslPlainFlags)

	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to the TLS CA certificate for the source cluster.")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to the TLS client certificate for the source cluster.")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to the TLS client key for the source cluster.")
	tlsFlags.BoolVar(&insecureSkipTLSVerify, "insecure-skip-tls-verify", false, "Skip TLS certificate verification for Kafka connections. Supported with --use-sasl-scram only — the other authentication modes cannot honour it, and naming it alongside one of them is rejected rather than ignored. Lab use only.")
	cmd.Flags().AddFlagSet(tlsFlags)

	outputFlags := pflag.NewFlagSet("output", pflag.ExitOnError)
	outputFlags.SortFlags = false
	outputFlags.StringVar(&outPath, "out", DefaultOutPath, "Path to write the discovered groups to. Written 0600: it enumerates customer topic names.")
	outputFlags.StringVar(&statsOutPath, "stats-out", "", "Also write a JSON diagnostics report — per-partition keep-up metrics, decode-failure counts, and the full per-transaction footprints — to this path. Written 0600.")
	outputFlags.StringVar(&auditLogPath, "audit-log-out", "", "Path for the JSONL audit trail explaining every grouping decision. Defaults to "+DefaultAuditBasename+" beside --out. Written 0600.")
	outputFlags.BoolVar(&noAuditLog, "no-audit-log", false, "Do not write the audit trail. It is the only artifact that answers why two topics were grouped, so disabling it is rarely what you want.")
	outputFlags.BoolVar(&dryRun, "dry-run", false, "Observe and print the summary but write no files. Does not suppress kcp.log.")
	cmd.Flags().AddFlagSet(outputFlags)

	groups := []struct {
		name  string
		flags *pflag.FlagSet
	}{
		{"Required Flags", requiredFlags},
		{"Observation Flags", observationFlags},
		{"Output Flags", outputFlags},
		{"Source Cluster Authentication Flags", authFlags},
		{"IAM Flags", iamFlags},
		{"SASL/SCRAM Flags", saslScramFlags},
		{"SASL/PLAIN Flags", saslPlainFlags},
		{"TLS Flags", tlsFlags},
	}
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		w := c.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%s\n\n", c.Short)
		for _, g := range groups {
			if usage := g.flags.FlagUsages(); usage != "" {
				_, _ = fmt.Fprintf(w, "%s:\n%s\n", g.name, usage)
			}
		}
		_, _ = fmt.Fprintln(w, "All flags can be provided via environment variables (uppercase, with underscores).")
		// A custom usage function replaces cobra's default template, which is
		// what would otherwise have rendered these. Without this they reach
		// only the generated docs and never an operator running --help.
		if c.Example != "" {
			_, _ = fmt.Fprintf(w, "\nExamples:\n%s\n", c.Example)
		}
		return nil
	})

	_ = cmd.MarkFlagRequired("source-bootstrap")
	cmd.MarkFlagsMutuallyExclusive(authFlagNames...)
	cmd.MarkFlagsOneRequired(authFlagNames...)
	cmd.MarkFlagsMutuallyExclusive("audit-log-out", "no-audit-log")

	return cmd
}

// authFlagNames are the mutually exclusive source-cluster authentication flags,
// exactly as `kcp migration execute` declares them.
var authFlagNames = []string{
	"use-sasl-iam",
	"use-sasl-scram",
	"use-sasl-plain",
	"use-tls",
	"use-unauthenticated-tls",
	"use-unauthenticated-plaintext",
}

func preRunTxnDiscovery(cmd *cobra.Command, args []string) error {
	// Every flag also reads from its uppercase, underscored environment
	// equivalent. For the two password flags that is the documented path: a
	// value passed by flag is visible in the process list.
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// A non-positive window would start every reader, observe nothing and write
	// an authoritative-looking empty result; a non-positive interval would panic
	// time.NewTicker part-way through the run.
	if duration <= 0 {
		return fmt.Errorf("--duration must be greater than zero (got %s)", duration)
	}
	if interval <= 0 {
		return fmt.Errorf("--interval must be greater than zero (got %s)", interval)
	}

	// --insecure-skip-tls-verify reaches exactly one of the six auth modes.
	//
	// It is threaded correctly as far as client.AdminOptionForAuthMethod, but only
	// WithSASLSCRAMAuth takes the parameter: WithIAMAuth, WithTLSAuth and
	// WithUnauthenticatedTlsAuth have no such argument and drop it, and the two
	// plaintext modes have no TLS for it to apply to. Ignoring it is fail-closed —
	// verification stays ON — but the flag's own help offered it for "Kafka
	// connections" without qualification, so an operator in a lab with a self-signed
	// certificate and --use-tls got a certificate error they could not suppress and no
	// indication why. Better a flag error now than that.
	//
	// Rejected rather than threaded through the other three constructors: those live in
	// internal/client and are shared with other commands, so widening their signatures
	// is a larger change than this one.
	//
	// Keyed on the resolved VALUE, so the environment path is covered too, and only
	// when an auth mode is actually selected, so a run that named no mode still gets
	// cobra's own message about that rather than this one.
	if insecureSkipTLSVerify && (useSaslIam || useSaslPlain || useTls || useUnauthenticatedTLS || useUnauthenticatedPlaintext) {
		return fmt.Errorf("--insecure-skip-tls-verify is only supported with --use-sasl-scram, the one authentication mode whose client accepts it: --use-sasl-iam, --use-tls and --use-unauthenticated-tls would ignore it and keep verifying certificates, and --use-sasl-plain and --use-unauthenticated-plaintext use no TLS at all")
	}

	if useSaslIam {
		_ = cmd.MarkFlagRequired("aws-region")
	}

	if useSaslPlain {
		_ = cmd.MarkFlagRequired("sasl-plain-username")
		_ = cmd.MarkFlagRequired("sasl-plain-password")
	}

	if useTls {
		_ = cmd.MarkFlagRequired("tls-ca-cert")
		_ = cmd.MarkFlagRequired("tls-client-cert")
		_ = cmd.MarkFlagRequired("tls-client-key")
	}

	if useSaslScram {
		_ = cmd.MarkFlagRequired("sasl-scram-username")
		_ = cmd.MarkFlagRequired("sasl-scram-password")

		// NewKafkaClient assigns the SCRAM configuration error to _, so an
		// unsupported mechanism there yields a client that simply cannot
		// authenticate, reported as a connection failure hours of debugging
		// away from its cause. Validate it here, while it is still a flag.
		switch saslScramMechanism {
		case "SHA256", "SHA512":
			// supported
		default:
			return fmt.Errorf("invalid --sasl-scram-mechanism %q: must be SHA256 or SHA512", saslScramMechanism)
		}
	}

	return nil
}

// authResolution is the source-cluster authentication choice, resolved from the
// flags into the triple client.AdminOptionForAuthMethod takes.
type authResolution struct {
	AuthType      types.AuthType
	Method        types.AuthMethodConfig
	SkipTLSVerify bool
}

// resolveAuth turns the auth flags into that triple. MarkFlagsOneRequired and
// MarkFlagsMutuallyExclusive guarantee exactly one branch is live.
func resolveAuth() authResolution {
	r := authResolution{SkipTLSVerify: insecureSkipTLSVerify}
	switch {
	case useSaslIam:
		r.AuthType = types.AuthTypeIAM
		r.Method.IAM = &types.IAMConfig{Use: true}
	case useSaslScram:
		r.AuthType = types.AuthTypeSASLSCRAM
		r.Method.SASLScram = &types.SASLScramConfig{
			Use:       true,
			Username:  saslScramUsername,
			Password:  saslScramPassword,
			Mechanism: saslScramMechanism,
		}
	case useSaslPlain:
		r.AuthType = types.AuthTypeSASLPlain
		r.Method.SASLPlain = &types.SASLPlainConfig{
			Use:      true,
			Username: saslPlainUsername,
			Password: saslPlainPassword,
		}
	case useTls:
		r.AuthType = types.AuthTypeTLS
		r.Method.TLS = &types.TLSConfig{
			Use:        true,
			CACert:     tlsCaCert,
			ClientCert: tlsClientCert,
			ClientKey:  tlsClientKey,
		}
	case useUnauthenticatedTLS:
		r.AuthType = types.AuthTypeUnauthenticatedTLS
		r.Method.UnauthenticatedTLS = &types.UnauthenticatedTLSConfig{Use: true}
	case useUnauthenticatedPlaintext:
		r.AuthType = types.AuthTypeUnauthenticatedPlaintext
		r.Method.UnauthenticatedPlaintext = &types.UnauthenticatedPlaintextConfig{Use: true}
	}
	return r
}

// runDiscovery is the command's action, behind a variable so the flag tests can
// exercise validation without reaching a broker.
var runDiscovery = func(ctx context.Context, opts Opts) error {
	return NewRunner(opts).Run(ctx)
}

func runTxnDiscovery(cmd *cobra.Command, args []string) error {
	opts := parseOpts()
	// R5: the POC's --log-level is dropped in favour of the root --verbose,
	// which is where kcp's console verbosity already lives. Looked up rather
	// than imported, because cmd/migration importing cmd would be a cycle.
	opts.Verbose = boolFlag(cmd, "verbose")
	return runDiscovery(cmd.Context(), opts)
}

// boolFlag reads an inherited boolean flag, reporting false when it is absent —
// which it is when the command is built standalone rather than under the root.
func boolFlag(cmd *cobra.Command, name string) bool {
	f := cmd.Flags().Lookup(name)
	return f != nil && f.Value.String() == "true"
}
