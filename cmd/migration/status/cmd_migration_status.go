package status

import (
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/msk"
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
	credentialsFile  string
	sourceClusterArn string
	ccBootstrap      string
)

func NewMigrationStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show migration lag comparing source and destination offsets",
		Long:  "Interactive TUI that compares source (MSK) and destination (CC) Kafka offsets alongside cluster link mirror topic lag. Requires credentials for both source and destination clusters.",
		Example: `  kcp migration status --rest-endpoint https://... --cluster-id lkc-xxx --cluster-link-name my-link --cluster-api-key xxx --cluster-api-secret xxx
  All flags can be provided via environment variables (uppercase, with underscores).`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationStatus,
		RunE:          runMigrationStatus,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&restEndpoint, "rest-endpoint", "", "Cluster link REST endpoint")
	requiredFlags.StringVar(&clusterID, "cluster-id", "", "Cluster link cluster ID")
	requiredFlags.StringVar(&linkName, "cluster-link-name", "", "Cluster link name")
	requiredFlags.StringVar(&apiKey, "cluster-api-key", "", "Cluster link API key")
	requiredFlags.StringVar(&apiSecret, "cluster-api-secret", "", "Cluster link API secret")
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Credentials YAML file for MSK cluster authentication")
	requiredFlags.StringVar(&sourceClusterArn, "source-cluster-arn", "", "ARN of the source MSK cluster")
	requiredFlags.StringVar(&ccBootstrap, "cc-bootstrap", "", "Confluent Cloud Kafka bootstrap endpoint")
	cmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.IntVar(&pollInterval, "poll-interval", 1, "Poll interval in seconds (1-60)")
	cmd.Flags().AddFlagSet(optionalFlags)

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		fmt.Printf("Required:\n%s\n", requiredFlags.FlagUsages())
		fmt.Printf("Optional:\n%s\n", optionalFlags.FlagUsages())
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	_ = cmd.MarkFlagRequired("rest-endpoint")
	_ = cmd.MarkFlagRequired("cluster-id")
	_ = cmd.MarkFlagRequired("cluster-link-name")
	_ = cmd.MarkFlagRequired("cluster-api-key")
	_ = cmd.MarkFlagRequired("cluster-api-secret")
	_ = cmd.MarkFlagRequired("credentials-file")
	_ = cmd.MarkFlagRequired("source-cluster-arn")
	_ = cmd.MarkFlagRequired("cc-bootstrap")

	return cmd
}

func preRunMigrationStatus(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runMigrationStatus(cmd *cobra.Command, args []string) error {
	interval := pollInterval
	if interval < 1 {
		interval = 1
	}
	if interval > 60 {
		interval = 60
	}

	// Parse credentials and find the source cluster
	credentials, errs := types.NewCredentialsFromFile(credentialsFile)
	if len(errs) > 0 {
		return fmt.Errorf("failed to parse credentials file: %v", errs[0])
	}

	clusterAuth, err := credentials.FindClusterByArn(sourceClusterArn)
	if err != nil {
		return err
	}

	authType, err := clusterAuth.GetSelectedAuthType()
	if err != nil {
		return fmt.Errorf("failed to determine auth type: %w", err)
	}

	// Extract region from ARN
	region, err := utils.ExtractRegionFromArn(sourceClusterArn)
	if err != nil {
		return err
	}

	// Get MSK bootstrap brokers via AWS API
	fmt.Printf("Discovering MSK bootstrap brokers...\n")
	mskAwsClient, err := client.NewMSKClient(region, 8, 1)
	if err != nil {
		return fmt.Errorf("failed to create MSK API client: %w", err)
	}

	mskService := msk.NewMSKService(mskAwsClient)
	ctx := cmd.Context()
	bootstrapOutput, err := mskService.GetBootstrapBrokers(ctx, sourceClusterArn)
	if err != nil {
		return fmt.Errorf("failed to get bootstrap brokers: %w", err)
	}

	awsInfo := types.AWSClientInformation{BootstrapBrokers: *bootstrapOutput}
	brokerAddresses, err := awsInfo.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return err
	}

	// Create source Kafka client (MSK)
	fmt.Println("Connecting to source cluster (MSK)...")
	sourceOpt := createAdminOption(authType, *clusterAuth)
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, sourceOpt)
	if err != nil {
		return fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	defer sourceClient.Close()
	fmt.Println("Source cluster connected.")

	// Create destination Kafka client (CC) via SASL/PLAIN
	fmt.Println("Connecting to destination cluster (Confluent Cloud)...")
	ccBrokers := strings.Split(ccBootstrap, ",")
	destClient, err := client.NewKafkaClient(ccBrokers, "", client.WithSASLPlainAuth(apiKey, apiSecret))
	if err != nil {
		return fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	defer destClient.Close()
	fmt.Println("Destination cluster connected.")

	// Cluster link config (existing)
	clConfig := clusterlink.Config{
		RestEndpoint: restEndpoint,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       apiKey,
		APISecret:    apiSecret,
		Topics:       []string{},
	}

	clSvc := clusterlink.NewConfluentCloudService(nil)
	m := newModel(sourceClient, destClient, clSvc, clConfig, region, interval)
	p := newProgram(m)
	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("TUI: %w", err)
	}
	return nil
}

// createAdminOption maps the auth type from credentials to the client option.
func createAdminOption(authType types.AuthType, clusterAuth types.ClusterAuth) client.AdminOption {
	switch authType {
	case types.AuthTypeIAM:
		return client.WithIAMAuth()
	case types.AuthTypeSASLSCRAM:
		return client.WithSASLSCRAMAuth(clusterAuth.AuthMethod.SASLScram.Username, clusterAuth.AuthMethod.SASLScram.Password)
	case types.AuthTypeUnauthenticatedTLS:
		return client.WithUnauthenticatedTlsAuth()
	case types.AuthTypeUnauthenticatedPlaintext:
		return client.WithUnauthenticatedPlaintextAuth()
	case types.AuthTypeTLS:
		return client.WithTLSAuth(clusterAuth.AuthMethod.TLS.CACert, clusterAuth.AuthMethod.TLS.ClientCert, clusterAuth.AuthMethod.TLS.ClientKey)
	default:
		return client.WithIAMAuth()
	}
}
