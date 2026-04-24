package migrate_topics

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile                 string
	clusterId                 string
	sourceType                string
	targetClusterId           string
	targetClusterRestEndpoint string
	clusterLinkName           string
	outputDir                 string
)

func NewMigrateTopicsCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:   "migrate-topics",
		Short: "Create assets for the migrate topics",
		Long:  "Create Terraform files for setting up mirror topics used by the cluster links to migrate data to the target cluster in Confluent Cloud",
		Example: `  kcp create-asset migrate-topics \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --target-cluster-id lkc-xyz123 \
      --target-rest-endpoint https://lkc-xyz123.eu-west-3.aws.private.confluent.cloud:443 \
      --cluster-link-name msk-to-cc-link`,
		SilenceErrors: true,
		PreRunE:       preRunMigrateTopics,
		RunE:          runMigrateTopics,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the cluster discovery reports have been written to.")
	requiredFlags.StringVar(&sourceType, "source-type", "msk", "Source type: 'msk' or 'osk'")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "The cluster identifier (ARN for MSK, cluster ID from credentials file for OSK).")
	requiredFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).")
	requiredFlags.StringVar(&targetClusterRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link that was created as part of the migration (e.g., msk-to-cc-migration-link).")
	migrationCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "migrate_topics", "The directory to output the Terraform files to. (default: 'migrate_topics')")
	migrationCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	migrationCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = migrationCmd.MarkFlagRequired("state-file")
	_ = migrationCmd.MarkFlagRequired("cluster-id")
	_ = migrationCmd.MarkFlagRequired("target-cluster-id")
	_ = migrationCmd.MarkFlagRequired("target-rest-endpoint")
	_ = migrationCmd.MarkFlagRequired("cluster-link-name")

	return migrationCmd
}

func preRunMigrateTopics(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrateTopics(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrateTopicsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migrate topics opts: %v", err)
	}

	migrateTopicsAssetGenerator := NewMigrateTopicsAssetGenerator(*opts)
	if err := migrateTopicsAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create migration assets: %v", err)
	}

	return nil
}

func parseMigrateTopicsOpts() (*MigrateTopicsOpts, error) {
	internalTopicsToInclude := []string{"__consumer_offsets"}

	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster file: %v", err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	var kafkaAdminInfo *types.KafkaAdminClientInformation

	switch sourceType {
	case "msk":
		cluster, err := state.GetClusterByArn(clusterId)
		if err != nil {
			return nil, fmt.Errorf("failed to get cluster: %w", err)
		}
		kafkaAdminInfo = &cluster.KafkaAdminClientInformation
	case "osk":
		cluster, err := state.GetOSKClusterByID(clusterId)
		if err != nil {
			return nil, fmt.Errorf("failed to get OSK cluster: %w", err)
		}
		kafkaAdminInfo = &cluster.KafkaAdminClientInformation
	default:
		return nil, fmt.Errorf("invalid --source-type: %s (must be 'msk' or 'osk')", sourceType)
	}

	var mirrorTopics []string
	if kafkaAdminInfo.Topics != nil {
		for _, topic := range kafkaAdminInfo.Topics.Details {
			if !strings.HasPrefix(topic.Name, "__") || slices.Contains(internalTopicsToInclude, topic.Name) {
				mirrorTopics = append(mirrorTopics, topic.Name)
			}
		}
	}

	opts := MigrateTopicsOpts{
		MirrorTopics:              mirrorTopics,
		TargetClusterId:           targetClusterId,
		TargetClusterRestEndpoint: targetClusterRestEndpoint,
		ClusterLinkName:           clusterLinkName,
		OutputDir:                 outputDir,
	}

	return &opts, nil
}
