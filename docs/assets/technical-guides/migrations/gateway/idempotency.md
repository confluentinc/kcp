# Idempotency and Resumability of Gateway Migrations

You can re-run `kcp migration cutover execute` after any failure, interruption, or completed migration. This page explains, for each workflow, what a re-run does and doesn't do.

For what to do at each failure point, see the "If a run stops" table in the guide for your workflow: [static](static-migration.md#if-a-run-stops), [dynamic](dynamic-migration.md#if-a-run-stops), or [conversion](dynamic-to-static-conversion.md#if-a-run-stops).

## How a run works

kcp keeps no state between runs: no state file, lock, run ID, or marker on a cluster. `metadata.name` in the manifest labels the log lines and is never written anywhere. What survives a run is only what it left on external systems, listed in [What a run leaves behind](#what-a-run-leaves-behind).

Every run does the same two things:

1. **Reconcile, once.** kcp reads the manifest, the live Gateway custom resource (CR), both clusters, and the cluster link, and classifies each topic (or, for a conversion, the route and the link's scope). A refusal is all-or-nothing: if any check fails, nothing is changed. If nothing is left to do, the run stops here and exits zero.
2. **Walk the steps.** kcp walks the full list of steps for the workflow from the first. The reconcile plan decides each step: a step either has nothing to do, or applies its change again. kcp doesn't re-check the live state for each step along the way.

Re-applying a completed step is safe, but some steps cost time again:

- **Fence.** A resume re-patches the fence even if it is already on the route. The value is identical, but the patch carries a fresh `spec.configId`, so it can cost another hot reload or pod roll before kcp moves on. An identical dynamic fence entry is replaced, never duplicated.
- **Offset sync pause** (static). kcp waits the [`consumerOffsetSyncDrainDuration`](../../../migration-assets/cutover/policies.md#policies-consumeroffsetsyncdrainduration) again, then sets the configuration to `false` again.
- **Unrouted-producer window.** The full [`detectUnroutedProducersDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedproducersduration) window runs again, with clients still blocked. A conversion likewise repeats its `detectUnroutedCommitsDuration` window.
- **Lag wait.** Polls again from the start.

## What a run leaves behind

| Where                      | What                                                                                                                                                                                                                                                           | Workflows                                           |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| Gateway CR route           | The fence (see below). Dynamic also adds a routing condition for the batch.                                                                                                                                                                                    | All                                                 |
| Gateway CR `spec.configId` | A random `kcp-<hex>` value, set by every patch when the Gateway supports it. Never cleaned up. Also set once by the hot-reload check at the start of the fence step, so a run that fails after the lag wait has already changed it, with no effect on traffic. | All, with hot reload                                |
| Cluster link               | `consumer.offset.sync.enable`, and each mirror's status. Promotion is irreversible.                                                                                                                                                                            | Configuration: static. Mirrors: static and dynamic. |
| Destination                | Committed consumer group offsets.                                                                                                                                                                                                                              | Conversion                                          |
| `kcp.log`                  | Diagnostic only. kcp never reads it back.                                                                                                                                                                                                                      | All                                                 |

### How kcp recognizes its own fence

kcp doesn't tag its fence. It recognizes it by exact shape, and anything else is treated as yours:

- **Static:** a route-level fence of exactly `scope: ALL` with the defaulted error code, `BROKER_NOT_AVAILABLE`. An operator's plain `scope: ALL` fence reads back identically, so kcp can't tell it from its own and removes it. Any other non-`NONE` fence is refused.
- **Dynamic:** a `rules.fencing` entry with only `topics` and `blocked: true`, where `topics` is exactly this batch's resolved set. Any other entry, such as one with a `trafficType` key or a different topic set, is treated as an operator's. The fence, the switch, and a rollback all leave it untouched.
- **Conversion:** a `rules.fencing` entry over every topic. Any other `rules.fencing` entry is refused as a foreign fence.

Two kcp migrations that produce the same shape can't be told apart.

## Step by step

### Static

| Step                  | On a re-run                                                                                                                  |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Check lag             | Skipped when no topic needs promoting. A run with only promoted-but-unswitched topics skips it but still fences, verifies, and switches. Otherwise polls again, with no timeout. |
| Fence                 | Re-patched. Skipped only if the plan has nothing to fence.                                                                   |
| Pause offset sync     | Waits the drain duration, then sets the configuration again. Skipped unless `pause` is set and the plan has a fence to apply. |
| Verify the fence      | The full window runs again, and covers promoted-but-unswitched topics too, because writes to a promoted topic's source are never copied. It fails if a source offset rises or offsets can't be read. Skipped if the duration is `0s`. |
| Promote               | Mirrors already `STOPPED` aren't promoted again. Mirrors in `PENDING_STOPPED` are awaited, not promoted again. A promote error is retried up to three times per topic, then the run fails. |
| Switch                | Skipped if the route is already bound to the destination.                                                                    |
| Restore offset sync   | Runs only when owed: `pause` is set and either this run migrated topics or the link's live setting differs from `baseline`.  |

### Dynamic

The same steps without the offset sync pause and restore. A dynamic migration doesn't touch offset sync, and refuses a link that has it enabled.

### Conversion

| Step              | On a re-run                                                                                                                                         |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| Fence             | Re-patched.                                                                                                                                         |
| Verify the fence  | Never skipped. kcp re-gathers every fact, re-runs the route, credential, and link checks, and takes two offset snapshots a window apart.              |
| Sync offsets      | Commits every group's offsets again. Commits are idempotent, so a partial earlier attempt, or a rollback and redo, repeats them harmlessly.        |
| Switch            | Replaces the route. Skipped if the route is already static on the destination.                                                                       |

A conversion has no lag or promote step.

## Interruptions

### Ctrl+C and crashes

Ctrl+C, `SIGTERM` (a canceled CI job, a pod eviction), and a crash all end the process where it is, and kcp doesn't roll back or clean up. Whatever is on the cluster stays, which typically means:

- The fence is on the route.
- For a static migration with `pause: true`, offset sync is still paused.
- Mirrors might be part-way through promotion.

The next run finds the fence by its shape, continues from the first step, and repeats the work in [How a run works](#how-a-run-works). If a resumed run fails before reaching its own fence step, it removes the earlier fence, unless rollback is no longer allowed.

### Interrupted during promote

The cluster link finishes any promotion it has already accepted, whether or not kcp is running. A re-run finds those mirrors in `PENDING_STOPPED`, waits for them to reach `STOPPED` and doesn't promote them again. With [`promoteBatchSize`](../../../migration-assets/cutover/policies.md#policies-promotebatchsize) set, a run that stops between batches resumes by waiting for the batch in flight and then promoting the rest.

### Where rollback stops

Until the point of no return, a failure removes the fence and restores offset sync, and traffic returns to the source.

| Workflow           | Rolled back on failure during                                  | Point of no return                                                  |
| ------------------ | -------------------------------------------------------------- | ------------------------------------------------------------------- |
| Static             | The fence, and the offset sync pause                           | The start of promote, even if nothing has been promoted yet         |
| Dynamic            | The fence, including the unrouted-producer check               | The start of promote                                                |
| Conversion         | The fence, the verify step, and the offset sync                | The switch                                                          |

Rollback is also off from the start of a run that finds a topic already promoted or promoting, because sending those clients back would split them from the destination. In that case even an early failure, such as a lag error, keeps the fence.

**A fence that was written but not confirmed.** If kcp patched the fence but couldn't confirm that every pod applied it, kcp rolls it back even if it appears not to have landed, because it could still take effect after kcp exits. Only a run where rollback isn't allowed keeps the fence. This rollback also restores offset sync, and it can cost another hot reload or pod roll.

**When the rollback itself has a problem.**

- The offset sync restore after a rollback is best effort. If it fails, kcp prints the command to run by hand.
- If removing the fence fails on a static or dynamic abort, kcp logs the error and returns the original failure, so the fence might still be on the route. Check the Gateway CR.
- A conversion, and the unconfirmed-fence rollback on any workflow, report the rollback failure alongside the original one.

**When a producer writes to the source after rollback stops.** If the unrouted-producer check fails after the point of no return, the fence stays. Stop the producer or route it through the Gateway, then re-run.

## Re-run a finished migration

A finished static or dynamic migration prints `Migration completed: <name>` with `nothing to do: no topic in it still needs migrating`, and exits zero. A finished conversion prints `Route conversion completed: <name>` with `nothing to do: the route is already static on its target domain`. In both cases only the reconcile reads happen: no Gateway patch, no capability probe, no promote.

Two cases are not "nothing to do":

- **Static with `pause: true`.** If the link's `consumer.offset.sync.enable` no longer equals `consumerOffsetSync.baseline`, kcp runs the restore step and sets it back. If you turned offset sync off on purpose after the cutover, a re-run turns it back on. kcp takes `baseline` from the manifest because the link doesn't record the value it had before, so update `baseline` to the state you want, or skip the re-run.
- **A switch that was applied but not confirmed.** Reconcile reads the Gateway CR, not the pods. If the switch reached the CR but kcp couldn't confirm every pod applied it, the next run sees the topics routed to the destination and reports nothing to do. For a conversion kcp tells you not to re-run. For static and dynamic migrations it doesn't, so check the Gateway's acceptance and pods before you re-run.

## Change the manifest between runs

kcp reads policies, credentials, and flags fresh on every run. An environment variable named like a policy flag overrides the manifest without telling you.

| Change                                        | Effect                                                                                                                                  |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Dynamic: the resolved topic set changes       | The earlier fence is no longer recognized as kcp's. It stays on the route after the switch, and a rollback doesn't lift it. Remove the leftover `blocked` entry by hand. |
| Dynamic: a source topic is deleted mid-way    | The run is refused: `<topic> not found on the source`.                                                                                  |
| Static: the topic selection changes           | The fence is recognized by shape, so it isn't orphaned. The route still flips as a whole. A topic dropped from the selection while still `ACTIVE` is cut over without being promoted. |
| Static: `pause` is set to `false` on a resume | No restore is owed and no rollback restore runs. A link that an interrupted run paused stays paused.                                     |
| `targetStreamingDomain` changes               | Reconcile evaluates against the live route. Mid-fence, the run proceeds with the new target if it is declared and staged. After completion it is refused. |
| `clusterLink.name` changes                    | Mirrors are read from the new link, so topics not on it fail the run as `not on the cluster link`.                                       |
| Dynamic: batches overlap                      | Tolerated. Completed topics are unchanged, and only the rest is fenced.                                                                  |

## Concurrency

kcp doesn't coordinate concurrent runs: it uses no lock, lease, or `resourceVersion` check. The guard is on each Gateway patch. It tests the route's index and the field being overwritten against the value kcp read moments earlier, so a concurrent edit between kcp's read and its patch makes the patch fail.

Some cases fall outside that guard:

- **Drift since reconcile.** The fence, switch, and rollback are computed at reconcile time. An edit made to the route between reconcile and the patch, for example during a long lag wait, can be overwritten.
- **Concurrent batches on one dynamic route.** Each writes the whole `rules` block, so they can overwrite each other. Run one batch at a time.
- **Two runs of the same migration.** Each patch stamps a new `configId`, so one run's per-pod wait can time out because the other superseded it.

kcp can't tell when a GitOps controller reverts its fence, so consider pausing syncing on the Gateway CR for the duration of the cutover.

## What `--dry-run` guarantees

`--dry-run` runs reconcile and stops there, and exits non-zero if the plan is refused. It makes no Gateway write, link configuration write, promote, or capability probe.

Reads it performs: the Gateway CR, the Gateway's Deployment and pods (and, with hot reload on, each pod's `/config`), topic lists and cluster IDs on both clusters, the link's state, mirrors, and configurations, and for a static migration the staged Secrets. A conversion also lists groups, describes topics, reads offsets and partition counts, and sends one write probe: an offset commit for a group and topic that don't exist, to test the destination credential. It creates nothing.

Because it stops there, a passing `--dry-run` doesn't exercise the Kubernetes patch permissions, the cluster-scoped hot-reload grant, or the link write permission. See [Permissions](permissions.md) for what to grant ahead of a real run.

## Related content

- [Gateway migrations](overview.md)
- [Permissions for Gateway migrations](permissions.md)
- [Gateway migration limitations](limitations.md)
- [Run a migration cutover](../../../migration-assets/cutover/running-a-cutover.md)
