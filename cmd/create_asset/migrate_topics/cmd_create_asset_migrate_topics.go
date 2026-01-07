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
	clusterArn                string
	targetClusterId           string
	targetClusterRestEndpoint string
	clusterLinkName           string
	outputDir                 string
)

func NewMigrateTopicsCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:           "migrate-topics",
		Short:         "Create assets for the migrate topics",
		Long:          "Create Terraform files for setting up mirror topics used by the cluster links to migrate data to the target cluster in Confluent Cloud",
		SilenceErrors: true,
		PreRunE:       preRunMigrateTopics,
		RunE:          runMigrateTopics,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to create migration scripts for.")
	requiredFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).")
	requiredFlags.StringVar(&targetClusterRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link (e.g., msk-to-cc-migration-link).")
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

	migrationCmd.MarkFlagRequired("state-file")
	migrationCmd.MarkFlagRequired("cluster-arn")
	migrationCmd.MarkFlagRequired("target-cluster-id")
	migrationCmd.MarkFlagRequired("target-cluster-rest-endpoint")
	migrationCmd.MarkFlagRequired("target-cluster-link-name")

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

	cluster, err := utils.GetClusterByArn(&state, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	var mirrorTopics []string
	for _, topic := range cluster.KafkaAdminClientInformation.Topics.Details {
		if !strings.HasPrefix(topic.Name, "__") || slices.Contains(internalTopicsToInclude, topic.Name) {
			mirrorTopics = append(mirrorTopics, topic.Name)
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
