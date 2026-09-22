package migration

import (
	"github.com/confluentinc/kcp/cmd/migration/execute"
	"github.com/confluentinc/kcp/cmd/migration/lagcheck"
	"github.com/confluentinc/kcp/cmd/migration/reconcile"

	"github.com/spf13/cobra"
)

func NewMigrationCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:   "migration",
		Short: "Commands for migrating using CPC Gateway.",
		Long: `Execute end-to-end Kafka migrations to Confluent Cloud using the Confluent Platform Connect (CPC) Gateway.

The migration workflow follows a defined lifecycle managed by a finite state machine:

1. **Reconcile and validate** — every ` + "`kcp migration execute`" + ` run resolves whether the manifest's route is static or topic-based and validates the cluster link and gateway CRs live against the manifest and the current cluster state — no separate init step, and nothing is persisted between runs. ` + "`--dry-run`" + ` runs this validation alone and exits, touching no state.
2. **Check Lags** — compare source and destination offsets until lag drops below the configured threshold.
3. **Fence Gateway** — apply the fenced gateway CR to block traffic during cutover.
4. **Pause Offset Sync** (static routes only) — with ` + "`spec.clusterLink.pauseConsumerOffsetSync`" + `, pause cluster-link consumer offset sync (passes through otherwise); on failure the fence is rolled back automatically. Topic-based (dynamic) migrations skip this step — see the note below.
5. **Verify Fence** — monitor source offsets to catch producers bypassing the gateway; on detection the fence is rolled back and any paused offset sync restored.
6. **Promote Topics** — promote mirror topics at zero lag.
7. **Switch Gateway** — apply the switchover gateway CR to route traffic to Confluent Cloud.

` + "`spec.clusterLink.pauseConsumerOffsetSync`" + ` has no effect for a topic-based migration: a dynamic route requires consumer offset sync to be disabled, so a fresh run that sets it is refused at registration.

If execution is interrupted at any step, re-running ` + "`kcp migration execute`" + ` safely continues: there is no persisted position to resume from — every run re-derives the migration's state live from the manifest and the cluster, and re-applying an already-completed step is a no-op.

Supporting documentation:

- [Gateway Switchover Examples](../../gateway-switchover/index.md) — Gateway CR YAML templates for each supported authentication combination.
- [Getting Started with Zero-Cut Migrations](../../getting-started-with-zero-cut-migrations.md) — end-to-end reference for the KCP + Gateway approach, including networking and auth pre-requisites.`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	migrationCmd.AddCommand(
		execute.NewMigrationExecuteCmd(),
		lagcheck.NewMigrationLagCheckCmd(),
		reconcile.NewMigrationReconcileCmd(),
	)

	return migrationCmd
}
