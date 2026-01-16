package update2

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	force     bool
	checkOnly bool
)

func NewUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "update2",
		Short:         "Update the kcp binary to the latest version",
		Long:          `Updates the kcp binary to the latest version by downloading latest release from github and installing`,
		SilenceErrors: true,
		RunE:          runUpdate,
	}

	groups := map[*pflag.FlagSet]string{}

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&force, "force", false, "Force update without user confirmation")
	optionalFlags.BoolVar(&checkOnly, "check-only", false, "Only check for updates, don't install")
	groups[optionalFlags] = "Optional Flags"

	cmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{optionalFlags}
		groupNames := []string{"Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		return nil
	})

	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	opts, err := parseUpdateOpts()
	if err != nil {
		return fmt.Errorf("failed to parse update opts: %v", err)
	}

	updater := NewUpdater2(*opts)
	if err := updater.Run(); err != nil {
		return fmt.Errorf("failed to update: %v", err)
	}

	return nil
}

func parseUpdateOpts() (*Updater2Opts, error) {
	opts := Updater2Opts{
		Force:     force,
		CheckOnly: checkOnly,
	}

	return &opts, nil
}
