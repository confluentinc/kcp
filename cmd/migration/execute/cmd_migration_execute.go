package execute

import (
	"fmt"

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

	saslScramUsername string
	saslScramPassword string

	saslPlainUsername string
	saslPlainPassword string

	tlsCaCert             string
	tlsClientCert         string
	tlsClientKey          string
	insecureSkipTLSVerify bool
)

func NewMigrationExecuteCmd() *cobra.Command {
	migrationExecuteCmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute an initialized migration",
		Long: `Execute an initialized migration through its remaining workflow steps.

This command resumes a migration from its current state, progressing through:
lag checking, gateway fencing, topic promotion, and gateway switchover.

The migration must first be created with 'kcp migration init'. If execution is
interrupted, re-running this command will resume from the last completed step.`,
		SilenceErrors: true,
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
	migrationExecuteCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&insecureSkipTLSVerify, "insecure-skip-tls-verify", false, "Skip TLS certificate verification for REST endpoint and Kafka connections.")
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
	migrationExecuteCmd.MarkFlagsMutuallyExclusive("use-sasl-iam", "use-sasl-scram", "use-sasl-plain", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")
	migrationExecuteCmd.MarkFlagsOneRequired("use-sasl-iam", "use-sasl-scram", "use-sasl-plain", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")

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

	return nil
}

func runMigrationExecute(cmd *cobra.Command, args []string) error {
	// Load migration state (following established pattern)
	migrationState, err := types.NewMigrationStateFromFile(migrationStateFile)
	if err != nil {
		return fmt.Errorf("failed to load migration state file %q: %w\nRun 'kcp migration init' to create a new migration first", migrationStateFile, err)
	}

	// Get MigrationConfig by ID with two-level error handling
	config, err := migrationState.GetMigrationById(migrationId)
	if err != nil {
		return fmt.Errorf("migration '%s' not found in %s\nRun 'kcp migration list' to see available migrations", migrationId, migrationStateFile)
	}

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

func parseMigrationExecutorOpts(migrationState types.MigrationState, config types.MigrationConfig) MigrationExecutorOpts {
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
		SaslPlainUsername:     saslPlainUsername,
		SaslPlainPassword:     saslPlainPassword,
		TlsCaCert:             tlsCaCert,
		TlsClientCert:         tlsClientCert,
		TlsClientKey:          tlsClientKey,
		InsecureSkipTLSVerify: insecureSkipTLSVerify,
	}
}
