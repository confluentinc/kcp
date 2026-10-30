# Migrate transactional workloads with a static route

Transactional workloads can only be migrated all at once. When a producer writes to several topics inside one Kafka transaction, those topics have to reach Confluent Cloud in the same cutover, or a single transaction ends up split across two clusters. Of the Gateway migration types, only a **static migration** gives you that: it fences the whole route, promotes every selected mirror topic, and flips the route to the destination in one step. A dynamic route doesn't support transactions at all.

This guide walks through a transactional migration end to end: finding the topics that must move together, checking that the route and cluster link are ready, running the cutover, and confirming that your transactional clients came across intact. It builds on [Migrate with a static route](../migrations/gateway/static-migration.md), which covers the mechanics of the cutover itself, and on [Discover transactional workloads](discovery.md), which covers discovery.

## Prerequisites and limitations

**Route and migration type**

- The route is **static**. A dynamic route doesn't support transactions, so don't use a [topic-based migration](../migrations/gateway/dynamic-migration.md) for these workloads, even for a subset of them. kcp can't tell that a topic is transactional and won't stop you, but the Gateway rejects requests from transactional producers on a dynamic route with an `INVALID_REQUEST` error, which is fatal to the client. The producers fail instead of silently losing their guarantees. See [Migrate Topics Incrementally with Topic-Based Routing and Fencing](https://docs.confluent.io/private-cloud-gateway/current/gateway-topic-based-routing.html).
- Everything in [Migrate with a static route](../migrations/gateway/static-migration.md#prerequisites-and-limitations) applies: Kubernetes and CFK, the destination staged on the Gateway CR with swap authentication, a cluster link with mirror topics, and credentials for each.

**Transaction groups**

- You know which topics are coupled by a shared transaction. Run [`txn-discovery`](discovery.md) against the source to get them. kcp doesn't read `migration.yaml` from that utility, and doesn't check that your topic selection keeps a transaction group intact. That check is yours to make.
- Every topic in every transaction group is a mirror topic on the cluster link and is selected in the manifest's `topicGroup`. Include consumed input topics of read-process-write groups as well as the topics written to.

!!! important "The whole route moves"
    A static migration flips the entire route, so every topic that clients reach through it moves to Confluent Cloud in the same cutover, not only the ones in your transaction groups. Topic selection decides which mirrors kcp promotes and lag-checks. A topic on the route that isn't mirrored and promoted is unreachable after the switch.

<!-- TODO: confirm what happens to a topic on the route that is NOT selected / not mirrored when the route flips. Static guide says "selection only decides which mirrors are promoted and lag-checked". Is the right guidance "select every topic on the route", or can an unselected topic stay on the source somehow? Wording of the callout above depends on this. -->

**Clients**

- Every producer and consumer connects through the Gateway, including the transactional ones. The fence only blocks traffic that goes through it.
- Transactional clients aren't only the ones you wrote. Kafka Streams applications and Kafka Connect workers running in exactly-once mode produce inside transactions too, so count them when you check the route.
- Transactional producers can reach the destination with the credentials and `transactional.id` they use today. Nothing about the `transactional.id` needs to change.
- Consumers of transactional topics use `isolation.level=read_committed`, as they do today. They keep it after the switch.
- KIP-848 consumers (`group.protocol=consumer`) work on a static route.

<!-- TODO: does the destination's transaction coordinator need anything special for a transactional.id that has never been used on that cluster (ACLs: TransactionalId WRITE/DESCRIBE on Confluent Cloud)? Confirm and list the ACLs/role bindings clients need on the destination before cutover. -->

**Cluster link**

- Consumer offset sync is **on** for the link, so committed offsets reach the destination ahead of the switch. kcp doesn't check this for a static route.
- Mirror lag is at or below [`lagThreshold`](../../migration-assets/cutover/policies.md#policies-lagthreshold) before kcp fences the route. Leave it at the default of `0` for transactional topics.

<!-- TODO: cluster links mirror data byte for byte. Confirm the guarantees we can state about transaction markers (commit/abort control records) and aborted-transaction data on mirror topics, so read_committed consumers see the same result on the destination. Link to the Cluster Linking doc that says so. -->

## What a cutover does to transactions

The cutover follows the [stages in the static guide](../migrations/gateway/static-migration.md#how-the-cutover-works): check lag, fence, promote, switch. Three things matter specifically for transactional clients.

**Transactions are in flight at the fence.** When the fence goes up, clients get retriable errors and buffer. A transaction that was open at that moment can't complete through the Gateway.

<!-- TODO: what actually happens to an open transaction when the fence goes up? Options: (a) the producer's EndTxn is fenced too and the transaction hangs until transaction.timeout.ms, then the coordinator aborts it; (b) EndTxn passes through and the transaction completes on the source; (c) something else. Which is it, and does the mirror receive the abort marker before promote? This decides what we tell users to do about long transaction timeouts. -->

**Transaction state doesn't move with the topics.** The source's `__transaction_state` stays on the source. After the switch, the destination's transaction coordinator knows nothing about your `transactional.id` values, so producers re-initialize their transactions on the destination.

<!-- TODO: confirm. Producers call InitProducerId against the destination after reconnecting; is anything user-visible (ProducerFencedException, a one-off error to handle) or does the client library absorb it? Any guidance for Kafka Streams apps (EOS v2) and for hand-written transactional clients? -->

**Consumers read committed data only.** Mirror topics contain the transaction markers from the source, so a `read_committed` consumer on the destination skips aborted data and stops at the last stable offset, as it did before.

<!-- TODO: same confirmation as the cluster link item above. -->

## Set up

### 1. Find the transaction groups

Run `txn-discovery` for long enough to cover your batch jobs, as described in [Choose an observation window](discovery.md#choose-an-observation-window). Keep `migration.yaml`: its groups are the topics your manifest must select.

### 2. Check the route and cluster link

Stage the destination on the Gateway CR as described in [Stage the destination on the Gateway](../migrations/gateway/static-migration.md#1-stage-the-destination-on-the-gateway), and confirm that each topic in your transaction groups is a mirror topic on the link. kcp never creates either; use [`kcp migration resources create`](../../migration-assets/README.md) to set them up.

### 3. Write the migration manifest

Declare a [`spec.staticMigration`](../../migration-assets/cutover/overview.md#declaring-the-migration-type) and put the topics from every transaction group into its `topicGroup`. The rest of the manifest is the same as in [Migrate with a static route](../migrations/gateway/static-migration.md#2-write-the-migration-manifest).

```yaml
  staticMigration:
    route:
      name: migration-route
      targetStreamingDomain: confluent-cloud
    topicGroup:
      - topics:
          # group-1: checkout-service, fulfilment-service
          - orders
          - order-events
          - shipments
          # group-2: payments-processor (read-process-write)
          - payment-requests
          - payments-approved
          - payments-ledger
          # individual topic, moves in the same cutover because the route does
          - audit-trail
    consumerOffsetSync:
      pause: true
      baseline: enabled
    policies:
      lagThreshold: 0
      rolloutTimeout: 10m
      detectUnroutedProducersDuration: 30s
```

Two policies deserve a second look for transactional topics:

- [`detectUnroutedProducersDuration`](../../migration-assets/cutover/policies.md#policies-detectunroutedproducersduration) is **off by default**. Turn it on. A transactional producer that bypasses the Gateway keeps committing to the source after the fence, and its transactions are lost to the destination.
- [`lagThreshold`](../../migration-assets/cutover/policies.md#policies-lagthreshold) stays at `0`.

<!-- TODO: should we recommend a minimum detectUnroutedProducersDuration relative to transaction.timeout.ms or the typical transaction duration, so the check can observe a bypassing producer between commits? -->

### 4. Dry run

```bash
kcp migration cutover execute --migration-yaml migration.yaml --dry-run
```

The dry run validates the plan and changes nothing. Run it until it passes. Then compare the topics it lists against your transaction groups once more: kcp checks that the topics exist and are mirrored, not that the set is complete.

### 5. Watch the lag

```bash
kcp migration cutover lag-check --migration-yaml migration.yaml
```

Run it ahead of the window and again just before. Mirroring is asynchronous, so lag exists until you stop producing; the fence is what brings it to zero.

## Run the cutover

Schedule a window. Clients are paused from the fence until the switch.

<!-- TODO: how long should the window be for transactional workloads, given the open-transaction question above? -->

```bash
kcp migration cutover execute --migration-yaml migration.yaml
```

Stopping and resuming behaves as it does for any [static migration](../migrations/gateway/static-migration.md#if-a-run-stops): before promote the fence is removed and traffic returns to the source, and from promote onwards the fence stays and a re-run rolls forward.

## After the cutover

Confirm the transactional clients, not only that they reconnected:

- Producers are committing transactions on Confluent Cloud, and no transaction errors repeat in their logs.
- `read_committed` consumers are reading new data and their lag is falling.
- Each transaction group is consistent: a record in one topic of a group has its counterpart in the others.
- If you paused offset sync, the link is back at your [`consumerOffsetSync.baseline`](../../migration-assets/cutover/migrations.md#consumeroffsetsync-baseline).

<!-- TODO: concrete verification commands. e.g. a kafka-console-consumer --isolation-level read_committed check against the destination, or a ccloud/kcp command to list open transactions. Want me to capture something from the e2e rig? -->

<!-- TODO: failback. The Confluent active-passive guide covers reverse-and-start / truncate-and-restore. Is there a supported way back after the switch for transactional topics (reverse link), or is the answer "none"? -->

## Related content

- [Gateway migrations](../migrations/gateway/index.md)
- [Migrate with a static route](../migrations/gateway/static-migration.md)
- [Discover transactional workloads](discovery.md)
- [Migration manifest reference](../../migration-assets/cutover/overview.md)
- [Cluster Linking active-passive manual switchover](https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/dr-manual-switch-active-passive.html)
