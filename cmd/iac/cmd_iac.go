package iac

import (
	"github.com/confluentinc/kcp/cmd/iac/acls"
	"github.com/confluentinc/kcp/cmd/iac/bastion_host"
	"github.com/confluentinc/kcp/cmd/iac/connectors"
	"github.com/confluentinc/kcp/cmd/iac/migration_infra"
	"github.com/confluentinc/kcp/cmd/iac/reverse_proxy"
	"github.com/confluentinc/kcp/cmd/iac/schemas"
	targetinfra "github.com/confluentinc/kcp/cmd/iac/target_infra"
	"github.com/confluentinc/kcp/cmd/iac/topics"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

func NewIacCmd() *cobra.Command {
	iacCmd := utils.NewGroupCmd(&cobra.Command{
		Use:   "iac",
		Short: "Generate infrastructure and migration assets",
		Long:  "Generate various infrastructure and migration assets including bastion host configurations, data migration tools, and target environment setups.",
	})

	iacCmd.AddCommand(
		utils.NewNounCmd("bastion-host", bastion_host.NewGenerateCmd()),
		acls.NewAclsCmd(),
		connectors.NewConnectorsCmd(),
		utils.NewNounCmd("topics", topics.NewGenerateCmd()),
		utils.NewNounCmd("schemas", schemas.NewGenerateCmd()),
		utils.NewNounCmd("migration-infra", migration_infra.NewGenerateCmd()),
		utils.NewNounCmd("reverse-proxy", reverse_proxy.NewGenerateCmd()),
		utils.NewNounCmd("target-infra", targetinfra.NewGenerateCmd()),
	)

	return iacCmd
}
