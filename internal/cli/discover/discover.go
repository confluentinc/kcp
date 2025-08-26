package discover

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	regions []string
)

func NewDiscoverCmd() *cobra.Command {
	discoverCmd := &cobra.Command{
		Use:           "discover",
		Short:         "Discover msk clusters and information",
		Long:          "For given regions, discover msk clusters and collect information about them",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Discovering Kafka clusters")
		},
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false

	requiredFlags.StringSliceVar(&regions, "regions", []string{}, "The AWS regions to discover")

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

	return discoverCmd
}
