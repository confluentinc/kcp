package discover_v2

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/generators/discover_v2"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	regions []string
)

func NewDiscoverV2Cmd() *cobra.Command {
	discoverCmd := &cobra.Command{
		Use:           "discover-v2",
		Short:         "Multi-region, multi cluster discovery scan of AWS MSK",
		Long:          "Performs a full Discovery of all MSK clusters across multiple regions, and their associated resources, costs and metrics",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunDiscoverV2,
		RunE:          runDiscoverV2,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false

	requiredFlags.StringSliceVar(&regions, "region", []string{}, "The AWS region(s) to scan (comma separated list or repeated flag)")

	discoverCmd.Flags().AddFlagSet(requiredFlags)

	groups[requiredFlags] = "Required Flags"

	discoverCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	discoverCmd.MarkFlagRequired("region")

	return discoverCmd
}

func preRunDiscoverV2(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runDiscoverV2(cmd *cobra.Command, args []string) error {
	opts, err := parseDiscoverV2Opts()
	if err != nil {
		return fmt.Errorf("failed to parse discover opts: %v", err)
	}

	discoverer := discover_v2.NewDiscovererV2(*opts)

	if err := discoverer.Run(); err != nil {
		return fmt.Errorf("failed to discover: %v", err)
	}

	return nil
}

func parseDiscoverV2Opts() (*discover_v2.DiscovererV2Opts, error) {
	opts := discover_v2.DiscovererV2Opts{
		Regions: regions,
	}

	return &opts, nil
}
