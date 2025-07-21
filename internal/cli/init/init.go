package init

import (
	"fmt"

	i "github.com/confluentinc/kcp/internal/generators/init"
	"github.com/spf13/cobra"
)

func NewInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a set of env vars to export",
		Long: `Generate a comprehensive set of environment variables to export.
eg.
export VPC_ID=vpc-1234567890
export REGION=us-east-1

etc
		`,
		Example: `
		`,
		SilenceErrors: true,
		RunE:          runInitConfig,
	}

	return cmd
}

func runInitConfig(cmd *cobra.Command, args []string) error {
	initializer := i.NewInitializer()
	if err := initializer.Run(); err != nil {
		return fmt.Errorf("failed to generate config: %v", err)
	}

	return nil
}
