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
		Short: "Commands for migrating using the Confluent Gateway.",
		Long: `Execute end-to-end Kafka migrations to Confluent Cloud using Cluster Linking and the Confluent Gateway.

The migration runs as a fixed sequence of steps:

1. **Reconcile and validate** — every ` + "`kcp migration execute`" + ` run resolves whether the manifest's route is static or topic-based and validates the cluster link and gateway CRs live against the manifest and the current cluster state. ` + "`--dry-run`" + ` runs this validation alone and exits, touching no state.
2. **Check Lags** — wait until every selected topic's replication lag (the sum of its partition lags) is at or below ` + "`spec.defaultPolicies.lagThreshold`" + ` (default 0: fully caught up). Producers are still running at this point.
3. **Fence Gateway** — fence the route in the gateway CR to block traffic during cutover, and wait for the gateway to confirm it. A static route is fenced as a whole; a topic-based (dynamic) route fences only the topics in this migration.
4. **Pause Offset Sync** (static routes only) — with ` + "`spec.clusterLink.pauseConsumerOffsetSync`" + `, pause cluster-link consumer offset sync (passes through otherwise). Topic-based (dynamic) migrations skip this step — see the note below.
5. **Verify Fence** — only when ` + "`spec.defaultPolicies.detectUnroutedProducersDuration`" + ` is set (minimum 10s; the default, 0, skips this step): take two source offset snapshots that far apart. If any partition's offset advanced, a producer is bypassing the gateway and the run stops (see "Failure and rollback" below). This is the only check for unrouted producers before promote, and it only sees producers active during the window.
6. **Promote Topics** — promote mirror topics at zero lag and wait for each to reach STOPPED. Promote is the point of no return.
7. **Switch Gateway** — switch the route in the gateway CR to Confluent Cloud: a static route is switched as a whole, a dynamic route gets a routing condition for this migration's topics.
8. **Restore Offset Sync** (static routes only) — with ` + "`spec.clusterLink.pauseConsumerOffsetSync`" + `, set cluster-link consumer offset sync back to the declared ` + "`spec.clusterLink.consumerOffsetSyncBaseline`" + `; a failure fails the run, and re-running retries it.

` + "`spec.clusterLink.pauseConsumerOffsetSync`" + ` has no effect for a topic-based migration: a dynamic route requires consumer offset sync to be disabled, so a run that sets it is refused up front when the manifest is reconciled.

**Failure and rollback.** If a step fails after the fence is up but before any topic is promoted, kcp removes the fence and restores offset sync, so traffic returns to the source. Once any topic is promoted or promoting, kcp never removes the fence — sending those clients back to the source would split them from the target. Fix the cause and re-run ` + "`kcp migration execute`" + ` to roll forward. If removing the fence itself fails, the error is logged and the original failure is returned, so check the gateway CR.

Interrupting kcp (Ctrl-C) does not remove the fence: kcp has no signal handler, so the gateway stays fenced until you re-run ` + "`kcp migration execute`" + `.

If execution is interrupted at any step, re-running ` + "`kcp migration execute`" + ` safely continues: every run re-derives the migration's state live from the manifest and the cluster, and re-applying an already-completed step is safe (idempotent).

**Before you run it.** Every run, including ` + "`--dry-run`" + `, connects to the source Kafka cluster, the destination Kafka cluster, the cluster-link REST API and the Kubernetes API, using the credentials in the manifest. To confirm each gateway pod applied a config change, the Kubernetes identity also needs ` + "`get`" + ` on ` + "`pods/proxy`" + `. All producers must go through the gateway for the fence to hold. If the reconcile plan is refused — for example a selected topic is not a mirror topic on the cluster link — nothing is changed and the whole run is refused; ` + "`--dry-run`" + ` prints the plan and the reasons.

The manifest these commands read is documented in the [Migration manifest reference](../../migration-manifest-reference.md) — every field, validation rules, and a fully-annotated example.`,
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
