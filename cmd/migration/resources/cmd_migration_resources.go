package resources

import (
	"github.com/confluentinc/kcp/cmd/migration/resources/create"
	"github.com/confluentinc/kcp/cmd/migration/resources/validate"
	"github.com/spf13/cobra"
)

func NewMigrationResourcesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "resources",
		Short:         "Migrate Kafka resources directly to Confluent Cloud or Confluent Platform",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}
	cmd.AddCommand(validate.NewMigrationResourcesValidateCmd())
	cmd.AddCommand(create.NewMigrationResourcesCreateCmd())
	return cmd
}
