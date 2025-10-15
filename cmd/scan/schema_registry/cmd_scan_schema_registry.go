package schema_registry

import (
	"fmt"

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
)

func NewScanSchemaRegistryCmd() *cobra.Command {
	schemaRegistryCmd := &cobra.Command{
		Use:           "schema-registry",
		Short:         "Scan schema registry for information",
		Long:          "Scan schema registry for information",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanSchemaRegistry,
		RunE:          runScanSchemaRegistry,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&url, "url", "", "The URL of the schema registry to scan.")
	schemaRegistryCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use Unauthenticated Authentication")
	schemaRegistryCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Authentication Flags"

	schemaRegistryCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, authFlags}
		groupNames := []string{"Required Flags", "Authentication Flags"}

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

	// will have more auth flags later
	schemaRegistryCmd.MarkFlagsMutuallyExclusive("use-unauthenticated")
	schemaRegistryCmd.MarkFlagsOneRequired("use-unauthenticated")

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

	schemaRegistryClient, err := client.NewSchemaRegistryClient(opts.Url, client.WithUnauthenticated())
	if err != nil {
		return fmt.Errorf("❌ failed to create schema registry client: %v", err)
	}

	schemaRegistryService := schema_registry.NewSchemaRegistryService(schemaRegistryClient)

	schemaRegistryScanner := NewSchemaRegistryScanner(schemaRegistryService, *opts)
	if err := schemaRegistryScanner.Run(); err != nil {
		return fmt.Errorf("❌ failed to scan schema registry: %v", err)
	}

	return nil
}

func parseScanSchemaRegistryOpts() (*SchemaRegistryScannerOpts, error) {
	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}
	opts := SchemaRegistryScannerOpts{
		State: *state,
		Url:   url,
	}

	return &opts, nil
}
