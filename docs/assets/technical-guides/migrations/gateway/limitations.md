# Gateway Migration Limitations

This page covers the boundaries of `kcp migration cutover` itself, so you can plan around them.

Limits that come from Confluent Gateway, such as dynamic routing being Early Access, no transactions or share groups on a dynamic route, no KIP-848 consumers, and the `host` broker identification strategy, are in the [Gateway migrations overview](overview.md) and the guide for each workflow.

## Scope

- **The cluster link and its mirror topics are prepared before the run.** kcp reads and promotes them but doesn't create them. Use [`kcp migration resources create`](../../../migration-assets/README.md) to set them up.
- **kcp looks at each mirror's status.** `ACTIVE`, `STOPPED`, and `PENDING_STOPPED` are understood. A mirror in any other state, such as `FAILED` or `PAUSED`, stops the run with `<topic> mirror is in a failed/transitional state`, so bring the mirror back to a healthy state first.
- **A manifest covers one route**, and for static and dynamic migrations one topic group per run.
- **Kubernetes and Confluent for Kubernetes (CFK).** See the [overview](overview.md).

## Connections and authentication

- **kcp connects directly from the machine it runs on** to the source and destination brokers, the link's REST endpoint, and the Kubernetes API, so each needs to be reachable from there.
- **kcp's own Apache Kafka® connections** support `iam` (MSK source only), `sasl_scram` (SHA-256 or SHA-512, stated explicitly), `sasl_plain`, `mtls`, `unauthenticated_tls`, and `unauthenticated_plaintext`. OAUTHBEARER and Kerberos aren't available for kcp's own connections, even where the Gateway supports them. The link REST credential is one of `api_key`, `basic`, `bearer`, or `mtls`.
- **Sources are `msk` or `apache-kafka`.** `confluent-platform` isn't accepted as a source, and MSK Serverless isn't supported. See [Source compatibility](../../../source-compatibility.md).
- **A destination `sasl_plain` credential always uses TLS.**
- **IAM sources use the AWS credential chain** on the machine running kcp.
- **The Kubernetes context** is the kubeconfig's current one. `spec.gateway` takes `kubeconfig`, `namespace`, and `cr-name`, so select the context in the kubeconfig before the run.
- **Credentials are file paths.** The manifest takes a path rather than an inline block or an environment variable, and paths resolve against the working directory.

## Cluster link

- **Create the link without `cluster.link.prefix`.** kcp expects each mirror topic to carry the same name on both clusters. A conversion checks this and stops with a clear message. For static and dynamic migrations, plan on unprefixed mirrors.
- **Offset sync.** A dynamic migration and a conversion require `consumer.offset.sync.enable` to be off on the link. A static migration doesn't check it, or the link's `consumer.offset.group.filters`, so confirm the filters include the groups you want to move.
- **Cluster identity checks.** kcp compares the link's `source_cluster_id` with the source cluster's ID, and `spec.target.clusterId` with the destination's. If an ID is empty, for example on a Confluent Platform version that doesn't report `source_cluster_id`, that comparison is skipped.
- **Lag.** kcp computes lag from partition offsets on both clusters: for each partition, the source high-water mark minus the destination's, summed. `kcp migration cutover lag-check` shows the lag the link reports, so the two figures can differ slightly. Both kcp Kafka credentials need `DESCRIBE` on topics for this.
- **REST requests.** kcp sends link REST requests without a request timeout and doesn't retry them, so a transient failure such as a 429 or 5xx ends the step. Before promote, kcp rolls the fence back. During promote, the fence stays and a re-run continues. Mirror topics are listed in a single request.

## Topic selection

- **Patterns use RE2 syntax and match the whole topic name.** The Gateway itself uses Java-style patterns, so constructs such as lookahead, backreferences, and possessive quantifiers aren't accepted, either in the manifest or in the routing conditions of the live Gateway custom resource (CR). The manifest has no exclude list, so write patterns that name what you want to include.
- **`topicPatterns: ['.*']` selects every topic the broker doesn't flag as internal,** and each one needs to be a mirror on the link or the run is refused. If your cluster holds topics that aren't mirrored, such as monitoring topics, prefer patterns or literal names that cover your application topics.

