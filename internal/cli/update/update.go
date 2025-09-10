package update

import (
	"fmt"

	u "github.com/confluentinc/kcp/internal/generators/update"
	"github.com/spf13/cobra"
)

func NewUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "update",
		Short:         "Update the kcp binary to the latest version",
		Long:          `Updates the kcp binary to the latest version by downloading latest release from github and installing`,
		SilenceErrors: true,
		RunE:          runUpdate,
	}

	cmd.Flags().Bool("force", false, "Force update without user confirmation")
	cmd.Flags().Bool("check-only", false, "Only check for updates, don't install")

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
