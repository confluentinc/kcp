// Package txndiscovery wires `kcp migration txn-discovery`: the flags, the
// preflight, and the run orchestration that drives the txndiscovery services.
package txndiscovery

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
)

// NewMigrationTxnDiscoveryCmd builds the `kcp migration txn-discovery` command.
func NewMigrationTxnDiscoveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "txn-discovery",
		Short:         "Discover which topics are coupled by Kafka transactions and must migrate together",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE:          runTxnDiscovery,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&sourceBootstrap, "source-bootstrap", "", "Bootstrap server(s) of the source Kafka cluster (e.g. broker1:9092,broker2:9092).")
	cmd.Flags().AddFlagSet(requiredFlags)

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
	tlsFlags.BoolVar(&insecureSkipTLSVerify, "insecure-skip-tls-verify", false, "Skip TLS certificate verification for Kafka connections. Lab use only.")
	cmd.Flags().AddFlagSet(tlsFlags)

	_ = cmd.MarkFlagRequired("source-bootstrap")
	cmd.MarkFlagsMutuallyExclusive(authFlagNames...)
	cmd.MarkFlagsOneRequired(authFlagNames...)

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

func runTxnDiscovery(cmd *cobra.Command, args []string) error {
	return nil
}
