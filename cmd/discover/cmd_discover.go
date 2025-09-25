package discover

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	regions []string
)

func NewDiscoverCmd() *cobra.Command {
	discoverCmd := &cobra.Command{
		Use:           "discover",
		Short:         "Multi-region, multi cluster discovery scan of AWS MSK",
		Long:          "Performs a full Discovery of all MSK clusters across multiple regions, and their associated resources, costs and metrics",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunDiscover,
		RunE:          runDiscover,
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

func preRunDiscover(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runDiscover(cmd *cobra.Command, args []string) error {
	opts, err := parseDiscoverOpts()
	if err != nil {
		return fmt.Errorf("failed to parse discover opts: %v", err)
	}

	discoverer := NewDiscoverer(*opts)

	if err := discoverer.Run(); err != nil {
		return fmt.Errorf("failed to discover: %v", err)
	}

	return nil
}

func parseDiscoverOpts() (*DiscovererOpts, error) {
	const stateFile = "kcp-state.json"
	var state *types.State

	// Check if existing state file exists
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		// No state file found - start fresh
		slog.Info("starting with fresh state")
	} else if err != nil {
		// Error checking file - return error
		return nil, fmt.Errorf("failed to check state file: %v", err)
	} else {
		// State file exists - load it
		state = &types.State{}
		if err := state.LoadStateFile(stateFile); err != nil {
			return nil, fmt.Errorf("failed to load existing state file: %v", err)
		}
		slog.Info("using existing state file", "file", stateFile)
	}

	return &DiscovererOpts{
		Regions: regions,
		State:   state,
	}, nil
}
