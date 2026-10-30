# Gateway migrations

`kcp migration cutover execute` cuts clients over from your source Kafka cluster to Confluent Cloud without asking them to change their configuration. It does this through the Confluent Gateway: your applications connect to the Gateway, and a **route** on the Gateway decides which cluster their traffic reaches. A cutover changes where the route points, and the clients follow.

A route has a **mode**, and the mode decides which kind of migration you can run:

| Route mode | Migration type            | Cuts over                    | Consumer group coordination |
| ---------- | ------------------------- | ---------------------------- | --------------------------- |
| `static`   | All-at-once (AAO)         | Every topic on the route, together | Moves with the cutover |
| `dynamic`  | Topic-based (TBM)         | Selected topics, in batches  | Stays on the source until the route is converted |

A third workflow, the **dynamic-to-static conversion**, closes out a topic-based migration by changing the route's mode once every topic has moved.

!!! important "Kubernetes and CFK only"
    kcp orchestrates Gateway migrations only when the Gateway runs on Kubernetes and is managed by Confluent for Kubernetes (CFK). kcp reads and updates the Gateway custom resource through the Kubernetes API, so a Gateway started directly from its Docker image, or by any other means, can't be migrated with `kcp migration cutover execute`. The Gateway images are published on Docker Hub as [`cpc-gateway`](https://hub.docker.com/r/confluentinc/cpc-gateway), [`confluent-gateway`](https://hub.docker.com/r/confluentinc/confluent-gateway) and [`confluent-gateway-for-cloud`](https://hub.docker.com/r/confluentinc/confluent-gateway-for-cloud).

This page introduces the three. Each gets a detailed guide of its own; the field-by-field manifest syntax is in the [migration manifest reference](../../../migration-assets/cutover/overview.md).

!!! note "How kcp picks the workflow"
    You declare the workflow in the migration manifest by including exactly one of `spec.staticMigration`, `spec.dynamicMigration` or `spec.dynamicToStaticConversion`. kcp reads the live Gateway resource on every run and refuses to continue if the route's mode doesn't match what you declared. A static migration can only run on a static route, and a topic-based migration only on a dynamic one.

## How every cutover works

All three workflows follow the same shape. kcp keeps no state between runs: every run reconciles the manifest against the live Gateway, both clusters and the cluster link, and continues from wherever the live state already is. If a run is interrupted, re-run the same command.

Resuming moves forward, and rolling back is only possible early on. Until the **point of no return**, a failure removes the fence and traffic returns to the source. After it, kcp never rolls back, and a failure leaves the fence in place until a re-run completes the migration. The point of no return is the start of the promote step for static and dynamic migrations, and the switch for a conversion.

1. **Fence.** The Gateway starts rejecting client traffic for the topics being moved, so nothing is written to the source while the destination catches up. Clients see retriable errors and buffer.
2. **Promote.** kcp promotes the mirror topics on the cluster link once they have caught up with the source.
3. **Switch.** kcp updates the route to point at the destination and removes the fence. Clients reconnect to the same Gateway address and land on Confluent Cloud.

Run `kcp migration cutover execute --dry-run` at any point to see the plan and any refusals without changing anything.

## Version requirements

