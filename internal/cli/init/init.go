package init

import (
	"fmt"

	i "github.com/confluentinc/kcp/internal/generators/init"
	"github.com/spf13/cobra"
)

func NewInitCmd() *cobra.Command {
	initCmd := &cobra.Command{
		Use:           "init",
		Short:         "Generate a README.md and set of env vars to export",
		Long:          `Generates a README.md to guide usage of kcp and a script to export environment variables for various kcp commands.`,
		SilenceErrors: true,
		RunE:          runInitConfig,
	}

	initCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Fprintf(c.OutOrStdout(), "%s\n\n", c.Short)
		fmt.Fprintf(c.OutOrStdout(), "All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	return initCmd
}

func runInitConfig(cmd *cobra.Command, args []string) error {
	initializer := i.NewInitializer()
	if err := initializer.Run(); err != nil {
		return fmt.Errorf("failed to generate config: %v", err)
	}

	return nil
}
