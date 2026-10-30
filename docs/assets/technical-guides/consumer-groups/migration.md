# Migrate consumer groups

A consumer group is more than its members. It is a set of committed offsets, one per partition it reads, and a **group coordinator**, the broker that tracks who is in the group and who owns which partition. Moving a group to Confluent Cloud means moving both: the committed offsets have to reach the destination so the group resumes where it stopped, and the coordinator has to move so its members can commit and rebalance there. A cutover through the Gateway does this without changing any client configuration. Which mechanism does the work depends on the route.

This guide explains how consumer groups move on each Gateway migration type, what to check before you start, what your consumers see during the switch, and how to confirm the groups came across intact. It builds on [Migrate with a static route](../migrations/gateway/static-migration.md), [Migrate with a dynamic route](../migrations/gateway/dynamic-migration.md) and [Convert a dynamic route to static](../migrations/gateway/dynamic-to-static-conversion.md), which cover the mechanics of each cutover.

## How groups move

|                                                   | Static migration                                              | Dynamic migration                                   | Conversion                                                  |
| ------------------------------------------------- | ------------------------------------------------------------- | --------------------------------------------------- | ----------------------------------------------------------- |
| **Committed offsets reach the destination**       | The cluster link syncs them ahead of the switch               | They don't. Groups keep committing to the source    | kcp copies them itself, during the conversion               |
| **Group coordinator moves**                       | At the switch                                                 | Stays on the source                                 | At the switch                                               |
| **Link's `consumer.offset.sync.enable`**          | On. kcp doesn't check                                         | Off. kcp refuses otherwise                          | Off. kcp refuses otherwise                                  |
| **Which groups**                                  | The groups the link's `consumer.offset.group.filters` selects | Every group on the route, coordinated by the source | Groups whose committed offsets are all on the link's topics |
| **KIP-848 consumers** (`group.protocol=consumer`) | Supported                                                     | Not supported                                       | Supported once the route is static                          |

