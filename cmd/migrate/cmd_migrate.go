package migrate

import (
	"github.com/confluentinc/kcp/cmd/migrate/validate"
	"github.com/spf13/cobra"
)

func NewMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate Kafka resources directly to Confluent Cloud or Confluent Platform",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(validate.NewMigrateValidateCmd())
	return cmd
}
