package migration

import (
	"github.com/confluentinc/kcp/cmd/migration/execute"
	i "github.com/confluentinc/kcp/cmd/migration/init"
	"github.com/confluentinc/kcp/cmd/migration/lagcheck"
	"github.com/confluentinc/kcp/cmd/migration/list"

	"github.com/spf13/cobra"
)

func NewMigrationCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:   "migration",
		Short: "Commands for migrating using CPC Gateway.",
		Long: `Execute end-to-end Kafka migrations to Confluent Cloud using the Confluent Platform Connect (CPC) Gateway.

The migration workflow follows a defined lifecycle managed by a finite state machine:

1. **Initialize** — validate cluster link and gateway CRs, persist migration config (` + "`kcp migration init`" + `).
2. **Check Lags** — compare source and destination offsets until lag drops below the configured threshold.
3. **Fence Gateway** — apply the fenced gateway CR to block traffic during cutover.
4. **Promote Topics** — promote mirror topics at zero lag.
5. **Switch Gateway** — apply the switchover gateway CR to route traffic to Confluent Cloud.

If execution is interrupted at any step, re-running ` + "`kcp migration execute`" + ` resumes from the last completed step.

Supporting documentation:

- [Gateway Switchover Examples](../../gateway-switchover/index.md) — Gateway CR YAML templates for each supported authentication combination.
- [Getting Started with Zero-Cut Migrations](../../getting-started-with-zero-cut-migrations.md) — end-to-end reference for the KCP + Gateway approach, including networking and auth pre-requisites.`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	migrationCmd.AddCommand(
		i.NewMigrationInitCmd(),
		execute.NewMigrationExecuteCmd(),
		lagcheck.NewMigrationLagCheckCmd(),
		list.NewMigrationListCmd(),
	)

	return migrationCmd
}
