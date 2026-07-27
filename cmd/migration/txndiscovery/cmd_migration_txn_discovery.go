// Package txndiscovery wires `kcp migration txn-discovery`: the flags, the
// preflight, and the run orchestration that drives the txndiscovery services.
package txndiscovery

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
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
		PreRunE:       preRunTxnDiscovery,
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

func preRunTxnDiscovery(cmd *cobra.Command, args []string) error {
	// Every flag also reads from its uppercase, underscored environment
	// equivalent. For the two password flags that is the documented path: a
	// value passed by flag is visible in the process list.
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
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

func runTxnDiscovery(cmd *cobra.Command, args []string) error {
	return nil
}
