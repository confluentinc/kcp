# kcp limitations

This page lists the limits of `kcp migration cutover` itself. Limits that come from the Confluent Gateway, such as dynamic routing being Early Access, no transactions or share groups on a dynamic route, no KIP-848 consumers and the `host` broker identification strategy, are in the [Gateway migrations overview](index.md) and the guide for each workflow, and aren't repeated here.

Items marked _inferred_ come from reading the code and haven't been confirmed against a live run.

## Scope

- **kcp doesn't create the cluster link or its mirror topics.** Both must exist before the run. Use [`kcp migration resources create`](../../../migration-assets/README.md) to set them up.
- **kcp doesn't check the link's health, only each mirror's status.** `ACTIVE`, `STOPPED` and `PENDING_STOPPED` are understood. Anything else (`FAILED`, `PAUSED` and so on) refuses the run with `<topic> mirror is in a failed/transitional state`.
- **One route per manifest, and one topic group per run** for static and dynamic migrations.
- **Kubernetes and CFK only.** See the [overview](index.md).

## Connections and authentication

- **kcp connects directly, from the machine it runs on,** to the source and destination brokers, the link's REST endpoint and the Kubernetes API. Each must be reachable from there.
- **kcp's own Kafka connections support:** `iam` (MSK source only), `sasl_scram` (SHA-256 or SHA-512, stated explicitly), `sasl_plain`, `mtls`, `unauthenticated_tls` and `unauthenticated_plaintext`. OAUTHBEARER and Kerberos aren't supported, whatever the Gateway supports. The link REST credential is one of `api_key`, `basic`, `bearer` or `mtls`.
- **Sources are `msk` or `apache-kafka`.** `confluent-platform` isn't accepted as a source, and MSK Serverless isn't supported. See [Source compatibility](../../../source-compatibility.md).
- **A destination `sasl_plain` credential is always TLS.** There is no way to use SASL over plaintext on the destination.
- **IAM sources use the AWS credential chain** on the machine running kcp.
- **No Kubernetes context option.** `spec.gateway` takes `kubeconfig`, `namespace` and `cr-name`, and kcp uses the kubeconfig's current context.
- **Credentials are file paths only.** There is no inline block and no environment variable substitution. Paths resolve against the working directory.

## Cluster link

- **`cluster.link.prefix` isn't supported on any workflow.** A conversion refuses it up front. Static and dynamic migrations don't, but they look up destination topics by their source names, so they are expected to fail at the first lag sweep, before the fence. _Inferred._ Create the link without a prefix.
- **Offset sync.** A dynamic migration and a conversion refuse a link with `consumer.offset.sync.enable` on. A static migration doesn't check it, or the link's `consumer.offset.group.filters`, so a group missing from the filter just doesn't sync.
- **Identity checks pass silently when an id is empty.** kcp compares the link's `source_cluster_id` with the source cluster's id, and `spec.target.clusterId` with the destination's. If either id is empty, for example on a Confluent Platform version that omits `source_cluster_id`, that check is skipped.
- **kcp measures lag itself,** as the sum over partitions of the source high-water mark minus the destination's, from both clusters. `kcp migration cutover lag-check` reads the link's reported lag instead, so the two can differ. Both kcp credentials therefore need `DESCRIBE` on topics.
- **The REST client has no timeout and no retry.** A transient failure, such as a 429 or 5xx, aborts the step. Before promote that rolls the fence back; inside promote it keeps the fence. `ListMirrorTopics` is a single request with no pagination handling.

## Topic selection

- **Patterns are RE2, anchored to the whole topic name.** The Gateway matches Java-style patterns. Lookahead, backreferences and possessive quantifiers are rejected, both in the manifest and in the live CR's routing conditions. There is no exclude list, so "everything except X" can't be written.
- **`topicPatterns: ['.*']` selects every topic the broker doesn't flag as internal,** and every one of them must be a mirror on the link or the whole run is refused. On MSK that can include the `__amazon_msk_canary` topic. _Inferred._

## Waits with no deadline

| Wait                                            | Limit                                                                                                          |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Zero lag after the fence, before promote        | None, and it ignores `lagThreshold`. The fence stays up. `Ctrl-C` leaves it.                                   |
| Mirrors reaching `STOPPED` after promote        | None. A mirror that never gets there hangs the run with the fence up.                                          |
| Lag before the fence                            | None. Fails after three consecutive read failures.                                                             |
| Operator acceptance and rollout                 | [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout), default no deadline. |

Set `rolloutTimeout`. The first two have no setting.

## Verifying the Gateway applied a change

How kcp proves a change took effect depends on whether `spec.hotReload.enabled` is true on the live Gateway CR. kcp reads only that field. If it is absent or malformed, kcp treats hot reload as off.

**Hot reload on.** Every gate is a hard requirement. A failure refuses the run before the fence, with no fallback:

- kcp needs cluster-scoped `get` on the Gateway CRD, and the CRD must declare `spec.configId`.
- Every ready pod must answer `GET /config` through `pods/proxy`, on port 9180 unless `policies.gatewayConfigPort` says otherwise.
- A smoke test patches `spec.configId` alone on the live CR and waits for every pod to report it. A Gateway running on a trial licence doesn't run its config watcher: the operator reports success and the pods never apply the change, so kcp refuses.

**Hot reload off.** kcp waits up to ten seconds for the Gateway's Deployment generation to change. If it doesn't, kcp reports that no pod restart was required and treats that as success. It never reads what the pods applied. If the operator updates the Deployment more than ten seconds late, or applies a change in place by some route other than `spec.hotReload.enabled`, kcp can report success when the change hasn't landed.

