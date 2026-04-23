package schema_registry

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/client"
	glue_service "github.com/confluentinc/kcp/internal/services/glue_schema_registry"
	"github.com/confluentinc/kcp/internal/services/schema_registry"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile          string
	srType             string
	url                string
	useUnauthenticated bool
	useBasicAuth       bool
	username           string
	password           string
	registryName       string
	region             string
)

const schemaRegistryIAMPermissions = "Only required for `--sr-type glue`. AWS Glue scans use the AWS default credential chain.\n\n" +
	"```json\n" +
	`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "glue:ListSchemas",
        "glue:ListSchemaVersions",
        "glue:GetSchema",
        "glue:GetSchemaByDefinition",
        "glue:GetSchemaVersion",
        "glue:GetRegistry"
      ],
      "Resource": [
        "arn:aws:glue:<AWS REGION>:<AWS ACCOUNT ID>:registry/<REGISTRY NAME>",
        "arn:aws:glue:<AWS REGION>:<AWS ACCOUNT ID>:schema/<REGISTRY NAME>/*"
      ]
    }
  ]
}` + "\n```\n"

func NewScanSchemaRegistryCmd() *cobra.Command {
	schemaRegistryCmd := &cobra.Command{
		Use:   "schema-registry",
		Short: "Scan a schema registry for schemas and versions",
		Long:  "Scan a schema registry (Confluent or AWS Glue) to discover all schemas and their versions. Use --sr-type to select the registry type. Results are added to the state file under schema_registries.",
		Example: `  # Confluent Schema Registry, unauthenticated
  kcp scan schema-registry --sr-type confluent --state-file kcp-state.json \
      --url https://my-schema-registry:8081 --use-unauthenticated

  # Confluent Schema Registry, basic auth
  kcp scan schema-registry --sr-type confluent --state-file kcp-state.json \
      --url https://my-schema-registry:8081 \
      --use-basic-auth --username my-user --password my-pass

  # AWS Glue Schema Registry
  kcp scan schema-registry --sr-type glue --state-file kcp-state.json \
      --region us-east-1 --registry-name my-glue-registry`,
		Annotations: map[string]string{
			"aws_iam_permissions": schemaRegistryIAMPermissions,
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanSchemaRegistry,
		RunE:          runScanSchemaRegistry,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file.")
	requiredFlags.StringVar(&srType, "sr-type", "", "Schema registry type: 'confluent' or 'glue'")
	schemaRegistryCmd.Flags().AddFlagSet(requiredFlags)

	confluentFlags := pflag.NewFlagSet("confluent", pflag.ExitOnError)
	confluentFlags.SortFlags = false
	confluentFlags.StringVar(&url, "url", "", "The URL of the schema registry to scan.")
	confluentFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use Unauthenticated Authentication")
	confluentFlags.BoolVar(&useBasicAuth, "use-basic-auth", false, "Use Basic Authentication")
	confluentFlags.StringVar(&username, "username", "", "The username to use for Basic Authentication")
	confluentFlags.StringVar(&password, "password", "", "The password to use for Basic Authentication")
	schemaRegistryCmd.Flags().AddFlagSet(confluentFlags)

	glueFlags := pflag.NewFlagSet("glue", pflag.ExitOnError)
	glueFlags.SortFlags = false
	glueFlags.StringVar(&registryName, "registry-name", "", "The name of the AWS Glue Schema Registry to scan.")
	glueFlags.StringVar(&region, "region", "", "The AWS region where the Glue Schema Registry is located.")
	schemaRegistryCmd.Flags().AddFlagSet(glueFlags)

	schemaRegistryCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, confluentFlags, glueFlags}
		groupNames := []string{
			"Required Flags",
			"Confluent Flags (--sr-type=confluent)",
			"Glue Flags (--sr-type=glue)",
		}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		fmt.Println("Glue authentication uses AWS default credentials (environment variables, shared credentials file, or IAM role).")

		return nil
	})

	_ = schemaRegistryCmd.MarkFlagRequired("state-file")
	_ = schemaRegistryCmd.MarkFlagRequired("sr-type")

	// --use-unauthenticated and --use-basic-auth cannot be set together.
	// "One-of" is enforced in preRunE since it only applies when --sr-type=confluent.
	schemaRegistryCmd.MarkFlagsMutuallyExclusive("use-unauthenticated", "use-basic-auth")

	return schemaRegistryCmd
}