## Waits and timeouts

| Wait                                     | Bound                                                                                                           |
| ---------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| Zero lag after the fence, before promote | Not configurable. It waits for exactly zero, independent of `lagThreshold`, with the fence in place.            |
| Mirrors reaching `STOPPED` after promote | Not configurable. kcp waits for the link to finish promoting.                                                   |
| Lag before the fence                     | Not configurable. The run stops after three consecutive offset read failures.                                   |
| Operator acceptance and rollout          | [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout), which defaults to no deadline. |

Setting `rolloutTimeout` is recommended, so a rollout that doesn't converge stops the run instead of waiting while traffic is fenced.

## How kcp verifies a Gateway change

How kcp confirms that a change took effect depends on `spec.hotReload.enabled` on the live Gateway CR. kcp reads that field only. If it is absent, kcp treats hot reload as off.

**Hot reload on.** kcp checks the following before it fences, and stops with a message naming the missing piece if one isn't in place:

- kcp can `get` the Gateway custom resource definition (CRD) at cluster scope, and the CRD declares `spec.configId`.
- Every ready pod answers `GET /config` through `pods/proxy`, on port 9180 unless `policies.gatewayConfigPort` says otherwise.
- A check patches `spec.configId` alone on the live CR and waits for every pod to report it. A Gateway on a trial license doesn't run its configuration watcher: the operator reports success while the pods never apply the change, and kcp stops before the fence.

**Hot reload off.** kcp waits up to 10 seconds for the Gateway's Deployment generation to change. If it doesn't change, kcp takes that to mean no pod restart was needed and continues. kcp relies on this Deployment signal and doesn't read what each pod applied, so if the operator updates the Deployment more than 10 seconds later, or applies a change in place by another route, check the Gateway's state before you move on.

**Operator acceptance.** kcp waits for `status.observedGeneration` to reach the CR's generation. A `False` condition whose reason ends in `Failed` is treated as a rejection after it has persisted for 30 seconds, and conditions older than the current generation are ignored, which guards against stale conditions.

`--dry-run` doesn't run these checks. For what to grant ahead of a real run, see [Permissions](permissions.md). For the Gateway and CFK versions, see [Version requirements](overview.md#version-requirements).

## What to confirm yourself

kcp works from the Gateway CR, the clusters, and the link. A few things sit outside what it can observe, so they are worth confirming before a cutover:

- **Producers that bypass the Gateway.** kcp checks for these only when you set `detectUnroutedProducersDuration`, which is off by default. The check compares two source offset snapshots a window apart, so it sees producers active during the window, on the topics in this run's selection. A static fence blocks the whole route, but topics outside the selection aren't watched.
- **Consumers that bypass the Gateway.** A conversion checks for these. For static and dynamic migrations, make sure every consumer in a group connects through the Gateway, so the group isn't split across two coordinators.
- **Route topics that aren't selected or mirrored.** On a static route these move to the destination along with the rest of the route, without a warning.
- **The streaming domains' endpoints.** kcp doesn't compare a streaming domain's endpoint with the clusters in the manifest, so confirm that the source and destination domains point where you expect.
- **The fallback route.** kcp reads `routing.default` as the fallback domain, so declare the fallback there rather than as `unmatchedTopics`.
- **Staged Secrets.** For a static migration, kcp checks that each Kubernetes Secret named in the staged `security.cluster` block exists. It doesn't read the contents, and it doesn't check external stores such as Vault or AWS Secrets Manager.
- **Client settings.** `acks`, transactions, share groups, and the group protocol aren't visible to kcp. See the guide for your workflow for what each needs.
- **GitOps.** If a GitOps controller manages the Gateway CR, consider pausing syncing for the cutover, so it doesn't revert the fence.

## Kubernetes assumptions

