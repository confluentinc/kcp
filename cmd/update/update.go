package update

import (
	"fmt"

	u "github.com/confluentinc/kcp/internal/generators/update"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func NewUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "update",
		Short:         "Update the kcp binary to the latest version",
		Long:          `Updates the kcp binary to the latest version by downloading latest release from github and installing`,
		SilenceErrors: true,
		RunE:          runUpdate,
	}

	groups := map[*pflag.FlagSet]string{}

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.Bool("force", false, "Force update without user confirmation")
	optionalFlags.Bool("check-only", false, "Only check for updates, don't install")
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
	force, _ := cmd.Flags().GetBool("force")
	checkOnly, _ := cmd.Flags().GetBool("check-only")

	updater := u.NewUpdater()
	if err := updater.Run(force, checkOnly); err != nil {
		return fmt.Errorf("failed to update: %v", err)
	}

	return nil
}