func preRunScanSchemaRegistry(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	switch srType {
	case "confluent":
		if url == "" {
			return fmt.Errorf("--url is required when --sr-type=confluent")
		}
		if useUnauthenticated == useBasicAuth {
			return fmt.Errorf("exactly one of --use-unauthenticated or --use-basic-auth is required")
		}
		if useBasicAuth {
			if username == "" {
				return fmt.Errorf("--username is required when --use-basic-auth is set")
			}
			if password == "" {
				return fmt.Errorf("--password is required when --use-basic-auth is set")
			}
		}
	case "glue":
		if registryName == "" {
			return fmt.Errorf("--registry-name is required when --sr-type=glue")
		}
		if region == "" {
			return fmt.Errorf("--region is required when --sr-type=glue")
		}
	default:
		return fmt.Errorf("invalid --sr-type %q: must be 'confluent' or 'glue'", srType)
	}

	return nil
}

func runScanSchemaRegistry(cmd *cobra.Command, args []string) error {
	switch srType {
	case "confluent":
		return runScanConfluentSchemaRegistry()
	case "glue":
		return runScanGlueSchemaRegistry(cmd.Context())
	}
	return nil
}

func runScanConfluentSchemaRegistry() error {
	opts, err := parseConfluentOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan schema registry opts: %v", err)
	}

	authOption, err := getAuthOptionFromFlags()
	if err != nil {
		return fmt.Errorf("failed to get auth option: %v", err)
	}

	schemaRegistryClient, err := client.NewSchemaRegistryClient(opts.Url, authOption)
	if err != nil {
		return fmt.Errorf("failed to create schema registry client: %v", err)
	}

	schemaRegistryService := schema_registry.NewSchemaRegistryService(schemaRegistryClient)

	schemaRegistryScanner := NewSchemaRegistryScanner(schemaRegistryService, *opts)
	if err := schemaRegistryScanner.Run(); err != nil {
		return fmt.Errorf("failed to scan schema registry: %v", err)
	}

	fmt.Printf("✅ Successfully scanned schema registry\n")

	return nil
}

func runScanGlueSchemaRegistry(ctx context.Context) error {
	opts, err := parseGlueOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan glue schema registry opts: %v", err)
	}

	glueClient, err := client.NewGlueClient(ctx, opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS Glue client: %v", err)
	}

	glueService := glue_service.NewGlueSchemaRegistryService(glueClient)

	scanner := NewGlueSchemaRegistryScanner(glueService, *opts)
	if err := scanner.Run(ctx); err != nil {
		return fmt.Errorf("failed to scan Glue Schema Registry: %v", err)
	}

	return nil
}

func parseConfluentOpts() (*SchemaRegistryScannerOpts, error) {
	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}
	opts := SchemaRegistryScannerOpts{
		StateFile: stateFile,
		State:     *state,
		Url:       url,
	}

	return &opts, nil
}

func parseGlueOpts() (*GlueSchemaRegistryScannerOpts, error) {
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

func getAuthOptionFromFlags() (client.SchemaRegistryOption, error) {
	switch {
	case useUnauthenticated:
		return client.WithUnauthenticated(), nil
	case useBasicAuth:
		if username == "" || password == "" {
			return nil, fmt.Errorf("username and password are required for basic authentication")
		}
		return client.WithBasicAuth(username, password), nil

	default:
		return nil, fmt.Errorf("no authentication method specified")
	}
}
