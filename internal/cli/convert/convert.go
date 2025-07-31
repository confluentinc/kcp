package convert

import (
	"github.com/confluentinc/kcp/internal/cli/convert/acls"
	"github.com/spf13/cobra"
)

func NewConvertCmd() *cobra.Command {
	convertCmd := &cobra.Command{
		Use:   "convert",
		Short: "Convert various Kafka resources and configurations",
		Long:  "Convert various Kafka resources and configurations to their respective Confluent Cloud equivalents.",
	}

	convertCmd.AddCommand(
		acls.NewConvertAclsCmd(),
	)

	return convertCmd
}
