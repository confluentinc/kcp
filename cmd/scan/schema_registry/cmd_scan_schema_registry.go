package schema_registry

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/schema_registry"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile          string
	url                string
	useUnauthenticated bool
	useBasicAuth       bool
	username           string
	password           string
)

func NewScanSchemaRegistryCmd() *cobra.Command {
	schemaRegistryCmd := &cobra.Command{
		Use:           "schema-registry",
		Short:         "Scan schema registry for information",
		Long:          "Scan schema registry for information including all subjects, their versions, and latest schema metadata.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanSchemaRegistry,
		RunE:          runScanSchemaRegistry,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&url, "url", "", "The URL of the schema registry to scan.")
	schemaRegistryCmd.Flags().AddFlagSet(requiredFlags)

	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use Unauthenticated Authentication")
	authFlags.BoolVar(&useBasicAuth, "use-basic-auth", false, "Use Basic Authentication")
	authFlags.StringVar(&username, "username", "", "The username to use for Basic Authentication")
	authFlags.StringVar(&password, "password", "", "The password to use for Basic Authentication")
	schemaRegistryCmd.Flags().AddFlagSet(authFlags)

	schemaRegistryCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, authFlags}
		groupNames := []string{"Required Flags", "Authentication Flags (provide one of the following)"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	schemaRegistryCmd.MarkFlagRequired("state-file")
	schemaRegistryCmd.MarkFlagRequired("url")

	schemaRegistryCmd.MarkFlagsMutuallyExclusive("use-unauthenticated", "use-basic-auth")
	schemaRegistryCmd.MarkFlagsOneRequired("use-unauthenticated", "use-basic-auth")
	schemaRegistryCmd.MarkFlagsRequiredTogether("use-basic-auth", "username", "password")

	return schemaRegistryCmd
}

func preRunScanSchemaRegistry(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanSchemaRegistry(cmd *cobra.Command, args []string) error {
	opts, err := parseScanSchemaRegistryOpts()
	if err != nil {
		return fmt.Errorf("❌ failed to parse scan schema registry opts: %v", err)
	}

	authOption, err := getAuthOptionFromFlags()
	if err != nil {
		return fmt.Errorf("❌ failed to get auth option: %v", err)
	}

	schemaRegistryClient, err := client.NewSchemaRegistryClient(opts.Url, authOption)
	if err != nil {
		return fmt.Errorf("❌ failed to create schema registry client: %v", err)
	}

	schemaRegistryService := schema_registry.NewSchemaRegistryService(schemaRegistryClient)

	schemaRegistryScanner := NewSchemaRegistryScanner(schemaRegistryService, *opts)
	if err := schemaRegistryScanner.Run(); err != nil {
		return fmt.Errorf("❌ failed to scan schema registry: %v", err)
	}

	slog.Info("✅ successfully scanned schema registry")

	return nil
}

func parseScanSchemaRegistryOpts() (*SchemaRegistryScannerOpts, error) {
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
