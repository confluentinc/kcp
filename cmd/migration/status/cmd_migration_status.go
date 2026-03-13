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
	credentialsFile  string
	sourceClusterArn string
	ccBootstrap      string
)

func NewMigrationStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show migration lag comparing source and destination offsets",
		Long:  "Interactive TUI that compares source (MSK) and destination (CC) Kafka offsets to show real-time migration lag per topic and partition. Requires credentials for both source and destination clusters.",
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

	cmd.MarkFlagRequired("cluster-id")
	cmd.MarkFlagRequired("rest-endpoint")
	cmd.MarkFlagRequired("cluster-link-name")
	cmd.MarkFlagRequired("cluster-api-key")
	cmd.MarkFlagRequired("cluster-api-secret")
	cmd.MarkFlagRequired("credentials-file")
	cmd.MarkFlagRequired("source-cluster-arn")
	cmd.MarkFlagRequired("cc-bootstrap")

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

	ctx := cmd.Context()

	srcOffset, region, err := createSourceOffset(ctx, credentialsFile, sourceClusterArn)
	if err != nil {
		return err
	}

	dstOffset, err := createDestOffset(ccBootstrap, apiKey, apiSecret)
	if err != nil {
		return err
	}

	clConfig := clusterlink.Config{
		RestEndpoint: restEndpoint,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       apiKey,
		APISecret:    apiSecret,
		Topics:       []string{},
	}

	clSvc := clusterlink.NewConfluentCloudService(http.DefaultClient)
	m := newModel(srcOffset, dstOffset, clSvc, clConfig, region, interval)
	p := newProgram(m)
	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("TUI: %w", err)
	}
	return nil
}

func createSourceOffset(ctx context.Context, credFile, clusterArn string) (*offset.TopicOffset, string, error) {
	credentials, errs := types.NewCredentialsFromFile(credFile)
	if len(errs) > 0 {
		return nil, "", fmt.Errorf("failed to parse credentials file: %v", errs[0])
	}

	clusterAuth, err := credentials.FindClusterByArn(clusterArn)
	if err != nil {
		return nil, "", err
	}

	authType, err := clusterAuth.GetSelectedAuthType()
	if err != nil {
		return nil, "", fmt.Errorf("failed to determine auth type: %w", err)
	}

	region, err := utils.ExtractRegionFromArn(clusterArn)
	if err != nil {
		return nil, "", err
	}

	slog.Debug("discovering MSK bootstrap brokers")
	mskAwsClient, err := client.NewMSKClient(region, 8, 1)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create MSK API client: %w", err)
	}

	mskService := msk.NewMSKService(mskAwsClient)
	bootstrapOutput, err := mskService.GetBootstrapBrokers(ctx, clusterArn)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get bootstrap brokers: %w", err)
	}

	awsInfo := types.AWSClientInformation{BootstrapBrokers: *bootstrapOutput}
	brokerAddresses, err := awsInfo.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return nil, "", err
	}

	slog.Debug("connecting to source cluster (MSK)")
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, client.AdminOptionForAuth(authType, *clusterAuth))
	if err != nil {
		return nil, "", fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	slog.Debug("source cluster connected")

	return offset.NewTopicOffset(sourceClient), region, nil
}

func createDestOffset(bootstrap, key, secret string) (*offset.TopicOffset, error) {
	slog.Debug("connecting to destination cluster (Confluent Cloud)")
	brokers := strings.Split(bootstrap, ",")
	destClient, err := client.NewKafkaClient(brokers, "", client.WithSASLPlainAuth(key, secret))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	slog.Debug("destination cluster connected")

	return offset.NewTopicOffset(destClient), nil
}

