package migration_scripts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp-internal/internal/generators/create_asset/migration_scripts"
	"github.com/confluentinc/kcp-internal/internal/types"
	"github.com/confluentinc/kcp-internal/internal/utils"
	"github.com/spf13/cobra"
)

var (
	clusterFile          string
	migrationInfraFolder string
)

func NewMigrationCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:   "migration-scripts",
		Short: "Create assets for the migration scripts",
		Long: `Create assets for the migration scripts

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|---------------------------
--cluster-file           | CLUSTER_FILE=path/to/cluster.json
--migration-infra-folder | MIGRATION_INFRA_FOLDER=path/to/migration_infra
`,
		SilenceErrors: true,
		PreRunE:       preRunCreateMigrationScripts,
		RunE:          runCreateMigrationScripts,
	}

	migrationCmd.Flags().StringVar(&clusterFile, "cluster-file", "", "The cluster json file produced from 'scan cluster' command")
	migrationCmd.Flags().StringVar(&migrationInfraFolder, "migration-infra-folder", "", "The migration infra folder produced from 'create-asset migration-infra' command after applying the terraform")

	migrationCmd.MarkFlagRequired("cluster-file")
	migrationCmd.MarkFlagRequired("migration-infra-folder")

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
	// Parse cluster information from JSON file
	file, err := os.ReadFile(clusterFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster file: %v", err)
	}

	var clusterInfo types.ClusterInformation
	if err := json.Unmarshal(file, &clusterInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster info: %v", err)
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
		ClusterInformation: clusterInfo,
		TerraformOutput:    terraformState.Outputs,
		Manifest:           manifest,
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