Consumer groups on a dynamic route are a stage, not a destination. A topic-based migration moves topics, and the groups keep working because the source still coordinates them. The groups themselves only move when you [convert the route to static](#groups-on-a-dynamic-route). If you'd rather move groups in one step, use a static migration.

## Prerequisites and limitations

**All route types**

- Every member of every group connects through the Gateway. A member that connects to the source directly while the rest of the group connects through the route splits the group across two coordinators, and both halves can process the same partitions. The fence can't block a client that bypasses the Gateway, so stop such clients and restart them on the route. A member that reaches the source through another route on the Gateway counts the same way. kcp migrates one route per run, so once this route moves, that member is on a different coordinator. Move every member of a group onto the route before you migrate it.
- Every topic a group has committed offsets on is a mirror topic on the cluster link. Offsets are only ever synced or copied for the link's topics, because the destination rejects offset commits for a topic it doesn't have.
- Consumers can reach the destination with the credentials they use today, and have `READ` on their groups and topics there.

**Static migration**

- Consumer offset sync is **on** for the link, and `consumer.offset.group.filters` includes every group you are moving. The Confluent default for the filter is no groups. A link created with `kcp migration resources create` defaults to an include-all filter when sync is on.
- kcp doesn't check either setting for a static route. A group that isn't in the filter has no committed offsets on the destination, and its consumers start from `auto.offset.reset` there.
- No consumer of a group you are moving runs against the destination before the cutover. The cluster link can overwrite the destination's offsets for a filtered group that is used there, and a group that is active on the destination can leave a mirror topic stuck in `PENDING_MIRROR` during promote. Setting `consumerOffsetSync.pause: true` disables offset sync before promote, which avoids the stuck mirror. See [Migrate from a non-Confluent cluster to Confluent Cloud](https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/migrate-cc.html).

**Dynamic migration and conversion**

- The cluster link's consumer offset sync is **off**, and the route's `rules.routing.coordination.group` is the source. kcp refuses both a link with sync on and a route that doesn't pin coordination to the source.
- Consumers use the classic group protocol. A consumer with `group.protocol=consumer` can't start against a dynamic route.
- Share groups aren't supported on a dynamic route.
- The cluster link and the **source topics stay** until the conversion is done. While the route is dynamic, committed offsets for migrated topics are still stored on the source, and the source rejects commits for a topic it no longer has. The conversion also reads the link to decide which groups are in scope. See [Convert a dynamic route to static](../migrations/gateway/dynamic-to-static-conversion.md#prerequisites-and-limitations).

<!-- prettier-ignore -->
!!! note "Coordination on the destination isn't supported by kcp"
    The Gateway can coordinate groups on the destination for the whole life of a dynamic migration, by setting `coordination.group` to the destination. kcp doesn't run migrations that way. It pins coordination to the source and moves it once, in the conversion. See [Migrate Topics Incrementally with Topic-Based Routing and Fencing](https://docs.confluent.io/private-cloud-gateway/current/gateway-topic-based-routing.html) for the Gateway's own sequence.

**Cluster link permissions**

The link reads the source with the credential it was created with. It needs `READ`, `DESCRIBE` and `DESCRIBE_CONFIGS` on the topics you mirror, and with offset sync on, `READ` and `DESCRIBE` on the consumer groups it syncs. An MSK link uses SCRAM, since IAM credentials can't be used by a cluster link, so these are Kafka ACLs on MSK. See [Permissions](../migrations/gateway/permissions.md#the-links-own-credential).

## Audit your groups

Before you choose a route or write a manifest, list what you have. List every group on the source and the topics each commits on:

```bash
kafka-consumer-groups --bootstrap-server <source-bootstrap> --command-config source.properties --describe --all-groups
```

Look for:

- **Groups that commit on topics outside the migration.** On a conversion, a group with any committed offset on a topic that isn't on the link refuses the run. On a static migration those offsets are never synced.
- **Groups with members outside the Gateway.** Find them before the fence does. A consumer that bypasses the Gateway keeps committing to the source after the switch.
- **Empty and stale groups.** A group with no members still has committed offsets, and is in scope. Delete groups nobody owns, or their stale offsets, rather than carry them across.
- **Groups that already exist on the destination.** A conversion refuses an in-scope group with live members on the destination, and warns about one without members because its offsets are overwritten. On a static migration, look yourself.

## Static migration: groups move with the cutover

On a static route the cluster link carries the committed offsets. The link syncs them from the source to the destination continuously, so by the time you cut over the destination already holds recent offsets for every group in the filter. The switch moves the coordinator, and the consumers pick up from the synced offsets.

### Prepare the link

The link's offset sync is configured on the link, not in the migration manifest:

- `consumer.offset.sync.enable=true`
- `consumer.offset.group.filters`, a JSON filter naming the groups to sync. Each entry has a `name`, a `patternType` (`LITERAL` or `PREFIXED`) and a `filterType` (`INCLUDE` or `EXCLUDE`).
- `consumer.offset.sync.ms`, how often offsets sync. The default is 30 seconds, and it can go as low as one second. A shorter interval keeps the destination's offsets fresher, at the cost of some link throughput.

```json
{
  "groupFilters": [
    { "name": "*", "patternType": "LITERAL", "filterType": "INCLUDE" }
  ]
}
```

Sync is asynchronous, so the destination's offsets always trail the source's by up to one interval. Anything a consumer processed since the last sync is read again after the switch. See [What your consumers see](#what-your-consumers-see).

To check offset sync is working before the window, describe a group on the destination twice, one sync interval apart, while it consumes on the source. Its committed offsets should move. The commands are in [After the cutover](#after-the-cutover).

### What the cutover does with offset sync

The cutover's [stages](../migrations/gateway/static-migration.md#how-the-cutover-works) are the same as for any static migration. Two of them are about offsets:

1. **Fence.** Clients can no longer commit. The source's committed offsets stop moving.
2. **Pause offset sync** _(optional)_. kcp waits [`consumerOffsetSyncDrainDuration`](../../migration-assets/cutover/policies.md#policies-consumeroffsetsyncdrainduration), so the link runs further sync cycles and carries the final committed offsets across, then disables the link's offset sync.
3. After the switch, kcp restores the link's offset sync to your [`consumerOffsetSync.baseline`](../../migration-assets/cutover/migrations.md#consumeroffsetsync-baseline).

Set the drain to at least two of the link's sync intervals, so at least one full cycle completes after the fence. Draining narrows the re-read window but can't close it: sync is asynchronous, so a drain reduces duplicates and doesn't guarantee none.

```yaml
staticMigration:
  # route and topicGroup as in the static guide
  consumerOffsetSync:
    pause: true
    baseline: enabled
  policies:
    consumerOffsetSyncDrainDuration: 60s # two sync intervals at the 30s default
```

Pausing is optional. It stops the link writing offsets to the destination from just before promote, which matters in two cases. A group whose consumers commit on the destination while the link still syncs it would have those commits overwritten. And a group already active on the destination can leave a mirror topic stuck during promote. Turn it on if either could apply to your groups. The drain runs while the route is fenced, so it lengthens the pause clients see rather than shortening it.

<!-- prettier-ignore -->
!!! note "Pausing clears the link's group filters"
    Disabling `consumer.offset.sync.enable` clears `consumer.offset.group.filters` on the link, and the restore sets only `consumer.offset.sync.enable` back to your baseline. Because kcp keeps no state between runs, it has no record of the filters to put back. After a pause the link is `enabled` with an empty filter. This has no effect on the migration, because offset sync stops for every promoted mirror topic. It only matters if you reuse the link afterwards while it still has unpromoted mirror topics. In that case, set the filter again yourself.

For a manual alternative that needs no pause, the Cluster Linking migration guide stops the consumers on the source, waits at least one `consumer.offset.sync.ms`, removes their group from the filter, checks the offsets arrived, and restarts them on the destination.

If a run stops before promote, kcp removes the fence and restores offset sync, as for any static migration. See [If a run stops](../migrations/gateway/static-migration.md#if-a-run-stops).

## Groups on a dynamic route

A topic-based migration doesn't move groups. The route's coordination stays pinned to the source, the link's offset sync stays off, and each batch moves topics only. After a batch switches:

- The batch's consumers read the migrated topics from Confluent Cloud, through the route.
- Their groups still commit to the source, because the source coordinates every group on the route. Offsets are portable here because a mirror topic preserves the source's offsets, so a position committed on the source is the right position on the destination.

Nothing about consumer groups changes between batches, and the batch manifest has no consumer group settings. Three things behave differently while the route is dynamic:

- `kafka-consumer-groups` and other tools that call the group admin APIs don't work against the route. Point them at the source directly to inspect a group.
- Every member of a group has to use the route, as above.
- KIP-848 consumers can't connect at all. They come onto the route after the conversion.

The groups move when you convert.

## Conversion: groups move at the end

A conversion closes out a topic-based migration. kcp fences every topic on the route, checks that no client is still committing to the source directly, copies each in-scope group's committed offsets to the destination, and switches the route to a static route bound to the destination. The coordinator moves with the switch.

What is in scope, and what kcp checks, is covered in full in [Convert a dynamic route to static](../migrations/gateway/dynamic-to-static-conversion.md). For consumer groups:

- **Scope.** The cluster link. A group whose committed offsets are all on link topics is copied.
- **Refusals.** A group with a committed offset on a topic that isn't on the link, and an in-scope group with members on the destination. See [Refusals](../migrations/gateway/dynamic-to-static-conversion.md#refusals).
- **Direct commits.** kcp takes two snapshots of every source group's committed offsets, [`detectUnroutedCommitsDuration`](../../migration-assets/cutover/policies.md#policies-detectunroutedcommitsduration) apart. If any changed, a consumer commits to the source directly, and kcp unfences the route and stops. This check can't be turned off.
- **Offset sync.** kcp refuses if the offsets in the sync are past a destination partition's high-water mark. You don't turn link offset sync back on for a conversion.
- **Permissions.** kcp's destination credential needs `READ` on every group and topic to write their offsets. See [Permissions](../migrations/gateway/permissions.md#destination-kafka-cluster).

## What your consumers see

All three paths share one client experience at the switch.

- **A pause.** From the fence to the switch, consumers on the route fetch nothing and can't commit. A static migration pauses the whole route. A dynamic batch pauses only the batch's topics. A conversion pauses the whole route for about `detectUnroutedCommitsDuration` plus two Gateway reloads.
- **A rejoin.** After the switch the coordinator is on the destination, which doesn't know the consumers' members. Each consumer's first commit or heartbeat there fails, so it rejoins its group and resumes from the destination's committed offset. Consumers handle this in the client library. No configuration changes.
- **Possibly a re-read.** A consumer re-reads whatever it processed after the last committed offset the destination holds. On a static migration that is bounded by the sync interval and the drain. On a conversion it is up to one poll for a consumer that commits after every batch, and up to the backlog the fence built for one that auto-commits. Nothing is skipped. Design consumers to be idempotent, as you would for any at-least-once consumer.

## After the cutover

Confirm the groups, not only that consumers reconnected:

- Members rejoined their groups on Confluent Cloud, and each group is `Stable`.
- Each group's committed offsets on the destination match the source's last offsets for the same partitions, and the group's lag is falling.
- No group has members on both clusters.
- If you paused offset sync, the link is back at your [`consumerOffsetSync.baseline`](../../migration-assets/cutover/migrations.md#consumeroffsetsync-baseline).

Compare a group on both clusters:

```bash
kafka-consumer-groups --bootstrap-server <source-bootstrap> --command-config source.properties --describe --group <group>
kafka-consumer-groups --bootstrap-server <destination-bootstrap> --command-config destination.properties --describe --group <group>
```

Both commands work against Confluent Cloud, with a cluster API key in `destination.properties`.

Once promote starts (the switch, for a conversion), a migration only moves forward. kcp doesn't move clients back to the source after that point, so validate with `--dry-run` and `lag-check` before the window.

## Related content

- [Gateway migrations](../migrations/gateway/index.md)
- [Migrate with a static route](../migrations/gateway/static-migration.md)
- [Migrate with a dynamic route](../migrations/gateway/dynamic-migration.md)
- [Convert a dynamic route to static](../migrations/gateway/dynamic-to-static-conversion.md)
- [Migration manifest reference](../../migration-assets/cutover/overview.md)
- [Cluster Linking active-passive manual switchover](https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/dr-manual-switch-active-passive.html)
- [Migrate Topics Incrementally with Topic-Based Routing and Fencing](https://docs.confluent.io/private-cloud-gateway/current/gateway-topic-based-routing.html)
