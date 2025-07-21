package cluster

import (
	"fmt"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp-internal/internal/client"
	"github.com/confluentinc/kcp-internal/internal/generators/scan/cluster"
	"github.com/confluentinc/kcp-internal/internal/types"
	"github.com/confluentinc/kcp-internal/internal/utils"

	"github.com/spf13/cobra"
)

var (
	clusterArn         string
	saslScramUsername  string
	saslScramPassword  string
	tlsCaCert          string
	tlsClientCert      string
	tlsClientKey       string
	skipKafka          bool
	useSaslIam         bool
	useSaslScram       bool
	useUnauthenticated bool
	useTls             bool
)

func NewScanClusterCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Scan a given cluster",
		Long: `Scan a given cluster for information that will help with migration.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                        | ENV_VAR
----------------------------|-----------------------------------------------------
Required flags:
--cluster-arn               | CLUSTER_ARN=arn:aws:kafka:us-east-1:1234567890:cluster/my-cluster/1234567890

Auth flags [choose one of the following]
--skip-kafka                | SKIP_KAFKA=true
--use-sasl-iam              | USE_SASL_IAM=true
--use-sasl-scram            | USE_SASL_SCRAM=true
--use-unauthenticated       | USE_UNAUTHENTICATED=true
--use-tls                   | USE_TLS=true

Provide with --use-sasl-scram
--sasl-scram-username       | SASL_SCRAM_USERNAME=msk-username
--sasl-scram-password       | SASL_SCRAM_PASSWORD=msk-password

Provide with --use-tls
--tls-ca-cert               | TLS_CA_CERT=path/to/ca-cert.pem
--tls-client-cert           | TLS_CLIENT_CERT=path/to/client-cert.pem
--tls-client-key            | TLS_CLIENT_KEY=path/to/client-key.pem
`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanCluster,
		RunE:          runScanCluster,
	}

	clusterCmd.Flags().StringVar(&clusterArn, "cluster-arn", "", "cluster arn")

	clusterCmd.Flags().StringVar(&saslScramUsername, "sasl-scram-username", "", "The SASL SCRAM username")
	clusterCmd.Flags().StringVar(&saslScramPassword, "sasl-scram-password", "", "The SASL SCRAM password")
	clusterCmd.Flags().StringVar(&tlsCaCert, "tls-ca-cert", "", "The TLS CA certificate")
	clusterCmd.Flags().StringVar(&tlsClientCert, "tls-client-cert", "", "The TLS client certificate")
	clusterCmd.Flags().StringVar(&tlsClientKey, "tls-client-key", "", "The TLS client key")

	clusterCmd.Flags().BoolVar(&skipKafka, "skip-kafka", false, "skip kafka level cluster scan, use when brokers are not reachable")
	clusterCmd.Flags().BoolVar(&useSaslIam, "use-sasl-iam", false, "use sasl iam authentication")
	clusterCmd.Flags().BoolVar(&useSaslScram, "use-sasl-scram", false, "use sasl scram authentication")
	clusterCmd.Flags().BoolVar(&useUnauthenticated, "use-unauthenticated", false, "use unauthenticated authentication")
	clusterCmd.Flags().BoolVar(&useTls, "use-tls", false, "use TLS authentication")

	clusterCmd.MarkFlagRequired("cluster-arn")
	clusterCmd.MarkFlagsMutuallyExclusive("skip-kafka", "use-sasl-iam", "use-sasl-scram", "use-unauthenticated", "use-tls")
	clusterCmd.MarkFlagsOneRequired("skip-kafka", "use-sasl-iam", "use-sasl-scram", "use-unauthenticated", "use-tls")

	return clusterCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunScanCluster(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if useSaslScram {
		cmd.MarkFlagRequired("sasl-scram-username")
		cmd.MarkFlagRequired("sasl-scram-password")
	}

	if useTls {
		cmd.MarkFlagRequired("tls-ca-cert")
		cmd.MarkFlagRequired("tls-client-cert")
		cmd.MarkFlagRequired("tls-client-key")
	}

	return nil
}

func runScanCluster(cmd *cobra.Command, args []string) error {
	opts, err := parseScanClusterOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan cluster opts: %v", err)
	}

	mskClient, err := client.NewMSKClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create msk client: %v", err)
	}

	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		switch opts.AuthType {
		case types.AuthTypeIAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithIAMAuth())
		case types.AuthTypeSASLSCRAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithSASLSCRAMAuth(opts.SASLScramUsername, opts.SASLScramPassword))
		case types.AuthTypeUnauthenticated:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithUnauthenticatedAuth())
		case types.AuthTypeTLS:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithTLSAuth(opts.TLSCACert, opts.TLSClientCert, opts.TLSClientKey))
		default:
			return nil, fmt.Errorf("❌ Auth type: %v not yet supported", opts.AuthType)
		}
	}

	// Scan the cluster
	clusterScanner := cluster.NewClusterScanner(mskClient, kafkaAdminFactory, *opts)
	if err := clusterScanner.Run(); err != nil {
		return fmt.Errorf("failed to scan cluster: %v", err)
	}

	return nil
}

func parseScanClusterOpts() (*cluster.ClusterScannerOpts, error) {
	// Extract region from ARN (format: arn:aws:service:region:account:resource)
	arnParts := strings.Split(clusterArn, ":")
	if len(arnParts) < 4 {
		return nil, fmt.Errorf("invalid cluster ARN format: %s", clusterArn)
	}
	region := arnParts[3]
	if region == "" {
		return nil, fmt.Errorf("region not found in cluster ARN: %s", clusterArn)
	}

	var authType types.AuthType
	switch {
	case useSaslIam:
		authType = types.AuthTypeIAM
	case useSaslScram:
		authType = types.AuthTypeSASLSCRAM
	case useUnauthenticated:
		authType = types.AuthTypeUnauthenticated
	case useTls:
		authType = types.AuthTypeTLS
	}

	opts := cluster.ClusterScannerOpts{
		Region:            region,
		ClusterArn:        clusterArn,
		SkipKafka:         skipKafka,
		AuthType:          authType,
		SASLScramUsername: saslScramUsername,
		SASLScramPassword: saslScramPassword,
		TLSCACert:         tlsCaCert,
		TLSClientKey:      tlsClientKey,
		TLSClientCert:     tlsClientCert,
	}

	return &opts, nil
}
