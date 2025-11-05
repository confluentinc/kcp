package migrate_topics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile            string
	migrationInfraFolder string
	clusterArn           string
)

func NewMigrateTopicsCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:           "migrate-topics",
		Short:         "Create assets for the migrate topics",
		Long:          "Create shell scripts for setting up mirror topics used by the cluster links to migrate data to the target cluster in Confluent Cloud",
		SilenceErrors: true,
		PreRunE:       preRunMigrateTopics,
		RunE:          runMigrateTopics,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&migrationInfraFolder, "migration-infra-folder", "", "The migration-infra folder produced from 'kcp create-asset migration-infra' command after applying the Terraform")
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to create migration scripts for.")
	migrationCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	migrationCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

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
	migrationCmd.MarkFlagRequired("migration-infra-folder")
	migrationCmd.MarkFlagRequired("cluster-arn")

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

	manifestPath := filepath.Join(migrationInfraFolder, "manifest.json")
	manifestFile, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}

	var manifest types.Manifest
	if err := json.Unmarshal(manifestFile, &manifest); err != nil {
		return nil, err
	}

	if !manifest.MigrationInfraType.IsValid() {
		return nil, fmt.Errorf("invalid migration infra type in manifest: %d", manifest.MigrationInfraType)
	}

	requiredTFStateFields := getRequiredTFStateFields(manifest)
	terraformState, err := utils.ParseTerraformState(migrationInfraFolder, requiredTFStateFields)
	if err != nil {
		return nil, fmt.Errorf("error: %v\n please run terraform apply in the migration infra folder", err)
	}

	opts := MigrateTopicsOpts{
		MirrorTopics:    mirrorTopics,
		TerraformOutput: terraformState.Outputs,
		Manifest:        manifest,
	}

	return &opts, nil
}

func getRequiredTFStateFields(manifest types.Manifest) []string {
	requiredFields := []string{
		"confluent_cloud_cluster_api_key",
		"confluent_cloud_cluster_api_key_secret",
		"confluent_cloud_cluster_rest_endpoint",
		"confluent_cloud_cluster_bootstrap_endpoint",
		"confluent_cloud_cluster_id",
	}

	if manifest.MigrationInfraType == types.MskCpCcPrivateSaslIam || manifest.MigrationInfraType == types.MskCpCcPrivateSaslScram {
		requiredFields = append(requiredFields, "confluent_platform_controller_bootstrap_server")
	}

	return requiredFields
}