- The Gateway is `platform.confluent.io/v1beta1`, resource `gateways`.
- Pods carry the label `app=<Gateway CR name>`.
- The Gateway's Deployment is named like the CR. Otherwise kcp uses the single Deployment the CR owns, and more than one match stops the run.
- kcp edits the CR with JSON Patches that test the route's index and the field being replaced, so a concurrent edit makes the patch fail rather than merge. See [Idempotency and resumability](idempotency.md#concurrency).

## Dynamic migration

- **Each batch adds a routing condition and a fence entry to `rules`.** kcp doesn't compact them, and a `rules` block over 512 KiB is refused with `rules block within size limit`, so narrow the selector or migrate a smaller batch.
- **A fence you wrote over the same topics is left untouched.** kcp warns about shadowing for conditions that name a topic exactly, but not for pattern conditions.
- **Confirm `security.cluster`, its Secrets, and the TLS settings for the `host` strategy before the run.** kcp doesn't check them, and the Gateway or CFK reports a problem at the fence or the switch, after the lag wait.
- **kcp treats the other bound domain as the source.** Beyond the cluster ID check, kcp doesn't verify that it is the cluster the link mirrors.
- **Run one batch at a time.** See [Idempotency and resumability](idempotency.md#concurrency).

## Dynamic-to-static conversion

- **Scope is every consumer group on the source cluster,** not only the route's. A group that has committed offsets on a topic that isn't on the link stops the conversion. That includes groups behind Kafka Streams changelog and repartition topics and Kafka Connect internal topics, so put those topics on the link or retire the group first.
- **Committed offsets and their metadata are copied.** Group configuration, share group state, and transactional state aren't.
- **The sync stops, writing nothing,** if an offset is above a destination partition's high-water mark, or if a topic is missing on the destination.
- **The run stops and rolls back** if a group's offsets change between the two snapshots, including a removal or an admin reset, or if offsets expire. Commits after the second snapshot aren't seen, and those consumers re-read from the copied offset.
- **The check window always runs.** `detectUnroutedCommitsDuration` defaults to 30 seconds, with a minimum of 10.
- **The switch removes `streamingDomains` and `rules` from the route and keeps the rest,** including the source's `security.cluster` entry. It doesn't change the CR's `spec.streamingDomains`.
- **Groups on the destination.** A destination share group with the same name as a source group stops the run, and so does a group with live members on the destination. Detecting a share-group name clash needs a destination that reports group types (Kafka 3.8 and later).
- **Credential checks run in order,** and a failing one skips the later link checks, so fixing one credential can show the next item to address. `--dry-run` shows each in turn.

## Fixed values

| Value                                          | Setting                                                                                       |
| ---------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Lag poll every 2 seconds, promote poll every 5 | Not configurable                                                                              |
| Offset sweep failures before the run stops: 3  | Not configurable                                                                              |
| Promote attempts per topic: 3                  | Not configurable                                                                              |
| Rollout confirmation window: 10 seconds        | Not configurable                                                                              |
| Rejection settle window: 30 seconds            | Not configurable                                                                              |
| Link configuration write timeout: 30 seconds   | Not configurable                                                                              |
| Hot reload wait: 90 seconds                    | [`hotReloadTimeout`](../../../migration-assets/cutover/policies.md#policies-hotreloadtimeout) |
| `/config` port: 9180                           | `gatewayConfigPort`                                                                           |
| Offset sync concurrency: 8 connections         | `offsetSyncConcurrency`                                                                       |
| Promote batch size: all topics in one request  | [`promoteBatchSize`](../../../migration-assets/cutover/policies.md#policies-promotebatchsize) |

## `lag-check`

`kcp migration cutover lag-check` is an interactive terminal UI, so it is best run from a terminal rather than a CI job. It needs only the link REST credential and polls every second by default (`--poll-interval`, 1 to 60 seconds).

## Related content

- [Gateway migrations](overview.md)
- [Permissions for Gateway migrations](permissions.md)
- [Idempotency and resumability of Gateway migrations](idempotency.md)
- [Migration manifest reference](../../../migration-assets/cutover/overview.md)
