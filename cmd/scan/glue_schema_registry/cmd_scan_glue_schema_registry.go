package glue_schema_registry

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/glue_schema_registry"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile    string
	region       string
	registryName string
)

func NewScanGlueSchemaRegistryCmd() *cobra.Command {
	glueSchemaRegistryCmd := &cobra.Command{
		Use:           "glue-schema-registry",
		Short:         "Scan an AWS Glue Schema Registry for schemas and versions",
		Long:          "Scan an AWS Glue Schema Registry to discover all schemas and their versions. Results are added to the state file under the schema_registries.aws_glue section.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanGlueSchemaRegistry,
		RunE:          runScanGlueSchemaRegistry,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file.")
	requiredFlags.StringVar(&region, "region", "", "The AWS region where the Glue Schema Registry is located.")
	requiredFlags.StringVar(&registryName, "registry-name", "", "The name of the AWS Glue Schema Registry to scan.")
	glueSchemaRegistryCmd.Flags().AddFlagSet(requiredFlags)

	glueSchemaRegistryCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		usage := requiredFlags.FlagUsages()
		if usage != "" {
			fmt.Printf("Required Flags:\n%s\n", usage)
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		fmt.Println("Authentication uses AWS default credentials (environment variables, shared credentials file, or IAM role).")

		return nil
	})

	_ = glueSchemaRegistryCmd.MarkFlagRequired("state-file")
	_ = glueSchemaRegistryCmd.MarkFlagRequired("region")
	_ = glueSchemaRegistryCmd.MarkFlagRequired("registry-name")

	return glueSchemaRegistryCmd
}

func preRunScanGlueSchemaRegistry(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanGlueSchemaRegistry(cmd *cobra.Command, args []string) error {
	opts, err := parseScanGlueSchemaRegistryOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan glue schema registry opts: %v", err)
	}

	glueClient, err := client.NewGlueClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS Glue client: %v", err)
	}

	glueService := glue_schema_registry.NewGlueSchemaRegistryService(glueClient)

	scanner := NewGlueSchemaRegistryScanner(glueService, *opts)
	if err := scanner.Run(); err != nil {
		return fmt.Errorf("failed to scan Glue Schema Registry: %v", err)
	}

	slog.Info("successfully scanned Glue Schema Registry")

	return nil
}

func parseScanGlueSchemaRegistryOpts() (*GlueSchemaRegistryScannerOpts, error) {
	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	opts := GlueSchemaRegistryScannerOpts{
		StateFile:    stateFile,
		State:        *state,
		Region:       region,
		RegistryName: registryName,
	}

	return &opts, nil
}
