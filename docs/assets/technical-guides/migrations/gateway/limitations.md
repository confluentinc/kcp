# Gateway Migration Limitations

This page covers the boundaries of `kcp migration cutover` itself, so you can plan around them.

Limits that come from Confluent Gateway, such as dynamic routing being Early Access, no transactions or share groups on a dynamic route, no KIP-848 consumers, and the `host` broker identification strategy, are in the [Gateway migrations overview](overview.md). Limits that apply to one workflow only are in the guide for that workflow.

## Scope

- **kcp doesn't create the cluster link or its mirror topics.** Set them up before the run, for example with [`kcp migration resources create`](../../../migration-assets/README.md).
- **Mirrors must be healthy.** kcp understands `ACTIVE`, `STOPPED`, and `PENDING_STOPPED`. A mirror in any other state, such as `FAILED` or `PAUSED`, stops the run with `<topic> mirror is in a failed/transitional state`.
- **A manifest covers one route**, and for static and dynamic migrations one topic group per run.

## Connections and authentication

- **kcp connects directly from the machine it runs on** to the source and destination brokers, the link's REST endpoint, and the Kubernetes API, so each needs to be reachable from there.
- **kcp's own Apache Kafka® connections** support `iam` (MSK source only), `sasl_scram` (SHA-256 or SHA-512, stated explicitly), `sasl_plain`, `mtls`, `unauthenticated_tls`, and `unauthenticated_plaintext`. OAUTHBEARER and Kerberos aren't available for kcp's own connections, even where the Gateway supports them.
- **Sources are `msk` or `apache-kafka`.** `confluent-platform` isn't accepted as a source, and MSK Serverless isn't supported. See [Source compatibility](../../../source-compatibility.md).
- **The Kubernetes context is the kubeconfig's current one.** The manifest has no field to pick another, so select it in the kubeconfig before the run.
- **Credentials are file paths,** not inline blocks or environment variables, and paths resolve against the working directory.

## Cluster link

- **A static migration doesn't check offset sync.** Dynamic migrations and conversions refuse a link with `consumer.offset.sync.enable` on. A static migration checks neither that setting nor `consumer.offset.group.filters`, so confirm the filters include the groups you want to move.
- **Cluster ID checks can be skipped.** kcp compares the link's `source_cluster_id` with the source cluster's ID, and `spec.target.clusterId` with the destination's. If an ID is empty, for example on a Confluent Platform version that doesn't report `source_cluster_id`, that comparison is skipped.
- **kcp's lag can differ slightly from `lag-check`.** kcp computes lag from partition offsets on both clusters, while `kcp migration cutover lag-check` shows the lag the link reports.
- **Link REST requests have no timeout and aren't retried,** so a transient failure such as a 429 or 5xx ends the step. Before promote, kcp rolls the fence back. During promote, the fence stays and a re-run continues.

## Topic selection

- **Patterns use RE2 syntax and match the whole topic name.** The Gateway uses Java-style patterns, so constructs such as lookahead, backreferences, and possessive quantifiers aren't accepted, either in the manifest or in the routing conditions of the live Gateway custom resource (CR). The manifest has no exclude list, so write patterns that name what you want to include.
- **`topicPatterns: ['.*']` selects every topic the broker doesn't flag as internal,** and each one needs to be a mirror on the link or the run is refused. If your cluster holds topics that aren't mirrored, such as monitoring topics, prefer patterns or literal names that cover your application topics.

## Waits and timeouts

| Wait                                                     | Bound                                                                                                               |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| Lag before the fence                                     | None. The run stops after three consecutive offset read failures.                                                   |
| Zero lag after the fence, and mirrors reaching `STOPPED` | None. kcp waits for exactly zero, whatever `lagThreshold` is, with the fence in place.                              |
| Operator acceptance and Gateway rollout                  | [`rolloutTimeout`](../../../migration-assets/cutover/policies.md#policies-rollouttimeout). Default: no deadline.    |
| Per-pod configuration wait (hot reload)                  | [`hotReloadTimeout`](../../../migration-assets/cutover/policies.md#policies-hotreloadtimeout). Default: 90 seconds. |

## How kcp verifies a Gateway change

kcp decides how to confirm a change from `spec.hotReload.enabled` on the live Gateway CR. If the field is absent, kcp treats hot reload as off.

- **Hot reload on.** Before the fence, kcp patches `spec.configId` alone on the live CR and waits for every pod to report it on `GET /config` (port 9180 unless `policies.gatewayConfigPort` says otherwise). A Gateway on a trial license doesn't run its configuration watcher, so its pods never apply the change and kcp stops before the fence. For the grants this needs, see [Permissions](permissions.md#cluster-scoped-grant-for-hot-reload).
- **Hot reload off.** kcp waits up to 10 seconds for the Gateway's Deployment generation to change. If it doesn't, kcp takes that to mean no pod restart was needed. kcp doesn't read what each pod applied, so if the operator updates the Deployment later than that, or applies a change in place another way, check the Gateway's state before you move on.

`--dry-run` doesn't run these checks.

## What to confirm yourself

kcp works from the Gateway CR, the clusters, and the link. A few things sit outside what it can observe, so confirm them before a cutover:

- **Producers that bypass the Gateway.** kcp checks for these only when you set `detectUnroutedProducersDuration`, which is off by default. The check compares two source offset snapshots a window apart, so it sees only producers active during the window, on the topics in this run's selection.
- **Consumers that bypass the Gateway.** A conversion checks for these. For static and dynamic migrations, make sure every consumer in a group connects through the Gateway, so the group isn't split across two coordinators.
- **Route topics that aren't selected or mirrored.** On a static route these move to the destination along with the rest of the route, without a warning.
- **The streaming domains' endpoints.** kcp doesn't compare a streaming domain's endpoint with the clusters in the manifest, so confirm that the source and destination domains point where you expect.
- **The fallback route.** kcp reads `routing.default` as the fallback domain, so declare the fallback there rather than as `unmatchedTopics`.
- **Gateway Secrets and `security.cluster`.** What kcp checks differs by workflow (see [Gateway secrets](permissions.md#gateway-secrets)). It never reads Secret contents, external stores such as Vault or AWS Secrets Manager, or the TLS settings for the `host` strategy. A problem there surfaces when the Gateway or CFK rejects the fence or the switch, after the lag wait.
- **Client settings.** `acks`, transactions, share groups, and the group protocol aren't visible to kcp. See the guide for your workflow for what each needs.

## Dynamic migration

- **Each batch adds a routing condition to `rules`,** and kcp doesn't compact them. A fence or switch that would make the `rules` block larger than 512 KiB is refused with `rules block within size limit`, so narrow the selector or migrate a smaller batch.
- **A fence you wrote over the same topics is left untouched.** kcp warns about shadowing for conditions that name a topic exactly, but not for pattern conditions.
- **kcp treats the other bound domain as the source.** Beyond the cluster ID check, kcp doesn't verify that it is the cluster the link mirrors.

## Related content

- [Gateway migrations](overview.md)
- [Permissions for Gateway migrations](permissions.md)
- [Idempotency and resumability of Gateway migrations](idempotency.md)
- [Migration manifest reference](../../../migration-assets/cutover/overview.md)
