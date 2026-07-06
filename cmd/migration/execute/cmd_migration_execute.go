package execute

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	migrationStateFile          string
	migrationId                 string
	lagThreshold                int64
	clusterApiKey               string
	clusterApiSecret            string
	awsRegion                   string
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

	tlsCaCert                       string
	tlsClientCert                   string
	tlsClientKey                    string
	insecureSkipTLSVerify           bool
	rolloutTimeout                  time.Duration
	detectUnroutedProducersDuration time.Duration
	promoteBatchSize                int
)

func NewMigrationExecuteCmd() *cobra.Command {
	migrationExecuteCmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute an initialized migration",
		Long: `Execute an initialized migration through its remaining workflow steps.

This command resumes a migration from its current state, progressing through:
lag checking, gateway fencing, topic promotion, and gateway switchover.

The migration must first be created with 'kcp migration init'. If execution is
interrupted, re-running this command will resume from the last completed step.

Credentials (cluster-api-key, cluster-api-secret) are intentionally not stored in
the migration state file and must be provided each time.`,
		Example: `  # MSK source with IAM auth
  kcp migration execute \
      --migration-id migration-a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
      --lag-threshold 0 \
      --cluster-api-key ABCDEFGHIJKLMNOP \
      --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
      --use-sasl-iam --aws-region us-east-1

  # Apache Kafka source with TLS
  kcp migration execute \
      --migration-id migration-a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
      --lag-threshold 0 \
      --cluster-api-key ABCDEFGHIJKLMNOP \
      --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
      --use-tls --tls-ca-cert ca.pem --tls-client-cert client.pem --tls-client-key client.key`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationExecute,
		RunE:          runMigrationExecute,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "Path to the migration state file.")
	requiredFlags.StringVar(&migrationId, "migration-id", "", "ID of the migration to execute (from 'kcp migration list').")
	requiredFlags.Int64Var(&lagThreshold, "lag-threshold", 0, "Total topic replication lag threshold (sum of all partition lags) before proceeding with migration.")
	requiredFlags.StringVar(&clusterApiKey, "cluster-api-key", "", "API key for authenticating with the destination cluster.")
	requiredFlags.StringVar(&clusterApiSecret, "cluster-api-secret", "", "API secret for authenticating with the destination cluster.")
	requiredFlags.DurationVar(&detectUnroutedProducersDuration, "detect-unrouted-producers-duration", 0, "Time to monitor source offsets after fencing to detect producers bypassing the gateway. Use 0 to skip. Minimum 10s if non-zero.")
	migrationExecuteCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&insecureSkipTLSVerify, "insecure-skip-tls-verify", false, "Skip TLS certificate verification for REST endpoint and Kafka connections.")
	optionalFlags.DurationVar(&rolloutTimeout, "rollout-timeout", 0, "Maximum time to wait for the Confluent operator to report the gateway as Ready during fence and switchover. 0 (the default) means no deadline — the wait runs until the operator converges or the user cancels.")
	optionalFlags.IntVar(&promoteBatchSize, "promote-batch-size", 0, "Maximum number of mirror topics to promote per batch. 0 (the default) promotes all topics at once. When set (>0), each batch is promoted and confirmed STOPPED before the next batch is submitted.")
	migrationExecuteCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	// Authentication flags.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslPlain, "use-sasl-plain", false, "Use SASL/PLAIN authentication for the source cluster.")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS authentication for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedTLS, "use-unauthenticated-tls", false, "Use unauthenticated (TLS encryption) for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedPlaintext, "use-unauthenticated-plaintext", false, "Use unauthenticated (plaintext) for the source MSK cluster.")
	migrationExecuteCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Source Cluster Authentication Flags"

	// SASL/SCRAM credential flags.
	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username for the source MSK cluster.")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password for the source MSK cluster.")
	saslScramFlags.StringVar(&saslScramMechanism, "sasl-scram-mechanism", "SHA512", "SASL/SCRAM mechanism (SHA256 or SHA512). Defaults to SHA512 for MSK compatibility.")
	migrationExecuteCmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Flags"

	// SASL/PLAIN credential flags.
	saslPlainFlags := pflag.NewFlagSet("sasl-plain", pflag.ExitOnError)
	saslPlainFlags.SortFlags = false
	saslPlainFlags.StringVar(&saslPlainUsername, "sasl-plain-username", "", "SASL/PLAIN username for the source cluster.")
	saslPlainFlags.StringVar(&saslPlainPassword, "sasl-plain-password", "", "SASL/PLAIN password for the source cluster.")
	migrationExecuteCmd.Flags().AddFlagSet(saslPlainFlags)
	groups[saslPlainFlags] = "SASL/PLAIN Flags"

	// IAM credential flags.
	iamFlags := pflag.NewFlagSet("iam", pflag.ExitOnError)
	iamFlags.SortFlags = false
	iamFlags.StringVar(&awsRegion, "aws-region", "", "AWS region of the source MSK cluster (e.g. us-east-1).")
	migrationExecuteCmd.Flags().AddFlagSet(iamFlags)
	groups[iamFlags] = "IAM Flags"

	// TLS credential flags.
	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to the TLS CA certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to the TLS client certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to the TLS client key for the source MSK cluster.")
	migrationExecuteCmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Flags"

	migrationExecuteCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, authFlags, iamFlags, saslScramFlags, saslPlainFlags, tlsFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Source Cluster Authentication Flags", "IAM Flags", "SASL/SCRAM Flags", "SASL/PLAIN Flags", "TLS Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = migrationExecuteCmd.MarkFlagRequired("migration-id")
	_ = migrationExecuteCmd.MarkFlagRequired("lag-threshold")
	_ = migrationExecuteCmd.MarkFlagRequired("cluster-api-key")
	_ = migrationExecuteCmd.MarkFlagRequired("cluster-api-secret")
	_ = migrationExecuteCmd.MarkFlagRequired("detect-unrouted-producers-duration")
	migrationExecuteCmd.MarkFlagsMutuallyExclusive("use-sasl-iam", "use-sasl-scram", "use-sasl-plain", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")
	migrationExecuteCmd.MarkFlagsOneRequired("use-sasl-iam", "use-sasl-scram", "use-sasl-plain", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")

	// If any credential in a pair/trio is set, the whole set must be set.
	migrationExecuteCmd.MarkFlagsRequiredTogether("sasl-scram-username", "sasl-scram-password")
	migrationExecuteCmd.MarkFlagsRequiredTogether("sasl-plain-username", "sasl-plain-password")
	migrationExecuteCmd.MarkFlagsRequiredTogether("tls-ca-cert", "tls-client-cert", "tls-client-key")

	return migrationExecuteCmd
}

func preRunMigrationExecute(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if useSaslIam {
		_ = cmd.MarkFlagRequired("aws-region")
	}

	if useSaslScram {
		_ = cmd.MarkFlagRequired("sasl-scram-username")
		_ = cmd.MarkFlagRequired("sasl-scram-password")
		switch saslScramMechanism {
		case "SHA256", "SHA512":
			// valid
		default:
			return fmt.Errorf("invalid --sasl-scram-mechanism %q: must be SHA256 or SHA512", saslScramMechanism)
		}
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

	if detectUnroutedProducersDuration != 0 && detectUnroutedProducersDuration < 10*time.Second {
		return fmt.Errorf("--detect-unrouted-producers-duration must be at least 10s (got %s). Use 0 to skip the check entirely", detectUnroutedProducersDuration)
	}

	return nil
}

func runMigrationExecute(cmd *cobra.Command, args []string) error {
	// Load migration state (following established pattern)
	migrationState, err := migration.NewMigrationStateFromFile(migrationStateFile)
	if err != nil {
		return fmt.Errorf("failed to load migration state file %q: %w\nRun 'kcp migration init' to create a new migration first", migrationStateFile, err)
	}

	// Get MigrationConfig by ID with two-level error handling
	config, err := migrationState.GetMigrationById(migrationId)
	if err != nil {
		return fmt.Errorf("migration '%s' not found in %s\nRun 'kcp migration list' to see available migrations", migrationId, migrationStateFile)
	}

	// Apply runtime flags to config (not stored at init time)
	config.DetectUnroutedProducersDuration = detectUnroutedProducersDuration

	opts := parseMigrationExecutorOpts(*migrationState, *config)

	migrationExecutor := NewMigrationExecutor(opts)
	if err := migrationExecutor.Run(); err != nil {
		return err
	}

	return nil
}

func resolveAuthType() types.AuthType {
	switch {
	case useSaslIam:
		return types.AuthTypeIAM
	case useSaslScram:
		return types.AuthTypeSASLSCRAM
	case useSaslPlain:
		return types.AuthTypeSASLPlain
	case useTls:
		return types.AuthTypeTLS
	case useUnauthenticatedTLS:
		return types.AuthTypeUnauthenticatedTLS
	case useUnauthenticatedPlaintext:
		return types.AuthTypeUnauthenticatedPlaintext
	default:
		panic("unreachable: MarkFlagsOneRequired guarantees an auth flag is set")
	}
}

func parseMigrationExecutorOpts(migrationState migration.MigrationState, config migration.MigrationConfig) MigrationExecutorOpts {
	return MigrationExecutorOpts{
		MigrationStateFile:    migrationStateFile,
		MigrationState:        migrationState,
		MigrationConfig:       config,
		LagThreshold:          lagThreshold,
		ClusterApiKey:         clusterApiKey,
		ClusterApiSecret:      clusterApiSecret,
		ClusterBootstrap:      config.ClusterBootstrap,
		SourceBootstrap:       config.SourceBootstrap,
		AWSRegion:             awsRegion,
		AuthType:              resolveAuthType(),
		SaslScramUsername:     saslScramUsername,
		SaslScramPassword:     saslScramPassword,
		SaslScramMechanism:    saslScramMechanism,
		SaslPlainUsername:     saslPlainUsername,
		SaslPlainPassword:     saslPlainPassword,
		TlsCaCert:             tlsCaCert,
		TlsClientCert:         tlsClientCert,
		TlsClientKey:          tlsClientKey,
		InsecureSkipTLSVerify: insecureSkipTLSVerify,
		RolloutTimeout:        rolloutTimeout,
		PromoteBatchSize:      promoteBatchSize,
	}
}
