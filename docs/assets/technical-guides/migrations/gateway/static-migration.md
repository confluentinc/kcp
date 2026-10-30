# Migrate with a static route

A static migration moves all topics on the route to Confluent Cloud in a single cutover, with no client configuration changes. Your applications already connect to the Confluent Gateway. A static route binds all of its traffic to one cluster, so cutting over means pausing the route, promoting the mirror topics on the cluster link, and repointing the route at the destination. Clients reconnect to the same Gateway address and land on Confluent Cloud.

This guide walks through the prerequisites, the migration manifest, and a cutover run. For how static migrations compare with topic-based ones, see [Gateway migrations](index.md).

## Prerequisites and limitations

**Gateway**

- The Gateway runs on Kubernetes and is managed by Confluent for Kubernetes (CFK). kcp reads and patches the Gateway custom resource, so a Gateway started any other way can't be migrated.
- The route to migrate is **static**: it has a single `streamingDomain` bound to the source cluster.
- The destination is already declared on the Gateway CR as a streaming domain with exactly one bootstrap server id, and the route already carries `security.cluster.<destination>` authentication for it, with its secret store and Secrets present. The cutover is a pure `streamingDomain` flip, so no secret or auth change happens during it. See [Stage the destination on the Gateway](#1-stage-the-destination-on-the-gateway).
- The route carries no fence of its own. kcp refuses a route with a fence it didn't add.

<!-- TODO: minimum CFK / operator / Gateway versions for static migrations (hot reload vs. rollout verification). Same open item as index.md. -->

**Cluster link**

- A cluster link from the source to the destination already exists, and every topic you select is a mirror topic on it. `kcp migration cutover` never creates either; use [`kcp migration resources create`](../../../migration-assets/README.md) to set them up.
- Consumer offset sync should be on for the link, so committed offsets reach the destination. kcp doesn't check this.
- Mirror lag must be at or below [`lagThreshold`](../../../migration-assets/cutover/policies.md#policies-lagthreshold) (default `0`, fully caught up) before kcp fences the route.

<!-- TODO: cluster link prerequisites. Link to the kcp docs for creating the link and mirror topics (kcp iac / migration infra?), or to Confluent's cluster linking docs? -->

**Clients**

- Every producer and consumer connects through the Gateway. The fence only blocks traffic that goes through it, so a producer writing to the source directly keeps writing.
- Producers use `acks=1` or `acks=all`. Fenced clients get a retriable error and buffer; with `acks=0` they never see it and their records are dropped.
- Consumer groups move with the cutover. KIP-848 consumers (`group.protocol=consumer`) and [transactional workloads](../../transactions/discovery.md) are supported on a static route.

**Limitations**

- The **whole route** pauses, not only the topics you select. Topic selection decides which mirrors are promoted and lag-checked.
- A manifest migrates one route and one topic group per run.
- The point of no return is the start of promote. After it kcp never rolls back; a failure leaves the fence up until you re-run and the migration completes.
- `Ctrl-C` leaves the fence in place. Re-run `execute` to continue.
- There is no per-topic staging. If you need to move topics in stages, use a [topic-based migration](index.md#dynamic-routes-topic-based).

<!-- TODO: anything else users hit? e.g. cluster.link.prefix on the link, topic auto-creation on the destination, Schema Registry / schema linking, ACLs on the destination. -->

**Access**

- Credentials for the source, the destination Kafka listener, and the cluster link REST API, each as a [credentials file](../../../migration-assets/cutover/credentials.md).
- A Kubernetes identity that can read and patch the Gateway CR and `get` on `pods/proxy`.

## How the cutover works

kcp keeps no state between runs. Each run reconciles the manifest against the live Gateway, both clusters and the cluster link, and continues from wherever they already are. On a static route the stages are:

1. **Check lag.** Every selected mirror topic must be within [`lagThreshold`](../../../migration-assets/cutover/policies.md#policies-lagthreshold).
2. **Fence.** kcp adds a route-level fence (`scope: ALL`, `errorCode: BROKER_NOT_AVAILABLE`) and waits for every Gateway pod to apply it. Clients get retriable errors and buffer.
3. **Pause offset sync** _(optional)_. If [`consumerOffsetSync.pause`](../../../migration-assets/cutover/migrations.md#consumeroffsetsync-pause) is set, kcp waits [`consumerOffsetSyncDrainDuration`](../../../migration-assets/cutover/policies.md#policies-consumeroffsetsyncdrainduration), then disables the link's offset sync.<!-- TODO: confirm the reason for pausing (stop the link overwriting destination offsets once consumers move?) and when to use it. -->
4. **Verify the fence.** If [`detectUnroutedProducersDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedproducersduration) is set, kcp reads the source's partition offsets, waits that duration, and reads them again. If any offset advanced, a producer is still writing to the source directly, and kcp aborts and unfences the route instead of promoting.
5. **Promote.** kcp promotes the mirror topics, in batches of [`promoteBatchSize`](../../../migration-assets/cutover/policies.md#policies-promotebatchsize), and waits for each to stop mirroring. **Point of no return.**
6. **Switch.** kcp flips the route's `streamingDomain` to the destination and removes the fence.
7. **Restore offset sync** _(if paused)_. kcp sets the link's consumer offset sync back to the state you declared in [`consumerOffsetSync.baseline`](../../../migration-assets/cutover/migrations.md#consumeroffsetsync-baseline), the state it was in before the migration.

A failure before promote removes the fence and restores offset sync, so traffic returns to the source.

## Set up

### 1. Stage the destination on the Gateway

The route starts bound to the source. The destination is declared next to it, with swap authentication staged on the route, so the switch needs no further change. This is the part of the CR that matters for a static migration; the rest is your normal Gateway configuration.

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
    # Declared ahead of time so the cutover is only a field flip.
    - name: destination-kafka-cluster
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
            endpoint: SASL_SSL://destination-kafka.confluent.svc.cluster.local:9071
  routes:
    - name: migration-route
      endpoint: gateway-initial.confluent.svc.cluster.local:9595
      # Singular streamingDomain makes this a static route.
      streamingDomain:
        name: source-kafka-cluster
        bootstrapServerId: UNAUTHED
      security:
        client:
          authentication:
            type: none
        cluster:
          source-kafka-cluster:
            auth: passthrough
          # Staged ahead of time: swap auth for the destination.
          destination-kafka-cluster:
            auth: swap
            secretStore: file-store
            authentication:
              type: plain
              jaasConfigPassThrough:
                secretRef: plain-jaas
```

kcp checks this staging on every run, before anything is fenced, and `--dry-run` reports it.

<!-- TODO: link to the Gateway docs for static routes, swap authentication and secret stores so users can build this CR themselves. -->

### 2. Write the migration manifest

The manifest declares the migration with [`spec.staticMigration`](../../../migration-assets/cutover/overview.md#declaring-the-migration-type). Every credentials field is a path to a credentials file, and the manifest holds no secrets. Paths resolve relative to the working directory you run kcp from.

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

  staticMigration:
    route:
      name: migration-route # the static route in the Gateway CR
      targetStreamingDomain: confluent-cloud # declared in the CR, with staged auth
    topicGroup:
      - topics: ["t1.order", "t1.inventory", "t2.order"]
        # topicPatterns: ['orders\..*'] # RE2, matched against the full topic name. ['.*'] selects every source topic, so every one must be mirrored on the link.
    consumerOffsetSync:
      pause: true
      baseline: enabled # what to restore after the switch
    policies:
      lagThreshold: 0
      promoteBatchSize: 100
      rolloutTimeout: 10m
      detectUnroutedProducersDuration: 30s # 0s skips the check; minimum 10s
      consumerOffsetSyncDrainDuration: 15s
```

Notes on the choices above:

- `topicGroup` takes literal [`topics`](../../../migration-assets/cutover/migrations.md#topicgroup-topics), anchored [`topicPatterns`](../../../migration-assets/cutover/migrations.md#topicgroup-topicpatterns), or both (the union). There is no default; to migrate everything, write `topicPatterns: ['.*']`.
- [`detectUnroutedProducersDuration`](../../../migration-assets/cutover/policies.md#policies-detectunroutedproducersduration) is **off by default**. Turn it on if you can't rule out producers bypassing the Gateway.
- [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout) defaults to no deadline, so a rollout that never converges waits indefinitely with traffic already fenced. Set one.
- [`consumerOffsetSync.baseline`](../../../migration-assets/cutover/migrations.md#consumeroffsetsync-baseline) is the offset sync state to restore after the switch. Set it to what the link has now, normally `enabled`.

The complete annotated manifest is [`gateway-static-migration.yaml`](../../../gateway-examples/gateway-static-migration.yaml). Every field, validation rule and policy default is in the [migration manifest reference](../../../migration-assets/cutover/overview.md).

### 3. Dry run

```bash
kcp migration cutover execute --migration-yaml migration.yaml --dry-run
```

A dry run validates the manifest and credentials, then reads the live Gateway CR, both clusters and the cluster link and prints the plan: each precondition and a verdict per topic. It changes nothing and exits non-zero if the plan is refused. Run it until it passes.

<!-- TODO: sample dry-run output (success and a refusal). Want me to capture it from the e2e rig? -->

### 4. Watch the lag

```bash
kcp migration cutover lag-check --migration-yaml migration.yaml
```

`lag-check` shows replication lag for every mirror topic on the link, whatever the topic selection. It only needs the link REST credentials, so you can run it long before the cutover.

## Run the cutover

Schedule a window. Clients are paused from the fence until the switch.

```bash
kcp migration cutover execute --migration-yaml migration.yaml
```

Any policy can be overridden for a single run with its flag, for example `--detect-unrouted-producers-duration 60s`. See the [command reference](../../../command-reference/migration/cutover/execute.md).

<!-- TODO: sample output of a successful run. -->

When it finishes, kcp prints `Migration completed: <metadata.name>`. Re-running a completed migration reconciles to nothing to do.

### If a run stops

| Where it stopped                                     | What happened                                                                  | What to do                                                   |
| ---------------------------------------------------- | ------------------------------------------------------------------------------ | ------------------------------------------------------------ |
| Before the fence (refused plan, lag above threshold) | Nothing was changed.                                                           | Fix the cause and re-run.                                    |
| After the fence, before promote                      | kcp removes the fence and restores offset sync. Traffic returns to the source. | Fix the cause (for example an unrouted producer) and re-run. |
| During or after promote                              | The fence stays. Rolling back would split promoted topics from their clients.  | Fix the cause and re-run `execute`. It rolls forward.        |
| `Ctrl-C` or lost connection                          | The fence stays.                                                               | Re-run `execute`.                                            |

Don't change the manifest's topic selection between an interrupted run and its resume unless you mean to.

## After the cutover

- Confirm clients reconnected and are producing and consuming against Confluent Cloud.
- The source cluster is no longer on the route. Decommission it on your own schedule, once you are sure nothing connects to it directly.
- If you paused offset sync, confirm the link is back at your [`consumerOffsetSync.baseline`](../../../migration-assets/cutover/migrations.md#consumeroffsetsync-baseline).

<!-- TODO: post-cutover verification commands (e.g. kcp or ccloud commands to confirm mirrors are STOPPED and groups are on the destination). Anything on rollback after the switch (reverse link)? -->

## Related content

- [Gateway migrations](index.md)
- [Migration manifest reference](../../../migration-assets/cutover/overview.md)
- [`kcp migration cutover execute`](../../../command-reference/migration/cutover/execute.md)
- [`kcp migration cutover lag-check`](../../../command-reference/migration/cutover/lag-check.md)
- [Discover transactional workloads](../../transactions/discovery.md)
