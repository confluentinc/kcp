# Convert a Dynamic Gateway Route to Static

A conversion closes out a topic-based migration on Confluent Gateway. It replaces the dynamic route with a static route bound to the destination, and copies each consumer group's committed offsets to the destination so group coordination moves too.

When every topic you're migrating has moved to Confluent Cloud, the route is still dynamic: it binds both clusters, and consumer group coordination is still pinned to the source. After the conversion, clients reconnect to the same Gateway address, and the source is no longer on the route.

This guide walks through the prerequisites, the migration manifest, and a conversion run. For how conversions fit with the other migration types, see [Gateway migrations](overview.md).

<!-- prettier-ignore -->
!!! warning "Early Access"
    At the time of writing, dynamic routing is an Early Access feature of Confluent Private Cloud Gateway 1.4, intended for evaluation and non-production testing. Its configuration schema might change before general availability. A conversion closes a [dynamic migration](dynamic-migration.md), so it needs a dynamic route.

## Prerequisites and limitations

**Gateway**

- The Gateway runs on Kubernetes and is managed by Confluent for Kubernetes (CFK). kcp reads and patches the Gateway custom resource (CR), so a Gateway started any other way can't be converted.
- The route to convert is dynamic: it binds exactly two streaming domains, the source and the destination, and sets `rules.routing.coordination.group` to the source.
- The destination is one of the two bound domains, its binding carries a `bootstrapServerId`, and the route already carries `security.cluster.<domain>` authentication for it. This is already true if you ran a [dynamic migration](dynamic-migration.md) on the route.
- The route carries no fence kcp didn't write, and no route-level fence. A fence left by an unfinished dynamic migration batch is the usual cause of a refusal. See [Refusals](#refusals).
- Gateway 1.4.0 and later, and CFK 3.4.0 and later. See [Version requirements](overview.md#version-requirements).

**Cluster link**

- The cluster link that the dynamic migration used still exists, and every mirror topic on it is promoted (`STOPPED`). The link is the conversion's scope: its mirror topics are the topics in scope, and the consumer groups that commit only on them are in scope too. A conversion selects no topics.
- Each link topic has the same name on both clusters, so the link has no `cluster.link.prefix`, and the same partition count on both clusters.
- Every link topic is routed to the destination by the dynamic route. Finish any unfinished batch first.
- Keep the link until the conversion is done. Don't delete it after the last batch: its mirror topics are kcp's only record of which topics were migrated, and kcp works out the in-scope consumer groups from those topics. Without the link, kcp can't read the scope and the run fails with an error reading the link's mirror topics.
- Keep the source topics too. Deleting a source topic purges its consumer groups' committed offsets, so the conversion has nothing to copy for it, and the source can no longer accept offset commits on it.
- Consumer offset sync is off for the link, as it was during the dynamic migration. kcp refuses otherwise. The conversion copies the offsets itself, so you don't turn sync back on.

- The route has one cluster link: every batch of the dynamic migration used it. One link maps to one route and one conversion. The manifest names a single link and the conversion's scope is that link, so topics migrated over any other link are outside it, and a consumer group that commits on one of them refuses the conversion. kcp doesn't check that the route's batches all used the same link, so this is yours to keep to.

**Consumer groups**

- A source consumer group whose committed offsets are all on link topics is in scope, and its offsets are copied to the destination.
- A group with a committed offset on a topic that isn't on the link refuses the conversion. After the switch that topic isn't reachable through the route, or the group belongs to a client outside it.
- An in-scope group with live members on the destination refuses the conversion. A group that exists on the destination with no members gets a warning, because its offsets are overwritten.

**Clients**

- Every producer and consumer connects through the Gateway. The conversion's fence only blocks traffic that goes through it. kcp checks for consumers that commit to the source directly, but not for producers.
- Producers use `acks=1` or `acks=all`, and a `delivery.timeout.ms` longer than the pause (see [What clients see](#what-clients-see)). With `acks=0` they never see the fence's errors and their records are dropped.
- KIP-848 consumers (`group.protocol=consumer`) can't use a dynamic route. They work on the static route after the conversion finishes.

**Limitations**

- The whole route pauses, not a batch. Every topic on the route is blocked from the fence to the switch.
- The point of no return is the switch. A failure before it removes the fence and leaves the route as it was. After the switch, kcp can't roll the route back to dynamic.
- Ctrl+C leaves the fence in place. Re-run `execute` to continue.
- A conversion needs at least one promoted topic on the link. It closes a topic-based migration rather than replacing one.

**Access**

- Credentials for the source, the destination Apache Kafka® listener, and the cluster link REST API, each as a [credentials file](../../../migration-assets/cutover/credentials.md).
- The source and destination credentials must be able to list every consumer group and describe every topic. A credential that sees only part of a cluster would hide a group or topic from kcp's checks. The destination credential must also be able to write consumer group offsets (`READ` on the groups and the topics). On an MSK IAM source, that means `kafka-cluster:DescribeGroup` and `kafka-cluster:DescribeTopic` on every group and topic resource.
- A Kubernetes identity that can read and patch the Gateway CR and read its Deployment. With hot reload on, it also needs `list` on pods, `get` on `pods/proxy`, and a cluster-scoped `get` on the Gateway custom resource definition (CRD). See [Permissions](permissions.md) for the full Role and the Kafka grants.

## How a conversion works

kcp keeps no state between runs. Each run reconciles the manifest against the live Gateway, both clusters, and the cluster link, and continues from wherever they already are. For a conversion the stages are:

1. **Fence.** kcp adds a fence that blocks every topic on the route (`topicPatterns: ['.*']`, `blocked: true`) at the head of the route's `rules.fencing`, and waits for every Gateway pod to apply it. Clients get errors that they retry, and they buffer in the meantime.
2. **Verify.** On data read after the fence, kcp re-runs the checks that decide whether the route can be converted, confirms its own fence is still on the route, and re-runs the credential, cluster link, and consumer group checks. It then takes two snapshots of every source group's committed offsets, [`detectUnroutedCommitsDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedcommitsduration) apart. If any offset changed, a client is committing to the source directly, and kcp aborts and unfences the route.
3. **Sync offsets.** kcp writes each in-scope group's committed offsets, with their metadata, to the destination. It refuses the whole sync if an offset is past its destination partition's high-water mark.
4. **Switch.** kcp replaces the dynamic route with a static route bound to the destination, with no `rules` (so no fence). `security` and the rest of the route are kept. This is the point of no return.

A conversion has no promote stage: every topic was promoted in the dynamic migration, and a conversion refuses to start until they are.

As in a migration, kcp works out how the Gateway applies a new configuration, in place (hot reload) or by rolling its pods, and waits for every pod to confirm each change. [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout) and [`hotReloadTimeout`](../../../migration-assets/cutover/policies.md#policies-hotreloadtimeout) bound those waits.

A failure before the switch removes the fence and leaves the route as it was. Offsets already written to the destination have no effect until the switch.

## What clients see

- **A pause.** Every topic on the route is blocked from the fence to the switch: about `detectUnroutedCommitsDuration` (30 seconds by default) plus two Gateway reloads. Producers retry. Consumers fetch nothing and can't commit.
- **A rejoin, and possibly a re-read.** After the switch, the consumers' group coordinator is on the destination. Each consumer's first commit or heartbeat there fails, because the destination doesn't know its member, so it rejoins its group and resumes from the copied offset. It might read again what it fetched between the switch and that first failed commit or heartbeat, and what it consumed just before the fence but couldn't commit. For a consumer that commits after every batch, that is up to one poll (`max.poll.records`). For one that auto-commits, it is up to the backlog the fence built. This is expected at-least-once behavior. Nothing is skipped.

## Set up

### 1. Finish the dynamic migration

Before you write the manifest:

- Every batch has completed, and every mirror topic on the link is `STOPPED`.
- No fence is left on the route's `rules.fencing` from an interrupted batch or other reason.
- Every application that uses the route is ready for the pause, and its producers meet the [client requirements](#prerequisites-and-limitations).

### 2. Write the migration manifest

The manifest declares the conversion with [`spec.dynamicToStaticConversion`](../../../migration-assets/cutover/route-conversion.md). Everything outside that section is the same as in the [dynamic migration](dynamic-migration.md#3-write-the-migration-manifest), so you can start from the last batch's manifest. Every credentials field is a path to a credentials file, and the manifest holds no secrets. Paths resolve relative to the working directory you run kcp from.

```yaml
apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration

metadata:
  name: msk-prod-to-cc-convert

spec:
  source:
    type: msk # msk | apache-kafka
    bootstrapServers:
      - b-1.msk-prod.abc123.c2.kafka.us-east-1.amazonaws.com:9096
    # Must be able to list every consumer group and describe every topic.
    credentials: /etc/kcp/source-creds.yaml

  target:
    type: confluent-cloud # confluent-cloud | confluent-platform
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      # Must also write consumer group offsets: the conversion copies the
      # source's committed offsets here.
      clusterCredentials: /etc/kcp/dest-kafka-creds.yaml

  clusterLink:
    name: msk-to-cc # the link the dynamic migration used
    linkCredentials: /etc/kcp/link-creds.yaml

  gateway:
    namespace: confluent
    cr-name: gateway-initial

  dynamicToStaticConversion:
    route:
      name: migration-route # the dynamic route the migration used
      targetStreamingDomain: confluent-cloud # one of the two domains the route binds
    # No topicGroup: a conversion covers every mirror topic on the link.
    policies:
      detectUnroutedCommitsDuration: 30s # 0s uses the built-in 30s; never skipped; minimum 10s
      offsetSyncConcurrency: 0 # workers reading and writing offsets; 0 uses the built-in 8
      rolloutTimeout: 10m
      hotReloadTimeout: 90s
```

Notes on the choices above:

- A conversion manifest has no `topicGroup`, `consumerOffsetSync` block, `lagThreshold`, or `promoteBatchSize`. A conversion's scope is the cluster link, and it has no promote stage.
- [`detectUnroutedCommitsDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedcommitsduration) is off by default, like its producer counterpart in a migration. When set, it is also how long every topic on the route is blocked, so raising it lengthens the pause.
- [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout) defaults to no deadline, so a rollout that never converges waits indefinitely with traffic already fenced. Set one.
- [`offsetSyncConcurrency`](../../../migration-assets/cutover/policies.md#policies-offsetsyncconcurrency) matters only when you have many consumer groups. Each worker holds its own broker connection.

The complete annotated manifest is [`gateway-route-conversion.yaml`](../../../gateway-examples/gateway-route-conversion.yaml). Every field, validation rule, and policy default is in the [migration manifest reference](../../../migration-assets/cutover/overview.md).

### 3. Dry run

```bash
kcp migration cutover execute --migration-yaml migration.yaml --dry-run
```

A dry run validates the manifest and credentials, then reads the live Gateway CR, both clusters, and the cluster link, and prints the plan: the route checks, the link's topics, and the consumer groups in scope. It changes nothing and exits non-zero if the plan is refused. Run it until it passes.

#### Refusals

A run is refused, with nothing changed, when any of the following is true:

- The route isn't dynamic, doesn't bind exactly two domains, doesn't bind the target, or doesn't pin `coordination.group` to the source.
- Consumer offset sync is still enabled on the cluster link.
- A link topic is still `ACTIVE`, is still being promoted, is routed to the source, is named differently on the destination, or has a different partition count on the destination, or no topic on the link is promoted at all.
- A consumer group commits on a topic that isn't on the link, or an in-scope group has members on the destination.
- A credential can't list every group, describe every topic, or write offsets.
- The route has a fence kcp didn't write, or a route-level fence.

The [conversion reference](../../../migration-assets/cutover/route-conversion.md#refusals-and-fixes) lists the fix for each. Two need care:

- **A fence kcp didn't write.** Find out what it is for before you remove it. If it belongs to an unfinished dynamic migration batch, finish that batch instead: removing its fence would send producers back to a source topic the link no longer mirrors. Otherwise remove it, because the conversion drops it.
- **A group commits on a topic that isn't on the link.** Migrate the topic, delete the group's stale offsets on it or the group itself, or stop the application that owns it.

kcp also warns, without refusing, about source topics that aren't on the link and that no group commits on: after the switch, the route sends their traffic to the destination.

## Run the conversion

Schedule a window. Every client on the route is paused from the fence until the switch.

```bash
kcp migration cutover execute --migration-yaml migration.yaml
```

You can override any policy for a single run with its flag, for example `--detect-unrouted-commits-duration 60s`. See the [command reference](../../../command-reference/migration/cutover/execute.md).

When it finishes, kcp prints `Route conversion completed: <metadata.name>`. Re-running a finished conversion reconciles to nothing to do.

### If a run stops

| Where it stopped                                                  | What happened                                                                                                    | What to do                                                                                  |
| ----------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| Before the fence (refused plan)                                   | Nothing was changed.                                                                                             | Fix the cause and re-run.                                                                   |
| After the fence, before the switch (for example unrouted commits) | kcp removes the fence and leaves the route as it was. Offsets already written to the destination have no effect. | Fix the cause (for example send the client through the Gateway) and re-run.                 |
| Switch failed, static route never reached the Gateway CR          | The fence stays.                                                                                                 | Fix the cause and re-run `execute` to finish.                                               |
| Switch failed, route reached the CR but wasn't confirmed          | The new route is in the CR, but kcp couldn't confirm every pod applied it.                                       | Check the Gateway (operator acceptance, pods). Don't re-run: it would report nothing to do. |
| Ctrl+C or lost connection                                         | The fence stays.                                                                                                 | Re-run `execute` with the same manifest.                                                    |

kcp doesn't clean up when it is stopped (Ctrl+C, `SIGTERM`, or a crash), so a killed run leaves the fence up and the route keeps blocking every topic until you re-run the same manifest. If a re-run is refused while kcp's fence is on the route, kcp warns you. Fix the refusal and re-run, or remove kcp's entry from `rules.fencing` by hand.

## After the conversion

After a conversion, the route is static and bound to the destination, with no `rules`:

```yaml
routes:
  - name: migration-route
    endpoint: bootstrap.gw.example.local:9595
    brokerIdentificationStrategy:
      type: host
      pattern: "broker$(nodeId).gw.example.local"
    mode: static
    # Bound to the destination only. streamingDomains and rules are gone.
    streamingDomain:
      name: confluent-cloud
      bootstrapServerId: SASL_PLAIN
    security:
      client:
        authentication:
          type: none
        tls:
          secretRef: client-tls
      cluster:
        # Kept by the conversion. Unused now that the route doesn't bind the source.
        source-kafka-cluster:
          auth: passthrough
        confluent-cloud:
          auth: swap
          secretStore: file-store
          authentication:
            type: plain
            jaasConfigPassThrough:
              secretRef: plain-jaas
```

kcp leaves the source's `security.cluster` entry on the route and the source's entry in the CR's `spec.streamingDomains`. Neither carries traffic. Remove them when you decommission the source, and keep the top-level domain while any other route still uses it.

- Confirm clients rejoined their groups on Confluent Cloud, are producing and consuming, and resumed from the copied offsets. To compare a group on both clusters, see [Migrate consumer groups](../../consumer-groups/migration.md#after-the-cutover).
- Re-running `kcp migration cutover execute --dry-run` reports nothing to do.
- Consumer group coordination is now on the destination. KIP-848 consumers (`group.protocol=consumer`) can connect through the route. Switching a consumer to the new protocol is a client change that kcp doesn't make.
- Transactional producers, like KIP-848 consumers, can use the route now that it is static.
- The source cluster is no longer on the route. Decommission it on your own schedule, after you are sure nothing connects to it directly.

After the conversion finishes, kcp no longer needs the cluster link. Delete it when you no longer need it.

After the switch, a conversion only moves forward. kcp doesn't move clients back to the source after that point, so validate with `--dry-run` before the window.

## Related content

- [Gateway migrations](overview.md)
- [Migrate with a dynamic route](dynamic-migration.md)
- [Migrate with a static route](static-migration.md)
- [Route conversion settings in the migration manifest](../../../migration-assets/cutover/route-conversion.md)
- [Migration manifest reference](../../../migration-assets/cutover/overview.md)
- [`kcp migration cutover execute`](../../../command-reference/migration/cutover/execute.md)
- [Discover transactional workloads](../../transactions/discovery.md)
