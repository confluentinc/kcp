package migration_scripts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migration_scripts"
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

func NewMigrationCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:           "migration-scripts",
		Short:         "Create assets for the migration scripts",
		Long:          "Create shell scripts for setting up mirror topics used by the cluster links to migrate data to the target cluster in Confluent Cloud",
		SilenceErrors: true,
		PreRunE:       preRunCreateMigrationScripts,
		RunE:          runCreateMigrationScripts,
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

func preRunCreateMigrationScripts(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runCreateMigrationScripts(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationScriptsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration scripts opts: %v", err)
	}

	migrationAssetGenerator := migration_scripts.NewMigrationAssetGenerator(*opts)
	if err := migrationAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create migration assets: %v", err)
	}

	return nil
}

func parseMigrationScriptsOpts() (*migration_scripts.MigrationScriptsOpts, error) {
	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster file: %v", err)
	}

	var discoveryData types.Discovery
	if err := json.Unmarshal(file, &discoveryData); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	cluster, err := utils.GetClusterByArn(&discoveryData, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	var mirrorTopics []string
	for _, topic := range cluster.KafkaAdminClientInformation.Topics.Details {
		if !strings.HasPrefix(topic.Name, "__") {
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

	opts := migration_scripts.MigrationScriptsOpts{
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
