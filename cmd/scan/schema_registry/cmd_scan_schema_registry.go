package schema_registry

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile string
)

func NewScanSchemaRegistryCmd() *cobra.Command {
	schemaRegistryCmd := &cobra.Command{
		Use:           "schema-registry",
		Short:         "Scan schema registry for information",
		Long:          "Scan schema registry for information",
		SilenceErrors: true,
		PreRunE:       preRunScanSchemaRegistry,
		RunE:          runScanSchemaRegistry,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	schemaRegistryCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	schemaRegistryCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	schemaRegistryCmd.MarkFlagRequired("state-file")

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
		return fmt.Errorf("failed to parse scan schema registry opts: %v", err)
	}

	_ = opts
	return nil
}

func parseScanSchemaRegistryOpts() (*SchemaRegistryScannerOpts, error) {
	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}
	opts := SchemaRegistryScannerOpts{
		State: *state,
	}

	return &opts, nil
}