**Operator acceptance.** kcp waits for `status.observedGeneration` to reach the CR's generation. A `False` condition whose reason ends in `Failed` counts as a rejection only after it persists for 30 seconds, and conditions older than the current generation are ignored. This is a heuristic against stale conditions, not a guarantee.

`--dry-run` tests none of this. See [Permissions](permissions.md).

<!-- TODO: minimum Gateway and CFK versions for hot reload. Same open item as index.md. -->

## Things kcp can't detect

- **Producers that bypass the Gateway.** Detected only if `detectUnroutedProducersDuration` is set, and it is off by default. The check takes two source offset snapshots a window apart, so it only sees producers that are active during the window, and only on topics in this run's selection. A static fence blocks the whole route, but unselected topics aren't watched.
- **Consumers that bypass the Gateway.** Only a conversion checks. For static and dynamic migrations, a consumer on the source splits its group.
- **Route topics that aren't selected or mirrored.** On a static route they flip to the destination with no warning.
- **A route bound to neither domain.** kcp doesn't compare a streaming domain's endpoint with the clusters you declared. A static route bound to something other than the source is flipped to the destination without a refusal.
- **A fallback written as `unmatchedTopics`.** kcp reads only `routing.default` as the fallback, so a topic that matches no condition resolves to no domain. On a conversion that appears as `route sends it to no streaming domain`. _Inferred._
- **Secret contents.** The static Secret check tests existence only, by name, for Kubernetes Secrets. It doesn't read contents, external stores such as Vault or AWS Secrets Manager, or TLS Secrets.
- **Client behaviour.** `acks`, transactions, share groups and the group protocol are all invisible to kcp.
- **A GitOps controller that reverts the fence.**

## Kubernetes assumptions

- The Gateway is `platform.confluent.io/v1beta1`, resource `gateways`.
- Pods carry the label `app=<Gateway CR name>`.
- The Gateway's Deployment is named like the CR. Failing that, kcp uses the one Deployment owned by it, and more than one match is an error.
- kcp edits the CR with JSON Patches that test the route's index and the field being replaced. A concurrent edit makes the patch fail rather than merge. See [Idempotency and resumability](idempotency.md#concurrency).

## Dynamic migration

- **Each batch adds a routing condition and a fence entry to `rules`, and kcp never compacts them.** A `rules` block over 512 KiB is refused: `rules block within size limit`. Narrow the selector or migrate a smaller batch.
- **A fence over the same topics that you wrote is left untouched.** kcp warns about shadowing only for conditions that name a topic exactly, not for pattern conditions.
- **No check of `security.cluster` or Secrets,** or of the `host` strategy's TLS. The Gateway or CFK rejects a problem at the fence or the switch, after the lag wait.
- **kcp doesn't verify that the other bound domain is the cluster the link mirrors,** beyond the cluster id check.
- **Batches must run one at a time.** See [Idempotency and resumability](idempotency.md#concurrency).

## Dynamic-to-static conversion

- **Scope is every group on the source cluster, not just the route's.** An unrelated application's group that has committed on a topic not on the link refuses the conversion. Kafka Streams changelog and repartition topics and Kafka Connect internal topics must be on the link, or the group must go. _Inferred._
- **Only committed offsets and their metadata are copied.** Group configuration, share group state and transactional state aren't.
- **Offsets above a destination partition's high-water mark, or on a topic missing from the destination, refuse the sync.** Nothing is written.
- **Any change in a group's offsets between the two snapshots fails the run and rolls back,** including a removal. That includes admin resets and offset expiry. Commits after the second snapshot aren't seen, and those consumers re-read from the copied offset.
- **The check window can't be skipped.** `detectUnroutedCommitsDuration` defaults to 30 seconds with a minimum of 10.
- **The switch drops `streamingDomains` and `rules` from the route and keeps the rest,** including the source's `security.cluster` entry. It doesn't touch the CR's `spec.streamingDomains`.
- **A destination share group with the same name as a source group refuses the run, and a group with live members on the destination fails the sync.** Detecting the share-group clash needs a destination that reports group types (Kafka 3.8 or later).
- **A rejected plan can take several runs to clear.** The credential checks stop the later link checks, so fixing one credential can reveal another refusal.

## Fixed values

| Value                                         | Setting                                          |
| --------------------------------------------- | ------------------------------------------------ |
| Lag poll every 2 seconds, promote poll every 5 | Not configurable                                |
| Offset sweep failures before the run aborts: 3 | Not configurable                                |
| Promote attempts per topic: 3                 | Not configurable                                 |
| Rollout confirmation window: 10 seconds       | Not configurable                                 |
| Rejection settle window: 30 seconds           | Not configurable                                 |
| Link config write timeout: 30 seconds         | Not configurable                                 |
| Hot reload wait: 90 seconds                   | [`hotReloadTimeout`](../../../migration-assets/cutover/policies.md#policies-hotreloadtimeout) |
| `/config` port: 9180                          | `gatewayConfigPort`                              |
| Offset sync concurrency: 8 connections        | `offsetSyncConcurrency`                          |
| Promote batch size: all topics in one request | [`promoteBatchSize`](../../../migration-assets/cutover/policies.md#policies-promotebatchsize) |

## `lag-check`

`kcp migration cutover lag-check` is a terminal UI. It has no non-interactive output mode, so it isn't suitable for a CI job. It needs only the link REST credential and polls every second by default (`--poll-interval`, 1 to 60 seconds).

## Related content

- [Gateway migrations](index.md)
- [Permissions](permissions.md)
- [Idempotency and resumability](idempotency.md)
- [Migration manifest reference](../../../migration-assets/cutover/overview.md)
