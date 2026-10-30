# Migrate With a Dynamic Gateway Route

A dynamic migration moves topics to Confluent Cloud in batches, with no client configuration changes.

Your applications already connect to the Confluent Gateway. A dynamic route binds both the source and the destination and sends each topic to one or the other according to routing rules. Cutting over a batch means fencing only those topics, promoting their mirror topics on the cluster link, and adding a routing rule that sends them to the destination. Clients reconnect to the same Gateway address, and everything outside the batch keeps flowing to the source.

This guide walks through the prerequisites, the migration manifest, and a batch run. For how dynamic migrations compare with static ones, see [Gateway migrations](index.md).

<!-- prettier-ignore -->
!!! warning "Early Access"
    At the time of writing, dynamic routing is an Early Access feature of Confluent Private Cloud Gateway 1.4, intended for evaluation and non-production testing. Its configuration schema might change before general availability. Before you plan a migration, read [Migrate Topics Incrementally with Topic-Based Routing and Fencing](https://docs.confluent.io/private-cloud-gateway/current/gateway-topic-based-routing.html).

## Prerequisites and limitations

**Gateway**

- The Gateway runs on Kubernetes and is managed by Confluent for Kubernetes (CFK). kcp reads and patches the Gateway custom resource (CR), so a Gateway started any other way can't be migrated.
- The route to migrate is dynamic: it binds exactly two streaming domains, the source and the destination, and sets `rules.routing.coordination.group` to the source.
- The route uses the `host` broker identification strategy, which requires TLS between clients and the Gateway. kcp doesn't check this. The Gateway rejects a dynamic route that doesn't.
- The destination is one of the two bound domains, and the route already carries `security.cluster.<domain>` authentication for both. `PLAIN` and `OAUTHBEARER` are supported on a dynamic route, but SCRAM is not.
- kcp adds and removes its own fence and routing rules in `rules`. Any fence or routing rule you authored stays in place. See [If a run stops](#if-a-run-stops).
- Gateway 1.4.0 or later, and CFK 3.4.0 or later. See [Version requirements](index.md#version-requirements).

**Cluster link**

- A cluster link from the source to the destination already exists, and every topic you select is a mirror topic on it. `kcp migration cutover` never creates either. To set them up, use [`kcp migration resources create`](../../../migration-assets/README.md).
- A dynamic route uses one cluster link. Every batch on the route migrates over that link, and the later [conversion](dynamic-to-static-conversion.md) uses it to decide which topics and consumer groups to move. `spec.clusterLink` takes a single link, and kcp doesn't check whether another manifest names a different link for the same route. Don't spread one route's batches across several links: topics on the others fall outside the conversion's scope, and a consumer group that commits on one of them refuses the conversion. Add every batch's topics to the same link instead.
- If you plan to [convert the route to static](dynamic-to-static-conversion.md) afterward, keep the cluster link and the source topics until the conversion is done, and create the link without a `cluster.link.prefix`. The conversion reads the link to find what to move, copies consumer group offsets from the source topics, and refuses a link whose mirrors are named differently on the destination.
- Consumer offset sync must be off for the link. kcp refuses to run otherwise. Consumer groups stay coordinated by the source while the route is dynamic, so the link has no offsets to sync for them. `kcp migration resources create` turns offset sync on by default. To create the link with sync off, set [`consumerOffsetSync.enable: false`](../../../migration-assets/manifest-reference.md#consumeroffsetsync) in its manifest. For an existing link, set `consumer.offset.sync.enable=false` in the link's configuration.
- Mirror lag must be at or below [`lagThreshold`](../../../migration-assets/cutover/policies.md#policies-lagthreshold) (default `0`, fully caught up) before kcp fences the batch. After the fence, kcp waits for each mirror to reach exactly zero lag before it promotes.

**Clients**

- Every producer and consumer connects through the Gateway. The fence only blocks traffic that goes through it, so a producer writing to the source directly keeps writing.
- Producers use `acks=1` or `acks=all`. Fenced clients get an error they can retry, and they buffer in the meantime. With `acks=0`, producers never see the error, and their records are dropped.
- Consumers use the classic group protocol. KIP-848 consumers (`group.protocol=consumer`) can't use a dynamic route at all, for the whole life of the migration.
- Every member of a consumer group connects through the route, or the group splits across two coordinators.
- [Transactional workloads](../../transactions/discovery.md) and share groups aren't supported on a dynamic route.

**Limitations**

- Only the topics in the batch are fenced and switched. Everything else on the route is untouched.
- A manifest migrates one route and one topic group per run, so each batch is its own manifest. Batches must contain disjoint topics.
- The point of no return is the start of promote. After it, kcp never rolls back. A failure leaves the batch's fence up until you re-run and the batch completes.
- Ctrl+C leaves the fence in place. Re-run `execute` to continue.
- Topic auto-creation isn't supported on a dynamic route, and some consumer group admin APIs are unavailable, so tools such as `kafka-consumer-groups` don't work against it.
- A migration leaves the route dynamic. Finish the job with a [conversion](dynamic-to-static-conversion.md).
- Create the cluster link without `cluster.link.prefix`. kcp expects each mirror topic to have the same name on both clusters.
- Cutover doesn't migrate schemas. Move them before the cutover, for example with [`kcp iac schemas generate`](../../../command-reference/iac/schemas/generate.md).

**Access**

- Credentials for the source, the destination Apache Kafka® listener, and the cluster link REST API, each as a [credentials file](../../../migration-assets/cutover/credentials.md).
- A Kubernetes identity that can read and patch the Gateway CR and read its Deployment. With hot reload on, it also needs `list` on pods, `get` on `pods/proxy`, and a cluster-scoped `get` on the Gateway custom resource definition (CRD). For the full Role, see [Permissions](permissions.md).

## How a batch cutover works

kcp keeps no state between runs. Each run reconciles the manifest against the live Gateway, both clusters, and the cluster link, and continues from wherever they already are. For one batch on a dynamic route the stages are:

1. **Check lag.** Every topic in the batch must be within [`lagThreshold`](../../../migration-assets/cutover/policies.md#policies-lagthreshold). Nothing is fenced yet, so kcp waits for it.
2. **Fence.** kcp adds a `rules.fencing` entry that blocks the batch's topics (`blocked: true`) and waits for every Gateway pod to apply it. Clients of those topics get errors they can retry, and they buffer in the meantime.
3. **Verify the fence.** If [`detectUnroutedProducersDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedproducersduration) is set, kcp reads the source's partition offsets for the batch's topics, waits that duration, and reads them again. If any offset advanced, a producer is still writing to the source directly, and kcp aborts and unfences the batch instead of promoting.
4. **Promote.** kcp waits for each mirror to reach zero lag, then promotes the mirror topics in batches of [`promoteBatchSize`](../../../migration-assets/cutover/policies.md#policies-promotebatchsize) and waits for each to stop mirroring. This is the point of no return.
5. **Switch.** In a single update to the route's `rules`, kcp adds a routing condition that sends the batch's topics to the destination and removes its fence. Clients reconnect to the same Gateway address and land on Confluent Cloud.

A dynamic migration has no offset sync stage: the link's consumer offset sync is already off, and kcp neither pauses nor restores it.

At the fence step, before the first change that blocks traffic, kcp works out how the Gateway applies a new configuration: in place (hot reload) or by rolling its pods. With hot reload, it proves the Gateway applies a configuration revision before anything is blocked, and refuses if it can't. This happens after the lag wait and not under `--dry-run`. Then it waits for every pod to confirm each change. [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout) and [`hotReloadTimeout`](../../../migration-assets/cutover/policies.md#policies-hotreloadtimeout) bound those waits.

A failure before promote removes the fence, so the batch's traffic returns to the source.

## Set up

### 1. Prepare the dynamic route on the Gateway

The route binds both clusters from the start. Before the first batch, every topic is routed to the source, and the fencing list is empty. This is the part of the CR that matters for a dynamic migration. The rest is your normal Gateway configuration.

```yaml
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
  namespace: confluent
spec:
  secretStores:
    - name: file-store
      # ...
  streamingDomains:
    - name: source-kafka-cluster
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: UNAUTHED
            endpoint: PLAINTEXT://source-kafka.confluent.svc.cluster.local:9071
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
            endpoint: SASL_SSL://pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
            tls:
              secretRef: tls
  routes:
    - name: migration-route
      endpoint: bootstrap.gw.example.local:9595
      # Dynamic routes require the host strategy, and with it client TLS.
      brokerIdentificationStrategy:
        type: host
        pattern: "broker$(nodeId).gw.example.local"
      mode: dynamic
      # Plural streamingDomains also makes this a dynamic route.
      streamingDomains:
        - name: source-kafka-cluster
          bootstrapServerId: UNAUTHED
        - name: confluent-cloud
          bootstrapServerId: SASL_PLAIN
      rules:
        routing:
          coordination:
            # Pinned to the source for the life of the migration.
            group: source-kafka-cluster
          # Topics that match no condition go here. Every topic starts on the
          # source; kcp adds a condition per migrated batch.
          default: source-kafka-cluster
        fencing: []
      security:
        client:
          authentication:
            type: none
          tls:
            secretRef: client-tls
        cluster:
          # Every bound domain needs an entry.
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

kcp checks the route on every run, before anything is fenced, and `--dry-run` reports it. It needs exactly two bound domains, `coordination.group` set to the source, and the manifest's `targetStreamingDomain` to be one of the two. Routing conditions must compile as anchored full-match regular expressions.

To build the route yourself, see the CFK documentation for [routes](https://docs.confluent.io/operator/current/gateway/co-gateway-deploy.html#configure-routes), [routing rules](https://docs.confluent.io/operator/current/gateway/co-gateway-deploy.html#configure-routing-rules-early-access), [authentication swapping](https://docs.confluent.io/operator/current/gateway/co-gateway-security.html#configure-authentication-swapping), and [secret stores](https://docs.confluent.io/operator/current/gateway/co-gateway-security.html#secret-store-configuration). See also [Migrate Topics with Topic-Based Routing](https://docs.confluent.io/operator/current/gateway/co-gateway-topic-based-routing.html).

### 2. Plan the batches

Each batch is a manifest with its own topic group. Choose batches so that:

- Topics that must move together are in the same batch. Find them before you plan. The [transactional workloads guide](../../transactions/discovery.md) shows how, though transactional workloads themselves need a [static migration](static-migration.md).
- Batches are disjoint. A topic already migrated by an earlier batch is skipped (nothing to do), so overlap doesn't fail, but it isn't a way to re-run a batch.
- Every consumer of a topic in the batch is ready for the pause. Consumers and producers of those topics are blocked from the fence until the switch. All other topics keep running.

### 3. Write the migration manifest

The manifest declares the migration with [`spec.dynamicMigration`](../../../migration-assets/cutover/overview.md#declare-the-migration-type). Everything outside that section is the same as in a [static migration](static-migration.md#2-write-the-migration-manifest). Every credentials field is a path to a credentials file, and the manifest holds no secrets. Paths resolve relative to the working directory you run kcp from.

```yaml
apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration

metadata:
  name: msk-prod-to-cc-batch-1

spec:
  source:
    type: msk # msk | apache-kafka
    bootstrapServers:
      - b-1.msk-prod.abc123.c2.kafka.us-east-1.amazonaws.com:9096
    credentials: /etc/kcp/source-creds.yaml

  target:
    type: confluent-cloud # confluent-cloud | confluent-platform
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      clusterCredentials: /etc/kcp/dest-kafka-creds.yaml

  clusterLink:
    name: msk-to-cc # must already exist on the destination
    linkCredentials: /etc/kcp/link-creds.yaml

  gateway:
    namespace: confluent
    cr-name: gateway-initial

  dynamicMigration:
    route:
      name: migration-route # the dynamic route in the Gateway CR
      targetStreamingDomain: confluent-cloud # one of the two domains the route binds
    # This batch only. Later batches are separate manifests with disjoint topics.
    topicGroup:
      - topics: ["t1.order", "t1.inventory"]
        # topicPatterns: ['t1\..*'] # RE2, matched against the full topic name. Resolved against the live source on every run.
    policies:
      lagThreshold: 0
      promoteBatchSize: 100
      rolloutTimeout: 10m
      detectUnroutedProducersDuration: 30s # 0s skips the check; minimum 10s
      hotReloadTimeout: 90s
```

Notes on the choices above:

- The manifest has no `consumerOffsetSync` block. It is static-only, because a dynamic route needs offset sync off.
- `topicGroup` takes literal [`topics`](../../../migration-assets/cutover/migrations.md#topicgroup-topics), anchored [`topicPatterns`](../../../migration-assets/cutover/migrations.md#topicgroup-topicpatterns), or both (the union), and has no default.
- Patterns are resolved against the source on every run. A topic created since an interrupted run can change the batch, and kcp then treats the earlier fence as someone else's. Prefer literal `topics` for a batch you might need to resume, or finish the interrupted batch first.
- [`detectUnroutedProducersDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedproducersduration) is off by default. If you can't rule out producers bypassing the Gateway, turn it on.
- [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout) defaults to no deadline, so a rollout that never converges waits indefinitely with traffic already fenced. Set one.

The complete annotated manifest is [`gateway-dynamic-migration.yaml`](../../../gateway-examples/gateway-dynamic-migration.yaml). Every field, validation rule, and policy default is in the [migration manifest reference](../../../migration-assets/cutover/overview.md).

### 4. Dry run

```bash
kcp migration cutover execute --migration-yaml migration.yaml --dry-run
```

A dry run validates the manifest and credentials, then reads the live Gateway CR, both clusters, and the cluster link, and prints the plan: each route check and a verdict per topic. It changes nothing and exits non-zero if the plan is refused. Run it until it passes.

A run is refused, with nothing changed, when any of the following is true:

- The route isn't dynamic, doesn't bind exactly two domains, doesn't bind the target, or doesn't pin `coordination.group` to the source.
- Consumer offset sync is still enabled on the cluster link.
- The cluster link doesn't mirror from `spec.source`, or `spec.target.clusterId` doesn't match the destination.
- A selected topic isn't on the source, isn't on the cluster link, exists on the destination without being a mirror, or has a mirror in a failed or paused state.

### 5. Watch the lag

```bash
kcp migration cutover lag-check --migration-yaml migration.yaml
```

`lag-check` shows replication lag for every mirror topic on the link, whatever the topic selection. It only needs the link REST credentials, so you can run it long before the cutover.

## Run a batch

Schedule a window for the batch's applications. Their clients are paused from the fence until the switch.

```bash
kcp migration cutover execute --migration-yaml migration.yaml
```

You can override any policy for a single run with its flag, for example `--detect-unrouted-producers-duration 60s`. See the [command reference](../../../command-reference/migration/cutover/execute.md).

When it finishes, kcp prints `Migration completed: <metadata.name>`. Re-running a completed batch reconciles to nothing to do.

### If a run stops

| Where it stopped                                                | What happened                                                                           | What to do                                                       |
| --------------------------------------------------------------- | --------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Before the fence (refused plan, lag above threshold)            | Nothing was changed.                                                                    | Fix the cause and re-run.                                        |
| After the fence, before promote (for example unrouted producer) | kcp removes the fence. The batch's traffic returns to the source. The exit is non-zero. | Fix the cause (for example reconfigure the producer) and re-run. |
| During or after promote                                         | The fence stays. Rolling back would split promoted topics from their clients.           | Fix the cause and re-run `execute`. It rolls forward.            |
| Switch reached the CR but wasn't confirmed                      | The new routing is in the CR, but kcp couldn't confirm every pod applied it.            | Check the Gateway (operator acceptance, pods) before anything else. A re-run reads only the CR, sees the topics routed to the destination and reports nothing to do. |
| Ctrl+C or lost connection                                       | The fence stays.                                                                        | Re-run `execute`.                                                |

Resume with a manifest that resolves to the same topics. kcp recognizes the fence it added by its exact shape: a `rules.fencing` entry with only `topics` (the batch's topic set) and `blocked: true`. If the topic set changes between runs, kcp treats the earlier fence as yours and leaves it on the route, so those topics stay blocked after the switch. Finish the interrupted batch first. If it has already happened, remove the leftover `blocked` entry from the route's `rules.fencing` by hand. For how each run reconciles and resumes, see [Execution model](../../../migration-assets/cutover/running-a-cutover.md#execution-model).

## Run the next batch

After a batch completes, the route is still dynamic with the same two bound domains. Its `rules` now have one routing condition for the batch and an empty fencing list:

```yaml
rules:
  routing:
    coordination:
      group: source-kafka-cluster
    default: source-kafka-cluster
    conditions:
      - topics: ["t1.order", "t1.inventory"]
        streamingDomain: confluent-cloud
  fencing: []
```

For the next batch, write a new manifest with the next topics and run it the same way. kcp prepends one condition per batch, so the list grows, up to a size cap on the whole `rules` block. If the cap is hit, kcp refuses with a message to migrate a smaller batch.

Validate each batch before starting the next: confirm its clients are producing and consuming against Confluent Cloud, and its mirrors are `STOPPED` (`kcp migration cutover lag-check` shows each mirror's status). Re-running `kcp migration cutover execute --dry-run` on the batch's manifest reports nothing to do.

## After the last batch

When every topic on the cluster link has been promoted, the route is still dynamic and consumer groups are still coordinated by the source. Run a [conversion](dynamic-to-static-conversion.md) to make the route static and move group coordination to the destination. KIP-848 consumers only come onto the migrated route after it is static.

The source cluster stays on the route until the conversion. Decommission it after the conversion, when you are sure nothing connects to it directly.

## Related content

- [Gateway migrations](index.md)
- [Migrate with a static route](static-migration.md)
- Confluent Documentation: [Migrate Topics Incrementally with Topic-Based Routing and Fencing](https://docs.confluent.io/private-cloud-gateway/current/gateway-topic-based-routing.html)
- [Migration manifest reference](../../../migration-assets/cutover/overview.md)
- [`kcp migration cutover execute`](../../../command-reference/migration/cutover/execute.md)
- [`kcp migration cutover lag-check`](../../../command-reference/migration/cutover/lag-check.md)
- [Discover transactional workloads](../../transactions/discovery.md)
