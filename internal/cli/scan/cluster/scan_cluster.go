package cluster

import (
	"fmt"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/generators/scan/cluster"
	"github.com/confluentinc/kcp/internal/services/ec2"
	"github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	clusterArn string

	useSaslIam         bool
	useSaslScram       bool
	useTls             bool
	useUnauthenticated bool
	skipKafka          bool

	saslScramUsername string
	saslScramPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string
)

func NewScanClusterCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:           "cluster",
		Short:         "Scan a single MSK cluster",
		Long:          "Scan a single MSK cluster for information that will help with migration at both the AWS and Kafka level",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanCluster,
		RunE:          runScanCluster,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The MSK cluster ARN")
	clusterCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Authentication flags.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS authentication")
	authFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use unauthenticated authentication")
	authFlags.BoolVar(&skipKafka, "skip-kafka", false, "Skip kafka level cluster scan, use when brokers are not reachable")
	clusterCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Authentication Flags"

	// SASL/SCRAM flags.
	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "The SASL SCRAM username")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "The SASL SCRAM password")
	clusterCmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Flags"

	// TLS flags.
	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "The TLS CA certificate")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "The TLS client certificate")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "The TLS client key")
	clusterCmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Flags"

	clusterCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, authFlags, saslScramFlags, tlsFlags}
		groupNames := []string{"Required Flags", "Authentication Flags", "SASL/SCRAM Flags", "TLS Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

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

	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		switch opts.AuthType {
		case types.AuthTypeIAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithIAMAuth())
		case types.AuthTypeSASLSCRAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithSASLSCRAMAuth(opts.SASLScramUsername, opts.SASLScramPassword))
		case types.AuthTypeUnauthenticated:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithUnauthenticatedAuth())
		case types.AuthTypeTLS:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithTLSAuth(opts.TLSCACert, opts.TLSClientCert, opts.TLSClientKey))
		default:
			return nil, fmt.Errorf("❌ Auth type: %v not yet supported", opts.AuthType)
		}
	}

	mskService := msk.NewMSKService(mskClient)

	ec2Service, err := ec2.NewEC2Service(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create ec2 service: %v", err)
	}

	kafkaService := kafka.NewKafkaService(kafka.KafkaServiceOpts{
		KafkaAdminFactory: kafkaAdminFactory,
		AuthType:          opts.AuthType,
		ClusterArn:        opts.ClusterArn,
	})

	// Scan the cluster
	clusterScanner := cluster.NewClusterScanner(mskService, ec2Service, kafkaService, *opts)
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
