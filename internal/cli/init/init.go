package init

import (
	"fmt"

	i "github.com/confluentinc/kcp/internal/generators/init"
	"github.com/spf13/cobra"
)

func NewInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a README.md and set of env vars to export",
		Long: `Generates a README.md to guide usage of kcp and a script to export environment variables for various kcp commands.
eg.
export VPC_ID=vpc-1234567890
export REGION=us-east-1

export SASL_SCRAM_USERNAME=<msk-username>
export SASL_SCRAM_PASSWORD=<msk-password>
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