- **Gateway 1.4.0 or later.** kcp v0.11.0 and later support Gateway 1.4.0 onwards. For a Gateway on 1.3.x, upgrade the Gateway first or use kcp v0.10.x.
- **CFK 3.4.0 or later.** Dynamic routing is Early Access in Gateway 1.4 and CFK 3.4. See the [CFK release notes](https://docs.confluent.io/operator/current/release-notes.html).
- **Hot reload.** If the Gateway CR sets `spec.hotReload.enabled: true`, the operator's Gateway CRD must also declare `spec.configId`. kcp checks this before the fence. If the field is missing, kcp refuses and names the ways to proceed, one of which is setting `spec.hotReload.enabled: false` for the duration of the migration. See [Verifying the Gateway applied a change](limitations.md#verifying-the-gateway-applied-a-change).

## Static routes (all-at-once)

A static route binds all of its traffic to one streaming domain. A static migration fences the route as a whole, promotes every selected mirror topic, and flips the route to the destination in a single cutover.

**Use it when**

- The workload can tolerate one short pause, and you would rather have one cutover than many.
- Topics are tightly coupled (for example [transactional workloads](../../transactions/discovery.md)) and must move together.
- You want consumer group coordination to move with the cutover, with the cluster link syncing committed offsets across.

**Things to know**

- The whole route pauses, not just the topics you selected. Topic selection only decides which mirrors are promoted and lag-checked.
- The route must already carry authentication for the destination, staged ahead of time, so the cutover itself needs no secret or auth change.
- Consumer offset sync should be **on** for the cluster link. kcp does not check this for a static route.
- The point of no return is the start of the promote step. Once it starts, kcp never rolls back: a failure leaves the fence in place and the next run continues forward.

## Dynamic routes (topic-based)

A dynamic route binds two streaming domains, the source and the destination, and sends each topic to one or the other according to routing rules. A topic-based migration moves a **batch** of topics at a time: it fences only those topics, promotes their mirrors at zero lag, and updates the routing so those topics now go to Confluent Cloud. Everything else keeps flowing to the source.

The Gateway side of this, including the routing rules and fencing, is documented in [Migrate Topics Incrementally with Topic-Based Routing and Fencing](https://docs.confluent.io/private-cloud-gateway/current/gateway-topic-based-routing.html). Read it before planning a topic-based migration.

!!! warning "Early Access"
    At the time of writing, dynamic routing is an Early Access feature of Confluent Private Cloud Gateway 1.4, intended for evaluation and non-production testing. Its configuration schema may change before general availability. Fencing itself is generally available.

**Use it when**

- You want to migrate incrementally, application by application or domain by domain, with time to validate between batches.
- A single whole-route pause is not acceptable.
- You want a smaller blast radius for each step.

**Things to know**

- Each batch is its own manifest, and batches must contain disjoint topics.
- Resume an interrupted batch with a manifest that resolves to the same topics. If the set changes between runs (a topic added or removed, or a pattern that now matches a new topic), kcp treats the earlier fence as operator-authored and leaves it on the route, so those topics stay blocked after the migration completes.
- The route must bind exactly two streaming domains, with consumer group coordination pinned to the source.
- The cluster link's consumer offset sync must be **off**, and kcp refuses to run otherwise. Consumer groups stay coordinated by the source cluster while the route is dynamic.
- **Transactions and share groups are not supported** on a dynamic route. Migrate transactional workloads with a static migration.
- Consumers must use the classic group protocol. KIP-848 consumers (`group.protocol=consumer`) can't use a dynamic route at all, for the whole life of a topic-based migration. They work on a static route, so use a static migration if you have them.
- Only the `host` broker identification strategy is supported, which requires TLS configuration on the route.
- SCRAM isn't supported for client or cluster authentication on a dynamic route. Use `PLAIN` or `OAUTHBEARER`.
- Topic auto-creation isn't supported, and some consumer group admin APIs are unavailable, so tools such as `kafka-consumer-groups` don't work against a dynamic route.
- Fencing pauses produce or consume traffic only. Producers with `acks=0` can't see the errors, so their records for fenced topics are silently dropped; use `acks=1` or `acks=all`.
- A fence can't stop clients connected to the source directly. The check for producers that bypass the Gateway (`detectUnroutedProducersDuration`) is off by default in kcp.
- All members of a consumer group must connect through the route, or the group splits across two coordinators.
- A topic-based migration leaves the route dynamic. Finish the job with a conversion.
- Dynamic routes need Gateway 1.4.0 and CFK 3.4.0 or later. See [Version requirements](#version-requirements).

## Converting a dynamic route to static

Once every topic on the cluster link has been migrated, the route is still dynamic, and consumer groups are still coordinated by the source cluster. A **conversion** changes the route's mode from dynamic to static, bound to the destination, and copies each consumer group's committed offsets across so that group coordination moves too. Topics don't change; the route's mode does.

**Use it when**

- A topic-based migration has finished and you are ready to retire the source for these clients.
- Your clients include KIP-848 consumers, which can't connect through a dynamic route and so only come onto the migrated route once it is static.
- You want the route back in its simplest form.

**Things to know**

- A conversion selects no topics. The cluster link is its scope: every mirror topic on it must already be promoted, with the same name and partition count on both clusters.
- It briefly fences every topic on the route. kcp watches committed offsets on the source for a window (30 seconds by default) to catch consumers that bypass the Gateway, and fails if it sees any.
- Consumers rejoin their group on the destination after the switch and resume from the copied offset. They may re-read a small amount of data; nothing is skipped.
- The switch is the point of no return. A failure before it removes the fence and leaves the route as it was.
- Conversion is refused for a cluster link that uses `cluster.link.prefix`, and for consumer groups that commit offsets on topics outside the link.

## Choosing between them

| If you need…                                              | Choose                              |
| --------------------------------------------------------- | ----------------------------------- |
| One cutover and one pause for everything on the route     | Static migration                    |
| To move topics in stages and validate in between          | Dynamic migration, then conversion  |
| Topics that must move together to preserve transactions   | Static (dynamic routes don't support transactions) |
| KIP-848 consumers                                         | Static migration (dynamic routes don't support them) |
| The smallest pause per step                               | Dynamic migration                   |

kcp changes a route's mode in one direction only: dynamic to static, with a [conversion](dynamic-to-static-conversion.md). It doesn't make a static route dynamic. To run a topic-based migration on a route that is static today, change the route yourself before the first batch: bind both domains, pin `coordination.group` to the source, and set `routing.default` to the source. Two things to check first:

- A dynamic route needs the `host` broker identification strategy and client TLS. If the static route uses something else, the change affects how clients connect, so plan it as a separate change ahead of the migration.
- KIP-848 consumers and transactional producers can't use a dynamic route. If the route has any, use a static migration instead.

## Next steps

- [Migrate with a static route](static-migration.md): the step-by-step guide for an all-at-once migration.
- [Migrate with a dynamic route](dynamic-migration.md): the step-by-step guide for a topic-based migration.
- [Convert a dynamic route to static](dynamic-to-static-conversion.md): the step-by-step guide for closing out a topic-based migration.
- [Migrate Topics Incrementally with Topic-Based Routing and Fencing](https://docs.confluent.io/private-cloud-gateway/current/gateway-topic-based-routing.html): the Gateway's own documentation for dynamic routes and fencing.
- [Permissions](permissions.md): what each identity needs, and what you see when a grant is missing.
- [Idempotency and resumability](idempotency.md): what a re-run does and doesn't do.
- [kcp limitations](limitations.md): limits of kcp itself, beyond the Gateway's.
- [Migration manifest reference](../../../migration-assets/cutover/overview.md): every manifest field, with annotated examples for [static](../../../gateway-examples/gateway-static-migration.yaml), [dynamic](../../../gateway-examples/gateway-dynamic-migration.yaml) and [conversion](../../../gateway-examples/gateway-route-conversion.yaml) manifests.
- [Discover transactional workloads](../../transactions/discovery.md): find the topics that must move together before you plan batches.
