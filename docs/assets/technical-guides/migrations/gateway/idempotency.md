# Idempotency and resumability

You can re-run `kcp migration cutover execute` after any failure, interruption or completed migration. It doesn't pick up where it stopped from a saved record. It reads the live world and continues from there. This page explains exactly what that means for each workflow, so you know what a re-run will and won't do.

For what to do at each failure point, see the "If a run stops" table in the guide for your workflow: [static](static-migration.md#if-a-run-stops), [dynamic](dynamic-migration.md#if-a-run-stops) or [conversion](dynamic-to-static-conversion.md#if-a-run-stops).

## How a run works

kcp keeps no state between runs. There is no state file, lock, run id or marker on a cluster. `metadata.name` in the manifest labels the log lines and is never written anywhere. What survives a run is only what it left on external systems, listed [below](#what-a-run-leaves-behind).

Every run does the same two things:

1. **Reconcile, once.** kcp reads the manifest, the live Gateway CR, both clusters and the cluster link, and classifies each topic (or, for a conversion, the route and the link's scope). A refusal is all-or-nothing: if any check fails, nothing is changed. If nothing is left to do, the run stops here and exits zero.
2. **Walk the steps.** kcp walks the full list of steps for the workflow from the first. Each step either does nothing because the reconcile plan says there is nothing for it to do, or applies its change again. It does not ask the live world whether the step is done.

That second point matters. "Re-applying an already-completed step is safe" is true, but a re-applied step isn't free:

- **Fence.** A resume re-patches the fence even if it is already on the route. The value is identical, but the patch carries a fresh `spec.configId`, so it can cost another hot reload or pod roll before kcp moves on.
- **Offset sync pause** (static). Set to `false` again, blind.
- **Unrouted-producer window.** The full [`detectUnroutedProducersDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedproducersduration) window runs again, with clients still blocked. A conversion likewise repeats its `detectUnroutedCommitsDuration` window.
- **Lag wait.** Polls again from the start.

## What a run leaves behind

| Where                     | What                                                                                                   | Workflows           |
| ------------------------- | ------------------------------------------------------------------------------------------------------ | ------------------- |
| Gateway CR route          | Static: a route-level fence, `scope: ALL` and `errorCode: BROKER_NOT_AVAILABLE`. Dynamic: a `rules.fencing` entry of `{topics, blocked: true}` and a routing condition for the batch. Conversion: a `rules.fencing` entry over every topic. | All                 |
| Gateway CR `spec.configId` | A random `kcp-<hex>` value, set by every patch when the Gateway supports it. Never cleaned up. Also set once by the hot-reload check before the fence. | All, with hot reload |
| Cluster link              | `consumer.offset.sync.enable`, and each mirror's status. Promotion is irreversible.                    | Static (config); static and dynamic (mirrors) |
| Destination               | Committed consumer group offsets.                                                                      | Conversion          |
| `kcp.log`                 | Diagnostic only. kcp never reads it back.                                                              | All                 |

### How kcp recognises its own fence

kcp doesn't tag its fence. It recognises it by exact shape, and anything else is treated as yours:

- **Static:** exactly `scope: ALL` with the defaulted error code. An operator's plain `scope: ALL` fence reads back identically, so kcp can't tell it from its own and removes it. Any other non-`NONE` fence is refused.
- **Dynamic:** a `rules.fencing` entry with only `topics` and `blocked: true`, where `topics` is exactly this batch's resolved set. A fence with a different set is treated as an operator's and left in place.
- **Conversion:** a `rules.fencing` entry over every topic. Any other `rules.fencing` entry is refused as a foreign fence.

Two kcp migrations that produce the same shape can't be told apart.

## Step by step

### Static

| Step                  | On a re-run                                                                                                                  |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Check lag             | Skipped if no topic is left to migrate. Otherwise polls again. No timeout.                                                  |
| Fence                 | Re-patched. Skipped only if the plan has nothing to fence.                                                                   |
| Pause offset sync     | Set again. Skipped unless `pause` is set and there is a fence to apply.                                                      |
| Verify the fence      | The full window runs again. Skipped if the duration is `0s`.                                                                 |
| Promote               | Mirrors already `STOPPED` aren't promoted again. Mirrors in `PENDING_STOPPED` are awaited, not promoted again. A promote error is retried up to three times per topic, then the run fails. |
| Switch                | Skipped if the route is already bound to the destination.                                                                    |
| Restore offset sync   | Runs only when owed: `pause` is set and either this run migrated topics or the link's live setting differs from `baseline`.  |

### Dynamic

The same steps without the offset sync pause and restore. A dynamic migration doesn't touch offset sync, and refuses a link that has it enabled.

### Conversion

| Step              | On a re-run                                                                                                                                         |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| Fence             | Re-patched.                                                                                                                                         |
| Verify the fence  | Never skipped. kcp re-gathers every fact, re-runs the route, credential and link checks, and takes two offset snapshots a window apart.              |
| Sync offsets      | Commits every group's offsets again. Commits are idempotent, so a partial earlier attempt, or a rollback and redo, repeats them harmlessly.        |
| Switch            | Replaces the route. Skipped if the route is already static on the destination.                                                                       |

A conversion has no lag or promote step.

## Interruptions

### Ctrl-C and crashes

kcp has no signal handler. `Ctrl-C`, `SIGTERM` (a cancelled CI job, a pod eviction) and a crash all end the process where it is. There is no rollback and no cleanup. Whatever is on the cluster stays, which typically means:

- the fence is on the route;
- for a static migration with `pause: true`, offset sync is still paused;
- mirrors may be part-way through promotion.

The next run finds the fence by its shape, continues from the first step, and repeats the work in [How a run works](#how-a-run-works). If a resumed run fails before reaching its own fence step, it removes the earlier fence, unless rollback is no longer allowed.

### Where rollback stops

Until the point of no return a failure removes the fence and restores offset sync, and traffic returns to the source.

| Workflow           | Rolled back on failure during                                  | Point of no return                                                  |
| ------------------ | -------------------------------------------------------------- | ------------------------------------------------------------------- |
| Static             | The fence, and the offset sync pause                           | The start of promote, even if nothing has been promoted yet         |
| Dynamic            | The fence, including the unrouted-producer check               | The start of promote                                                |
| Conversion         | The fence, the verify step and the offset sync                 | The switch                                                          |

Rollback is also off from the start of a run that finds a topic already promoted or promoting, because sending those clients back would split them from the destination. In that case even an early failure, such as a lag error, keeps the fence.

If removing the fence itself fails, kcp logs the error and returns the original failure, so the fence may still be there. Check the Gateway CR.

### Timeouts

| Wait                                         | Bound                                                                                                  |
| -------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| Lag before the fence                         | None. Fails after three consecutive offset read failures.                                              |
| Zero lag and mirror `STOPPED` during promote | None, and not bounded by `rolloutTimeout`. The fence stays up while it waits.                          |
| Operator acceptance and Gateway rollout      | [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout). Default: no deadline. |
| Per-pod config wait (hot reload)             | [`hotReloadTimeout`](../../../migration-assets/cutover/policies.md#policies-hotreloadtimeout). Default: 90 seconds. |

## Re-running a finished migration

A finished static or dynamic migration prints `Migration completed: <name>` with `nothing to do: no topic in it still needs migrating`, and exits zero. A finished conversion prints `Route conversion completed: <name>` with `nothing to do: the route is already static on its target domain`. In both cases only the reconcile reads happen: no Gateway patch, no capability probe, no promote.

Two cases are not "nothing to do":

- **Static with `pause: true`.** If the link's `consumer.offset.sync.enable` no longer equals `consumerOffsetSync.baseline`, kcp runs the restore step and sets it back. If you turned offset sync off on purpose after the cutover, a re-run turns it back on. Change `baseline` first, or don't re-run.
- **A switch that was applied but not confirmed.** Reconcile reads the Gateway CR, not the pods. If the switch reached the CR but kcp couldn't confirm every pod applied it, the next run sees the topics routed to the destination and reports nothing to do. For a conversion kcp warns you not to re-run. For static and dynamic migrations it doesn't. Check the Gateway's acceptance and pods before you re-run.

## Changing the manifest between runs

kcp reads policies, credentials and flags fresh on every run. An environment variable named like a policy flag overrides the manifest without telling you.

| Change                                        | Effect                                                                                                                                  |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Dynamic: the resolved topic set changes       | The earlier fence is no longer recognised as kcp's. It stays on the route after the switch, and a rollback won't lift it. Remove the leftover `blocked` entry by hand. |
| Dynamic: a source topic is deleted mid-way    | The run is refused: `<topic> not found on the source`.                                                                                  |
| Static: the topic selection changes           | The fence is recognised by shape, so it isn't orphaned. The route still flips as a whole. A topic dropped from the selection while still `ACTIVE` is cut over without being promoted. |
| Static: `pause` is set to `false` on a resume | No restore is owed and no rollback restore runs. A link that an interrupted run paused stays paused.                                     |
| `targetStreamingDomain` changes               | Reconcile evaluates against the live route. Mid-fence, the run proceeds with the new target if it is declared and staged. After completion it is refused. |
| `clusterLink.name` changes                    | Mirrors are read from the new link, so topics not on it fail the run as `not on the cluster link`.                                       |
| Dynamic: batches overlap                      | Tolerated. Completed topics are unchanged, and only the rest is fenced.                                                                  |

## Concurrency

Nothing stops two runs at once. There is no lock, lease or `resourceVersion` check. The only guard is on each Gateway patch: it tests the route's index and the field being overwritten against the value kcp read moments earlier, so a concurrent edit between kcp's read and its patch makes the patch fail.

What that doesn't cover:

- **Drift since reconcile.** The fence, switch and rollback are computed at reconcile time. An edit made to the route between reconcile and the patch, for example during a long lag wait, can be overwritten.
- **Concurrent batches on one dynamic route.** Each writes the whole `rules` block, so they can overwrite each other. Run one batch at a time.
- **Two runs of the same migration.** Each patch stamps a new `configId`, so one run's per-pod wait can time out because the other superseded it.

The last two are inferred from the code rather than tested.

A GitOps controller that reverts the fence is not detectable. Pause it for the duration of the cutover.

## What `--dry-run` guarantees

`--dry-run` runs reconcile and nothing else, and exits non-zero if the plan is refused. It makes no Gateway write, link config write, promote or capability probe.

Reads it performs: the Gateway CR, topic lists and cluster ids on both clusters, the link's state, mirrors and configs, and for a static migration the staged Secrets. A conversion also lists groups, describes topics, reads offsets and partition counts, and sends one write probe: an offset commit for a group and topic that don't exist, to test the destination credential. It creates nothing.

Because it stops there, a `--dry-run` that passes doesn't prove the Kubernetes patch permissions, the hot-reload prerequisites or the link write permission. See [Permissions](permissions.md).

## Before a run changes anything

"Nothing was changed" before the fence is nearly true. With hot reload on, kcp stamps `spec.configId` on the live CR once, at the start of the fence step, to prove the Gateway applies a new revision. That happens after the lag wait, so a run that is refused or fails later has already touched the CR, though with no effect on traffic.

## Related content

- [Gateway migrations](index.md)
- [Permissions](permissions.md)
- [kcp limitations](limitations.md)
- [Running a cutover](../../../migration-assets/cutover/running-a-cutover.md)
