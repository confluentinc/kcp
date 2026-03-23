package status

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	restEndpoint     string
	clusterID        string
	linkName         string
	apiKey           string
	apiSecret        string
	pollInterval     int
	sourceClusterArn string
	ccBootstrap      string

	useSaslIam                  bool
	useSaslScram                bool
	useTls                      bool
	useUnauthenticatedTLS       bool
	useUnauthenticatedPlaintext bool

	saslScramUsername string
	saslScramPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string
)

func NewMigrationStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show migration lag comparing source and destination offsets",
		Long:  "Interactive TUI that compares source (MSK) and destination (CC) Kafka offsets to show real-time migration lag per topic and partition. Requires credentials for both source and destination clusters.",
		Example: `  kcp migration status --rest-endpoint https://... --cluster-id lkc-xxx --cluster-link-name my-link --cluster-api-key xxx --cluster-api-secret xxx --source-cluster-arn arn:aws:kafka:... --cc-bootstrap pkc-xxx:9092 --use-sasl-iam
  All flags can be provided via environment variables (uppercase, with underscores).`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationStatus,
		RunE:          runMigrationStatus,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&restEndpoint, "rest-endpoint", "", "Cluster link REST endpoint")
	requiredFlags.StringVar(&clusterID, "cluster-id", "", "Cluster link cluster ID")
	requiredFlags.StringVar(&linkName, "cluster-link-name", "", "Cluster link name")
	requiredFlags.StringVar(&apiKey, "cluster-api-key", "", "Cluster link API key")
	requiredFlags.StringVar(&apiSecret, "cluster-api-secret", "", "Cluster link API secret")
	requiredFlags.StringVar(&sourceClusterArn, "source-cluster-arn", "", "ARN of the source MSK cluster")
	requiredFlags.StringVar(&ccBootstrap, "cc-bootstrap", "", "Confluent Cloud Kafka bootstrap endpoint")
	cmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Authentication flags.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS authentication for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedTLS, "use-unauthenticated-tls", false, "Use unauthenticated (TLS encryption) for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedPlaintext, "use-unauthenticated-plaintext", false, "Use unauthenticated (plaintext) for the source MSK cluster.")
	cmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Source Cluster Authentication Flags"

	// SASL/SCRAM credential flags.
	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username for the source MSK cluster.")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password for the source MSK cluster.")
	cmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Flags"

	// TLS credential flags.
	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to the TLS CA certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to the TLS client certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to the TLS client key for the source MSK cluster.")
	cmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.IntVar(&pollInterval, "poll-interval", 1, "Poll interval in seconds (1-60)")
	cmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, authFlags, saslScramFlags, tlsFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Source Cluster Authentication Flags", "SASL/SCRAM Flags", "TLS Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	cmd.MarkFlagRequired("cluster-id")
	cmd.MarkFlagRequired("rest-endpoint")
	cmd.MarkFlagRequired("cluster-link-name")
	cmd.MarkFlagRequired("cluster-api-key")
	cmd.MarkFlagRequired("cluster-api-secret")
	cmd.MarkFlagRequired("source-cluster-arn")
	cmd.MarkFlagRequired("cc-bootstrap")

	cmd.MarkFlagsMutuallyExclusive("use-sasl-iam", "use-sasl-scram", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")

	return cmd
}

func preRunMigrationStatus(cmd *cobra.Command, args []string) error {
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

func resolveAuthType() types.AuthType {
	switch {
	case useSaslIam:
		return types.AuthTypeIAM
	case useSaslScram:
		return types.AuthTypeSASLSCRAM
	case useTls:
		return types.AuthTypeTLS
	case useUnauthenticatedTLS:
		return types.AuthTypeUnauthenticatedTLS
	case useUnauthenticatedPlaintext:
		return types.AuthTypeUnauthenticatedPlaintext
	default:
		return types.AuthTypeIAM
	}
}

func runMigrationStatus(cmd *cobra.Command, args []string) error {
	interval := pollInterval
	if interval < 1 {
		interval = 1
	}
	if interval > 60 {
		interval = 60
	}

	ctx := cmd.Context()

	sourceOffset, region, err := createSourceOffset(ctx)
	if err != nil {
		return err
	}
	defer sourceOffset.Close()

	destinationOffset, err := createDestinationOffset(ccBootstrap, apiKey, apiSecret)
	if err != nil {
		return err
	}
	defer destinationOffset.Close()

	clConfig := clusterlink.Config{
		RestEndpoint: restEndpoint,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       apiKey,
		APISecret:    apiSecret,
		Topics:       []string{},
	}

	clSvc := clusterlink.NewConfluentCloudService(http.DefaultClient)
	m := newModel(sourceOffset, destinationOffset, clSvc, clConfig, region, interval)
	p := newProgram(m)
	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("TUI: %w", err)
	}
	return nil
}

func createSourceOffset(ctx context.Context) (*offset.Service, string, error) {
	authType := resolveAuthType()

	region, err := utils.ExtractRegionFromArn(sourceClusterArn)
	if err != nil {
		return nil, "", err
	}

	slog.Debug("discovering MSK bootstrap brokers")
	mskAwsClient, err := client.NewMSKClient(region, 8, 1)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create MSK API client: %w", err)
	}

	mskService := msk.NewMSKService(mskAwsClient)
	bootstrapOutput, err := mskService.GetBootstrapBrokers(ctx, sourceClusterArn)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get bootstrap brokers: %w", err)
	}

	awsInfo := types.AWSClientInformation{BootstrapBrokers: *bootstrapOutput}
	brokerAddresses, err := awsInfo.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return nil, "", err
	}

	// Build ClusterAuth from flag values
	clusterAuth := types.ClusterAuth{}
	switch authType {
	case types.AuthTypeSASLSCRAM:
		clusterAuth.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:      true,
			Username: saslScramUsername,
			Password: saslScramPassword,
		}
	case types.AuthTypeTLS:
		clusterAuth.AuthMethod.TLS = &types.TLSConfig{
			Use:        true,
			CACert:     tlsCaCert,
			ClientCert: tlsClientCert,
			ClientKey:  tlsClientKey,
		}
	case types.AuthTypeIAM:
		clusterAuth.AuthMethod.IAM = &types.IAMConfig{Use: true}
	case types.AuthTypeUnauthenticatedTLS:
		clusterAuth.AuthMethod.UnauthenticatedTLS = &types.UnauthenticatedTLSConfig{Use: true}
	case types.AuthTypeUnauthenticatedPlaintext:
		clusterAuth.AuthMethod.UnauthenticatedPlaintext = &types.UnauthenticatedPlaintextConfig{Use: true}
	}

	slog.Debug("connecting to source cluster (MSK)")
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, client.AdminOptionForAuth(authType, clusterAuth))
	if err != nil {
		return nil, "", fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	slog.Debug("source cluster connected")

	return offset.NewOffsetService(sourceClient), region, nil
}

func createDestinationOffset(bootstrap, key, secret string) (*offset.Service, error) {
	slog.Debug("connecting to destination cluster (Confluent Cloud)")
	brokers := strings.Split(bootstrap, ",")
	destClient, err := client.NewKafkaClient(brokers, "", client.WithSASLPlainAuth(key, secret))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	slog.Debug("destination cluster connected")

	return offset.NewOffsetService(destClient), nil
}
