package migrate

import (
	"github.com/confluentinc/kcp/cmd/migrate/apply"
	"github.com/confluentinc/kcp/cmd/migrate/validate"
	"github.com/spf13/cobra"
)

func NewMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Migrate Kafka resources directly to Confluent Cloud or Confluent Platform",
		Hidden:        true, // in-development direct-API feature; kept in the binary but not user-facing (cascades to --help and gen-docs)
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}
	cmd.AddCommand(validate.NewMigrateValidateCmd())
	cmd.AddCommand(apply.NewMigrateApplyCmd())
	return cmd
}
