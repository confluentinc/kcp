# Migration manifest reference

`kcp migration execute` runs a cutover described by a single YAML manifest,
`gateway-migration.yaml`: it fences the route in the Confluent Gateway,
promotes the mirror topics on the cluster link in batches, and switches
production traffic over to the destination. It runs one of three workflows:
all at once on a static route, topic by topic on a dynamic route, and, with
`spec.route.convertTo: static`, a
[conversion of a dynamic route to static](#converting-a-dynamic-route-to-static-specrouteconvertto)
once every topic on the cluster link is migrated. `kcp migration lag-check`
reads the same manifest to show replication lag on the cluster link. This page
is the field-by-field reference for that manifest, followed by fully-annotated
[example manifests](#example-manifest).

## Execution model

This manifest drives a resumable migration workflow:

- **`execute`** validates the manifest and live infrastructure, reads the live initial gateway CR to resolve the route's mode (see [`spec.route`](#specroute)) — and, on a static route, to derive the bootstrap server id from the target domain — and carries the migration forward through fence → promote → switch (a conversion: fence → verify → sync offsets → switch). Every run reconciles live from the manifest and the current cluster state, so an interrupted run is safely continued by re-running the same command — it resumes from whatever the live world already reflects. It re-reads the manifest (topology and policy) on every invocation.
  - **`--dry-run`** runs only the reconcile step, against the live gateway CR, the source and destination Kafka clusters and the cluster link, and prints the plan: each precondition (the route exists and has the expected mode, the gateway CR is set up the way that mode needs, the cluster link mirrors from the source cluster and `spec.target.clusterId` matches the destination) and a verdict for every selected topic. Nothing is changed and no migration steps run. It exits non-zero if the plan is refused. Flag overrides are applied and validated first, so a dry run rejects an invalid override exactly as a real run would. Useful for iterating on the manifest and your infrastructure before scheduling a live cutover.
- **`lag-check`** is an interactive terminal view of replication lag for every mirror topic on the cluster link, independent of `execute`. It uses only the destination REST leg (`spec.clusterLink.linkCredentials`), so it works before `execute` has ever run.

Every `execute` reconciles the current manifest live against the cluster. A completed migration re-reconciles to nothing to do — `execute` then runs no migration steps and reports that — and an interrupted one continues from the live state. To migrate a different topology, edit the manifest and run `execute` again. `metadata.name` is only a label (it appears in logs, the run report and the completion message); changing it does not change what is reconciled.

**Resume an interrupted migration with the same topic set (dynamic routes).**
On a dynamic route, kcp recognises the fence it added by its exact shape: a
`rules.fencing` entry with only `topics` (the migration's exact topic set) and
`blocked: true`. Any other entry, including one over the same topics that also
sets `trafficType` or `topicPatterns`, is an operator's, and kcp leaves it in
place. If a run is interrupted after fencing, resume it with a manifest that
resolves to the same topics. Resuming with a different set — a topic added or
removed, or a `topicPatterns` entry that now also matches a topic created on
the source since the interrupted run — is a different migration: kcp fences
the new set, treats the earlier fence as operator-authored, and leaves it on
the route, so those topics stay blocked after switchover. Finish the
interrupted migration first, then migrate the changed set. If this has already
happened, remove the leftover `blocked` entry from the route's `rules.fencing`
by hand.

**Resume an interrupted conversion.** A route conversion keeps no state
between runs either. kcp recognises its conversion fence by its exact shape:
a `rules.fencing` entry with only `topicPatterns: ['.*']` and `blocked: true`.
If a conversion is interrupted, re-run the same manifest: kcp finds its fence
on the route, re-checks everything and carries on. See
[Interruptions](#interruptions).

**A static route must not carry someone else's fence.** A static route has a
single route-level `fence`. kcp recognises its own by its exact value,
`{scope: ALL, errorCode: BROKER_NOT_AVAILABLE}`, and refuses a route that
already carries any other fence, because the switchover and a rollback would
remove it: remove that fence before migrating. `scope: NONE` counts as no
fence. An operator's plain `scope: ALL` fence reads back with the same
defaulted `errorCode`, so kcp cannot tell it from its own and would remove it
too; remove it before migrating.

Each `spec.defaultPolicies` field is a default that a matching CLI flag can
override for a single run, without editing the file.

## Before you run it

- **Connections.** Every `execute` run, including `--dry-run`, connects to the
  source Kafka cluster, the destination Kafka cluster, the cluster-link REST
  API and the Kubernetes API, using the credentials and kubeconfig named in the
  manifest. To confirm that each gateway pod applied a config change, the
  Kubernetes identity also needs `get` on `pods/proxy`.
- **Producers must use the gateway.** The fence blocks traffic that goes
  through the gateway. Producers writing to the source directly are not
  blocked, and the check that catches them (`detectUnroutedProducersDuration`)
  is **off by default**. A route conversion does not use
  `detectUnroutedProducersDuration` at all; it has its own
  `detectUnroutedCommitsDuration` instead (see
  [What it does](#what-it-does)): it watches for consumers committing to the
  source directly, and that check can't be turned off.
- **What is fenced.** A static route is fenced and switched as a whole — the
  topic selection only decides which mirrors are promoted and lag-checked. A
  dynamic route fences and switches only the selected topics.
- **Promote is the point of no return.** If a step fails after the fence is up
  but before any topic is promoted, kcp removes the fence and restores offset
  sync so traffic returns to the source. Once any topic is promoted or
  promoting, kcp never removes the fence — doing so would split those clients
  from the target. Fix the cause and re-run `execute` to roll forward. A
  conversion promotes nothing; its point of no return is the switch (see
  [What it does](#what-it-does)).
- **Interrupting kcp leaves the fence up.** kcp has no signal handler, so
  Ctrl-C does not remove the fence; the gateway stays fenced until you re-run
  `execute`.
- **No deadline by default.** `rolloutTimeout` defaults to no deadline, so a
  rollout that never converges waits indefinitely, with traffic already fenced.
- **A refused plan changes nothing.** If reconcile refuses the plan (for
  example a selected topic is not a mirror topic on the link), the whole run is
  refused before anything is fenced or changed. `--dry-run` shows the plan and
  the reasons.

## At a glance

```yaml
apiVersion: kcp.confluent.io/v1alpha1 # required, exact literal
kind: GatewayMigration # required, exact literal
metadata:
  name: my-migration # required, non-blank — the migration identity
spec:
  source: { ... } # required — cluster being migrated from
  target: { ... } # required — cluster being migrated to
  clusterLink: { ... } # required — an ALREADY-EXISTING cluster link
  gateway: { ... } # required — namespace, kubeconfig, initial gateway CR name
  route: { ... } # required — the route name, topic selection, and target domain
  defaultPolicies: { ... } # optional — execute-time policy defaults
```

`apiVersion` must equal `kcp.confluent.io/v1alpha1` and `kind` must equal
`GatewayMigration`, exactly. `metadata.name` is required and non-blank: it is the
migration's identity, used as its `migration_id` in logs and output.
`spec.source`, `spec.target`, `spec.clusterLink`, `spec.gateway`, and
`spec.route` are always required; `spec.defaultPolicies` is optional.

Every credentials field is a **path to a credentials file** — the manifest never
holds secret material inline, and there is no `${ENV_VAR}` substitution: a
`${VAR}`-shaped value is read literally.

## `spec.source`

The cluster being migrated from. `kcp` only ever reads from it.

| Field              | Type           | Required | Notes                                                               |
| ------------------ | -------------- | -------- | ------------------------------------------------------------------- |
| `type`             | enum           | yes      | `msk` or `apache-kafka`. Gates auth: `iam` is valid only for `msk`. |
| `bootstrapServers` | `[]string`     | yes      | Non-empty; each entry `host:port`.                                  |
| `credentials`      | path           | yes      | Path to a Kafka-family credentials file — see [Credentials](#credentials) below.   |

`confluent-platform` is not a valid value here — this manifest only ever
points at a link that already exists, so it never needs to act as a
source-side link initiator.

## `spec.target`

The cluster being migrated to.

| Field                    | Type           | Required                                                      | Notes                                                                                                                                                                                    |
| ------------------------ | -------------- | ------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `type`                   | enum           | yes                                                           | `confluent-cloud` or `confluent-platform`.                                                                                                                                               |
| `clusterId`              | string         | yes                                                           | Required for **both** destination types — this kind never discovers it live.                                                                                                             |
| `kafka.bootstrapServers` | `[]string`     | yes                                                           | Destination Kafka bootstrap.                                                                                                                                                             |
| `kafka.restEndpoint`      | string         | yes                                                           | Destination Admin REST endpoint (cluster link + topic operations).                                                                                                                       |
| `kafka.clusterCredentials` | path          | yes                                                           | Path to the destination Kafka leg credentials file, dialled directly to read destination-side offsets. Accepts `sasl_plain`, `sasl_scram`, `mtls`, `unauthenticated_tls`, or `unauthenticated_plaintext` — see below. |

The destination REST (cluster-link) credential is **not** on `spec.target.kafka`
— it lives at [`spec.clusterLink.linkCredentials`](#specclusterlink), separately
and always required.

`kafka.clusterCredentials` accepts any Kafka auth method **except** `iam` — the
destination is Confluent Cloud or Confluent Platform, never MSK, so `iam` is
rejected rather than silently accepted and then failing opaquely at
connection time. The rejection happens when `execute` (including `--dry-run`)
reads the credentials file — not when the manifest is parsed, and `lag-check`
never reads that file. As on the source leg, a `ca_cert` inside `sasl_plain`
names a private CA the client trusts directly, on top of (or instead of) the
public trust store `tls: true` selects.

Unlike the shared table above, a destination `sasl_plain` with **neither**
`ca_cert` nor `tls` set does not fall back to `SASL_PLAINTEXT`: it defaults to
`tls: true` against the public trust store — the destination is always a managed/production
cluster, never on-prem plaintext like a source may legitimately be. There is
no `sasl_plain` field that opts back into `SASL_PLAINTEXT` against the
destination; use `unauthenticated_plaintext` for a genuinely plaintext
destination (test/lab only).

## `spec.clusterLink`

| Field                     | Type       | Required | Default | Notes                                                                                                                                            |
| ------------------------- | ---------- | -------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `name`                    | string     | yes      | —       | Name of a cluster link that **already exists** on the destination. This kind never creates one.                                                  |
| `bootstrapServers`        | `[]string` | no       | —       | Repeats `spec.target.kafka.bootstrapServers` for manifest self-documentation. Not validated against it.                                          |
| `linkCredentials`         | path       | yes      | —       | Path to the destination REST (cluster-link) credentials file — see [REST credentials](#rest-credentials-specclusterlinklinkcredentials) below.  |
| `pauseConsumerOffsetSync` | bool       | no       | `false` | Static routes only; a dynamic route refuses it. Disable the link's `consumer.offset.sync.enable` right after fencing, and set it back to `consumerOffsetSyncBaseline` after the switch. If a run stops before that restore, the restore is still owed and re-running `execute` performs it. It is also restored if a failure before promote rolls the fence back. |
| `consumerOffsetSyncBaseline` | string | when pausing | — | `enabled` or `disabled`: the value `consumer.offset.sync.enable` is set back to after the switch. |

**Why two destination credentials at all?** `spec.target.kafka.clusterCredentials`
authenticates a direct Kafka-protocol connection to the destination
`bootstrapServers`; `spec.clusterLink.linkCredentials` authenticates HTTP calls
to `restEndpoint`'s Admin REST API, which is what actually manages the cluster
link (status, list/promote mirror topics, alter configs). These are two
different servers speaking two different protocols. On self-managed Confluent
Platform they are backed by two independent credential stores the operator
configures separately on the cluster: the Kafka SASL/SCRAM (or mTLS) listener
has its own store, while the REST API's auth (`kafka.rest.authentication.method`
— a Basic realm file, MDS for bearer, or its own mTLS trust store) has another. A
valid Kafka credential is **not** automatically a valid REST credential, even
against the same broker process. `linkCredentials` is therefore **always
required** and never derived from the Kafka leg — its top-level shape is one of
`api_key`/`api_secret`, `basic`, `bearer`, or `mtls` (see the
[REST credentials table](#rest-credentials-specclusterlinklinkcredentials)).

## `spec.gateway`

There is no `crs.fenced` or `crs.switchover` file: kcp derives both the fenced
CR and the switched CR from the live initial CR at cutover. On a static route
that is a route-level fence added to the route named in `spec.route`, then that
route's `streamingDomain` flipped to its declared target; on a dynamic route it
is a `rules.fencing` entry for the selected topics, then a routing condition
sending those topics to the target domain. The old `crs.initial` nesting
is flattened to a single `cr-name`, and the retired `crs`/`routes` keys are
removed from the schema entirely: a stale manifest that still uses them fails
the strict decode with an unknown-field error.

| Field        | Type   | Required | Notes                                                                                               |
| ------------ | ------ | -------- | --------------------------------------------------------------------------------------------------- |
| `namespace`  | string | yes      | Kubernetes namespace where the gateway is deployed.                                                 |
| `kubeconfig` | string | no       | Path to the kubeconfig to use. The **one** field in this manifest where a leading `~/` is expanded. Unset: the pod's in-cluster service account when kcp runs in a pod, `~/.kube/config` otherwise. |
| `cr-name`    | string | yes      | The **name** of the initial gateway custom resource — read live from the cluster on each `execute` run, not a file path. |

## `spec.route`

Required. Names the route to fence and switch over, the target streaming
domain it switches to, and either the topic selection(s) that migrate or, for
a conversion, `convertTo`.

| Field                   | Type       | Required | Notes                                                                                                                              |
| ----------------------- | ---------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `name`                  | string     | yes      | A `spec.routes[].name` in the initial CR to fence and switch over. Must be non-blank (checked when the manifest is read) and exist in the CR (checked at reconcile).                            |
| `topicGroup`            | list       | yes, unless `convertTo` is set | A list validated to **exactly one** entry today (one route, one migration per file). Must not be set with `convertTo`. See below. |
| `convertTo`             | string     | no       | `static` is the only value. Converts the dynamic route to a static route bound to `targetStreamingDomain` instead of migrating topics; mutually exclusive with `topicGroup`. See [Converting a dynamic route to static](#converting-a-dynamic-route-to-static-specrouteconvertto). |
| `targetStreamingDomain` | string     | yes      | The streaming domain this route switches to. On a **static** route it must be declared in the initial CR's `spec.streamingDomains` with exactly one bootstrap server id, and the route must already carry pre-staged `security.cluster.<domain>` auth for it (a `secretStore` and an `authentication` block, with the referenced Secrets present). On a **dynamic** route it must be one of the two domains the route binds. For a **conversion** (`convertTo`) it must be one of the two domains the dynamic route binds, and that binding must carry a `bootstrapServerId`: the static route binds to it.    |

### `spec.route.topicGroup`

| Field           | Type       | Required | Notes                                                                                                            |
| --------------- | ---------- | -------- | ------------------------------------------------------------------------------------------------------------------ |
| `topics`        | `[]string` | see note | A flat list of **literal** topic names — **not** globs. A glob is not rejected when the manifest is read; it fails at reconcile as "not found on the source".                                                          |
| `topicPatterns` | `[]string` | see note | A list of **anchored full-match** regular expressions (RE2), matched against the topic names on the source cluster. Case-sensitive, and the whole topic name must match; `.` matches any character, so escape it (`\.`) to match a literal dot. `['.*']` selects every source topic. |

**At least one of `topics` / `topicPatterns` is required.** When both are set,
the union is migrated: every literal name plus every source topic a pattern
matches. Every selected topic must be in a state kcp can migrate or resume: a topic
that is not on the source, not on the cluster link, present on the destination
without being a mirror, or whose mirror is in a failed or unexpected state
refuses the run, while a topic an earlier run already promoted or switched is
resumed or skipped. The refusal is all-or-nothing — nothing is fenced or
changed. If nothing is left to migrate, `execute` reports that and does
nothing. There is no
omit-`topics`-means-all default: to migrate every source topic, write an
explicit match-all pattern, `topicPatterns: ['.*']`.

`topicPatterns` are resolved against the live source on every run, so the
selected set can change between runs without the manifest changing. On a
dynamic route that matters when resuming an interrupted migration — see
[Execution model](#execution-model).

The route's **migration mode** — all-at-once (static) vs topic-based (dynamic)
— is **not** declared here; kcp reads it from the live CR on every execute
run. It uses the route's own `mode` field when the CR sets one; otherwise it
infers it from the binding: a named singular `streamingDomain` ⇒ static, a
non-empty plural `streamingDomains` ⇒ dynamic. A route with both or neither is
refused. On a static route the **bootstrap server id** is **derived** from the
target domain's declaration in the live CR, not written in the manifest.
`kcp migration execute` runs the workflow that matches the route's mode: a
whole-route cutover for a static route, topic by topic for a dynamic route.

A dynamic route has two further requirements, both checked at reconcile (so
`--dry-run` reports them): it must bind exactly two streaming domains (the
source and the target), with `coordination.group` pinned to the source domain,
and the cluster link's consumer offset sync must already be disabled, so
`spec.clusterLink.pauseConsumerOffsetSync` is refused on a dynamic route.

`lag-check` ignores the topic selection entirely and always watches every mirror
topic.

## Converting a dynamic route to static (`spec.route.convertTo`)

A topic-based migration leaves its route dynamic: each migrated topic is
routed to the destination by a routing condition, and consumer-group
coordination stays pinned to the source. Once every topic on the cluster link
is migrated, `convertTo: static` replaces the dynamic route with a static route
bound to the destination and copies the consumer groups' committed offsets
there, so group coordination moves too. Run it when the last batch has
switched.

```yaml
route:
  name: migration-route
  convertTo: static
  targetStreamingDomain: confluent-cloud
```

A conversion selects no topics: the cluster link is its scope. The rest of
the manifest is the topic-based migration's; see the
[conversion example](#example-manifest).

### What is checked

`execute` and `--dry-run` check, against the live gateway CR, both clusters
and the cluster link:

- **The route.** It is dynamic and binds exactly two streaming domains, with
  `coordination.group` on the source; `targetStreamingDomain` is one of them
  and its binding carries a `bootstrapServerId`; the route carries
  `security.cluster.<targetStreamingDomain>` auth; it has no fence kcp didn't
  write and no route-level fence. The cluster link's consumer offset sync is
  disabled.
- **The cluster link is the scope.** Every mirror topic on the link must be
  promoted (`STOPPED`), named as on the source (no `cluster.link.prefix`),
  present on the destination with the same partition count as on the source,
  and routed to the destination by the dynamic route. At least one topic must
  be promoted.
- **Consumer groups.** A source group whose committed offsets are all on link
  topics is in scope, and its offsets are copied. A group with any committed
  offset on a topic that is not on the link refuses the conversion: after the
  switch that topic is not reachable through the route, or the group belongs
  to a client outside it. An in-scope group with live members on the
  destination refuses too; one that exists there with no members gets a
  warning, because its offsets will be overwritten. Source topics that are not
  on the link and that no group commits on get a warning: after the switch the
  route sends their traffic to the destination.
- **Credentials.** The source and destination credentials must be able to
  list every consumer group and describe every topic (a credential that sees
  part of a cluster would hide a group or a topic from these checks), and the
  destination credential must be able to write consumer-group offsets (`READ`
  on the groups and the topics). On MSK IAM the actions these probes need have
  not been verified yet.

### What it does

1. **Fence.** kcp adds its conversion fence, `{topicPatterns: ['.*'],
   blocked: true}`, at the head of the route's `rules.fencing`, blocking every
   topic on the route.
2. **Verify.** On data read after the fence, kcp re-runs the route checks
   that decide whether the route can be converted (dynamic, two domains,
   `coordination.group` on the source, `targetStreamingDomain` one of them,
   consumer offset sync disabled), confirms its own conversion fence is still
   on the route, and re-runs the credential, cluster-link scope and
   consumer-group checks. It does not repeat the checks on the route's
   contents (the target binding's `bootstrapServerId`, the
   `security.cluster.<targetStreamingDomain>` auth, no fence kcp didn't write,
   no route-level fence) or the warnings about topics outside the link; those
   run before the fence. It then takes two snapshots of every source group's
   committed offsets, `detectUnroutedCommitsDuration` apart (`30s` by default;
   it can't be skipped). A change means a client is committing to the source directly,
   bypassing the gateway, and the run fails.
3. **Sync offsets.** kcp writes the in-scope groups' committed offsets, with
   their metadata, to the destination. It refuses the whole sync if an offset
   is past its destination partition's high-water mark.
4. **Switch.** kcp replaces the route with a static route bound to
   `targetStreamingDomain`, with no `rules` (so no fence) and no
   `streamingDomains`; `security` and the rest of the route are kept.

**The switch is the point of no return.** A failure before it removes the
fence and leaves the route as it was; offsets already written to the
destination have no effect until the switch. Each gateway change is confirmed
on every gateway pod, as in a migration.

### What clients see

- **A pause.** Every topic on the route is blocked from the fence to the
  switch: about `detectUnroutedCommitsDuration` plus two gateway reloads.
  Producers retry (give them a `delivery.timeout.ms` longer than that);
  consumers fetch nothing and can't commit.
- **A rejoin, and possibly a re-read.** After the switch, consumers' group
  coordinator is on the destination. Each consumer's first commit or heartbeat
  there fails (the destination doesn't know its member), so it
  rejoins its group and resumes from the copied offset. It may read again what
  it fetched between the switch and that first failed commit or heartbeat — up
  to one poll (`max.poll.records`) for a consumer that commits after every
  batch, and up to the backlog the fence built for one that auto-commits — and
  what it consumed just before the fence but couldn't commit. This is expected
  at-least-once behaviour; nothing is skipped.
- **KIP-848 consumers** (`group.protocol=consumer`) can't use a dynamic route
  at all; they work once the route is static.

### Refusals and how to fix them

| Refusal | Fix |
|---|---|
| A link topic's mirror is `ACTIVE`, not promoted | Migrate it in a topic-based migration batch, or promote it if nothing uses it. |
| A link topic's promotion is still in progress | Wait for it to reach `STOPPED`, then re-run. |
| The route sends a link topic to the source | Finish that topic's batch (re-run `execute` on its manifest) before converting. |
| A link topic has more partitions on the destination than on the source | Add partitions to the source copy to match, set the affected groups' offsets on the new partitions, then re-run. |
| A mirror is named differently on the destination | Conversions don't support a cluster link with `cluster.link.prefix`. |
| No topic on the link is promoted | Migrate the route's topics first: a conversion closes a topic-based migration. |
| A group commits on a topic that is not on the link | Migrate that topic; or delete the group's stale offsets on it (`kafka-consumer-groups --delete-offsets`), or the group; or stop or move the application outside this route that owns it. |
| An in-scope group has members on the destination | Stop the destination members first. |
| A credential can't list every group, describe every topic, or write offsets | Grant what the refusal names, then re-run. |
| The route has a fence kcp didn't write, or a route-level fence | Remove it: the conversion would drop the first, and the static route would enforce the second. |
| Committed offsets changed after the fence | A client commits to the source directly. Send it through the gateway, then re-run. |

A refusal before the fence changes nothing, and `--dry-run` shows it.

### Interruptions

- **A killed run leaves the fence up.** kcp has no signal handler, so the
  route keeps blocking every topic until you re-run the same manifest, which
  resumes.
- **A refusal with the fence up warns.** If a re-run is refused while kcp's
  fence is on the route, kcp says so: the route keeps blocking every topic
  until you fix the refusal and re-run, or remove kcp's entry from
  `rules.fencing` by hand.
- **A failed switch keeps the fence.** If the static route never reached the
  gateway CR, fix the cause and re-run to finish. If it reached the CR but
  couldn't be confirmed, check the Gateway (operator acceptance, pods) instead
  of re-running: a re-run would report nothing to do.
- **A finished conversion re-runs as nothing to do.**

## `spec.defaultPolicies`

Optional. Every field is a default that a matching `kcp migration execute` flag
can override for a single run; the section is re-read fresh from the manifest
on every `execute`, never fixed once. Duration values need a unit — `0s`,
`90s`, `10m`; in the manifest a bare number such as `0` or `90` is a parse
error (the command-line flags do accept a plain `0`). Negative values are
rejected.

| Field                             | Type     | Default | Override flag                           | Notes                                                                                                                                                                                                                                                                          |
| --------------------------------- | -------- | ------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `lagThreshold`                    | int      | `0`     | `--lag-threshold`                       | Replication lag tolerated per topic: each selected topic's lag (the sum of its partition lags) must be at or below this before the run fences. `0` is the strictest (fully caught up).                                                                                        |
| `promoteBatchSize`                | int      | `0`     | `--promote-batch-size`                  | Max mirror topics promoted per batch. `0` promotes all at once; when set, each batch is promoted and confirmed stopped before the next is submitted.                                                                                                                           |
| `rolloutTimeout`                  | duration | `0s`    | `--rollout-timeout`                     | Max wait for the operator to report the gateway `Ready` during fence and switchover (e.g. `10m`). `0s` means no deadline — waits until convergence or cancellation.                                                                                                            |
| `detectUnroutedProducersDuration` | duration | `0s`    | `--detect-unrouted-producers-duration`  | After fencing, kcp takes two source-offset snapshots this far apart to catch producers still bypassing the gateway; if any partition's offset advanced, the run stops before promote. `0s` **skips the check entirely** (the default); minimum `10s` when set — shorter can't span a producer's metadata refresh. Ignored by a route conversion, which uses `detectUnroutedCommitsDuration` instead. |
| `consumerOffsetSyncDrainDuration` | duration | `0s`    | `--consumer-offset-sync-drain-duration` | Static routes only. Wait after fencing, before disabling the link's consumer offset sync, letting final offsets propagate. Has no effect unless `pauseConsumerOffsetSync` is set (which a dynamic route refuses). `0s` means no wait.                                           |
| `hotReloadTimeout`                | duration | `0s`    | `--hot-reload-timeout`                  | Max wait for every gateway pod to report the new config revision when the gateway supports hot-reload (e.g. `90s`). Unlike `rolloutTimeout` this is never unbounded: a hot-reload moves no Kubernetes signal, so `0s` uses the built-in 90s budget rather than waiting forever. |
| `gatewayConfigPort`               | int      | `0`     | `--gateway-config-port`                 | Port serving the gateway's `/config` endpoint, polled per pod to confirm a config revision was applied. `0` uses the gateway default (`9180`).                                                                                                                                 |
| `detectUnroutedCommitsDuration`   | duration | `0s`    | `--detect-unrouted-commits-duration`    | Route conversion only. Window between the two committed-offset snapshots taken after fencing, to catch a consumer committing to the source directly, bypassing the gateway. `0s` uses the built-in `30s` (this check can never be skipped); minimum `10s` when set. Ignored by AAO/TBM. |
| `offsetSyncConcurrency`           | int      | `0`     | `--offset-sync-concurrency`             | Route conversion only. Number of workers, each with its own broker connection, reading and writing consumer-group offsets. `0` uses the built-in `8`. Ignored by AAO/TBM. |

## Credentials

Every `credentials`-shaped slot (`spec.source.credentials`,
`spec.target.kafka.clusterCredentials`, `spec.clusterLink.linkCredentials`) is a
**path to an external credentials file** — inline credential blocks are not
accepted, so the manifest itself never holds secret material:

```yaml
credentials: /etc/kcp/source-creds.yaml
```

The referenced file's top-level content is the credential (a Kafka-family block
for the Kafka legs, a REST block for `linkCredentials`). Prefer a credential
mounted as a Kubernetes Secret file. Each credentials file is secret-bearing —
`kcp` warns (not errors) if it is group- or world-readable, so keep it `0600`.

### Kafka credentials (`spec.source.credentials`, `spec.target.kafka.clusterCredentials`)

Specify **exactly one** method block — its **presence** selects it (no
`auth_method:` wrapper, no `use:` flag). An optional top-level
`insecure_skip_tls_verify: false` sibling applies to test environments only,
and takes effect only where a TLS handshake happens (not for
`unauthenticated_plaintext` or `sasl_plain` over plaintext). REST credentials
use a different key, `insecure_skip_verify`, inside their block.

| Method                      | Required fields             | Notes                                                                                                                                                         |
| --------------------------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `iam`                       | `region`                    | MSK source only; Confluent Cloud can't present IAM (a link to MSK uses SCRAM instead).                                                                        |
| `sasl_scram`                | `username`, `password`, `mechanism` | `mechanism` is `SHA256` or `SHA512` (`SCRAM-SHA-256` / `SCRAM-SHA-512` are accepted too; MSK requires `SHA512`) and must be set — an omitted mechanism is rejected. Optional `ca_cert`. Always TLS (SASL_SSL). |
| `sasl_plain`                | `username`, `password`      | Optional `ca_cert`, `tls`. `ca_cert` present ⇒ SASL_SSL against that CA; `tls: true` ⇒ SASL_SSL over the system/public trust store; neither ⇒ SASL_PLAINTEXT on the source leg (the destination leg always uses TLS — see [`spec.target`](#spectarget)). |
| `mtls`                      | `client_cert`, `client_key` | Optional `ca_cert`. Client is authenticated via its certificate.                                                                                              |
| `unauthenticated_tls`       | —                           | Optional `ca_cert`. One-way TLS; client is not authenticated.                                                                                                 |
| `unauthenticated_plaintext` | —                           | Written `unauthenticated_plaintext: {}` — a block is selected by its presence, so a bare key with no value is rejected. No auth, no TLS. Test/lab only.                                                                                                                               |

`ca_cert` (on `sasl_scram`, `sasl_plain`, `mtls`, `unauthenticated_tls`) is a
PEM file path used to verify the broker's TLS certificate. Supply it only for a
**private/internal CA**; public-CA brokers (AWS MSK, Confluent Cloud) validate
against the system trust store and need no `ca_cert`. Every `ca_cert`,
`client_cert` and `client_key` path must exist when the credentials file is
read.

Examples — each is the complete contents of a credentials file:

SASL/SCRAM (e.g. an MSK source):

```yaml
sasl_scram:
  username: kcp-migration
  password: <password>
  mechanism: SHA512
```

mTLS, with a private CA:

```yaml
mtls:
  client_cert: /etc/kcp/certs/client.crt
  client_key: /etc/kcp/certs/client.key
  ca_cert: /etc/kcp/certs/ca.crt
```

IAM (MSK source only):

```yaml
iam:
  region: us-east-1
```

### REST credentials (`spec.clusterLink.linkCredentials`)

Specify **exactly one** block (or the `api_key`/`api_secret` pair).

| Method                   | Required fields             | Notes                                            |
| ------------------------ | --------------------------- | ------------------------------------------------ |
| `api_key` + `api_secret` | `api_key`, `api_secret`     | Confluent Cloud; flat top-level pair; public CA. |
| `basic`                  | `username`, `password`      | e.g. Confluent Platform MDS.                     |
| `bearer`                 | `token`                     | e.g. MDS/OAuth.                                  |
| `mtls`                   | `client_cert`, `client_key` | Auth at the TLS layer.                           |

`basic`, `bearer`, and `mtls` each accept an optional `ca_cert` and
`insecure_skip_verify` inside their block to reach a TLS endpoint fronted by a
private/internal CA (e.g. self-managed CP/MDS). The flat `api_key` form takes
the same two keys at the top level instead, and they are rejected alongside
the block forms. Public-CA endpoints (Confluent Cloud) need neither. `basic`
does not check at load time that `username` and `password` are non-empty; a
missing value surfaces as an authentication error at runtime.

Examples — each is the complete contents of a credentials file:

Confluent Cloud (`api_key` + `api_secret`):

```yaml
api_key: <api-key>
api_secret: <api-secret>
```

Self-managed Confluent Platform, with a private CA (`basic`):

```yaml
basic:
  username: kcp-migration
  password: <password>
  ca_cert: /etc/kcp/certs/ca.crt
```

One restriction is specific to **this** manifest, narrower than the two
tables above:

- `spec.source.credentials.iam` is valid only when `spec.source.type: msk`.
  `spec.target.kafka.clusterCredentials.iam` is rejected outright — the
  destination is Confluent Cloud/Platform, never MSK.

## How the commands read this file

| Command                   | Flag                                                                                                                                                                                             | Required                            | Notes                                                                                                                                    |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `kcp migration execute`   | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest.                                                                                                                   |
|                           | `--dry-run`                                                                                                                                                                                      | no                                  | Run only the reconcile step and print the plan; change nothing. Exits non-zero if the plan is refused.                                   |
|                           | `--lag-threshold`, `--promote-batch-size`, `--rollout-timeout`, `--detect-unrouted-producers-duration`, `--consumer-offset-sync-drain-duration`, `--hot-reload-timeout`, `--gateway-config-port`, `--detect-unrouted-commits-duration`, `--offset-sync-concurrency` | no                                  | Per-run overrides of the matching `spec.defaultPolicies` field for this run only.                                                        |
| `kcp migration lag-check` | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest. Reads only the destination REST leg (`spec.clusterLink.linkCredentials`), honoured in whichever form it resolves — `api_key`/`basic`/`bearer`/`mtls`; it never dials the source or destination Kafka legs. |
|                           | `--poll-interval`                                                                                                                                                                                | no (default `1`)                    | Poll interval in seconds. Values outside `1`-`60` are clamped to that range.                                                                                                      |

Every path in the manifest resolves relative to the **process working
directory**, not the manifest's own location — the one exception is
`spec.gateway.kubeconfig`, where a leading `~/` is expanded. The same applies
to the certificate and key paths inside a credentials file.

Every flag can also be set through an upper-case environment variable of the
same name with dashes as underscores (`--lag-threshold` is `LAG_THRESHOLD`,
`--dry-run` is `DRY_RUN`, `--poll-interval` is `POLL_INTERVAL`); an explicit
flag wins. A policy value set through an environment variable overrides the
manifest exactly as the flag would, so a stray `LAG_THRESHOLD` in the shell
silently changes a run. The overrides are validated with the rest of the
effective policy, including under `--dry-run`.

## Validation

There is no separate `kcp migration validate` subcommand. Every read of this
manifest — by `execute` or `lag-check` — parses and structurally
validates in one step, so both commands get validation automatically and
neither can skip it by accident.

Validation collects **every** problem before returning, rather than
fail-fast, so an operator fixes the file in one pass:

```
3 problem(s) found in the migration manifest:
  - spec.source.bootstrapServers: must not be empty
  - spec.target.clusterId: required for target type "confluent-cloud"
  - spec.defaultPolicies.detectUnroutedProducersDuration: must be at least 10s when set (0 skips the check)
```

Key rules, beyond required/optional per field above:

- `apiVersion`/`kind` must be the exact literals; `metadata.name` non-blank.
- `spec.source.type` and `spec.target.type` must be one of their listed enum
  values.
- Every `bootstrapServers` entry must be `host:port`.
- `spec.clusterLink.consumerOffsetSyncBaseline` is required when
  `pauseConsumerOffsetSync` is set, and if set at all must be `enabled` or
  `disabled`.
- `spec.clusterLink.name` must not be blank (existence itself isn't checked
  until the first `execute` run touches the destination); `spec.clusterLink.linkCredentials` must
  not be blank.
- Every credentials field (`spec.source.credentials`,
  `spec.target.kafka.clusterCredentials`, `spec.clusterLink.linkCredentials`)
  must be a non-blank file path; an inline mapping is rejected at parse.
- `spec.gateway.namespace` and `cr-name` must not be blank; the retired
  `crs`/`routes` keys must not be set at all (they fail the strict decode).
- `spec.route.name` and `spec.route.targetStreamingDomain` must not be blank;
  unless `spec.route.convertTo` is set, `spec.route.topicGroup` must have
  exactly one entry.
- `spec.route.convertTo`, if set, must be `static`, and `spec.route.topicGroup`
  must not be set with it.
- Each entry must set at least one of `topics` / `topicPatterns` (both is
  allowed). Neither present is rejected. `topics`/`topicPatterns`, if present,
  must be non-empty with no blank entries; each `topicPatterns` entry must
  compile as an anchored RE2 regular expression.
- Duration policies are written with a unit (`0s`, `10m`); a bare number is a
  parse error.
- No `spec.defaultPolicies` field may be negative;
  `detectUnroutedProducersDuration` and `detectUnroutedCommitsDuration`, if
  greater than zero, must be at least `10s`.

Rules that need the contents of a credentials file run when that file is read,
not when the manifest is parsed: the `iam`-needs-an-`msk`-source rule, the
rejection of `iam` on the destination, and the credentials file's own
validation. `execute` (including `--dry-run`) reads the source, destination and
REST credentials files; `lag-check` reads only the REST one.

`kcp migration execute --dry-run` performs the same validation `execute` would (manifest structure, credentials, the effective policy, and the reconcile preconditions against the live gateway CR, clusters and cluster link) and prints a reconcile plan, but takes no actions and runs no migration steps — useful for validating your infrastructure and manifest while iterating before scheduling a live cutover.

## Field reference

| Path                                                      | Type           | Required                                               | Default                                      | Allowed values                                               |
| --------------------------------------------------------- | -------------- | ------------------------------------------------------ | -------------------------------------------- | ------------------------------------------------------------ |
| `apiVersion`                                              | string         | yes                                                    | —                                            | `kcp.confluent.io/v1alpha1`                                  |
| `kind`                                                    | string         | yes                                                    | —                                            | `GatewayMigration`                                           |
| `metadata.name`                                           | string         | yes                                                    | —                                            | non-blank                                                    |
| `spec.source.type`                                        | enum           | yes                                                    | —                                            | `msk`, `apache-kafka`                                        |
| `spec.source.bootstrapServers`                            | `[]string`     | yes                                                    | —                                            | `host:port`                                                  |
| `spec.source.credentials`                                 | path           | yes                                                    | —                                            | file path; Kafka family, `iam` only if `type: msk`          |
| `spec.target.type`                                        | enum           | yes                                                    | —                                            | `confluent-cloud`, `confluent-platform`                      |
| `spec.target.clusterId`                                   | string         | yes                                                    | —                                            | required for both target types                               |
| `spec.target.kafka.bootstrapServers`                      | `[]string`     | yes                                                    | —                                            | `host:port`                                                  |
| `spec.target.kafka.restEndpoint`                          | string         | yes                                                    | —                                            | non-blank (not parsed as a URL)                              |
| `spec.target.kafka.clusterCredentials`                    | path           | yes                                                    | —                                            | file path; Kafka family except `iam`                        |
| `spec.clusterLink.name`                                   | string         | yes                                                    | —                                            | must reference an existing link                              |
| `spec.clusterLink.bootstrapServers`                       | `[]string`     | no                                                     | —                                            | repeats `spec.target.kafka.bootstrapServers`; unvalidated   |
| `spec.clusterLink.linkCredentials`                        | path           | yes                                                    | —                                            | file path; `api_key`/`api_secret`, `basic`, `bearer`, or `mtls` |
| `spec.clusterLink.pauseConsumerOffsetSync`                | bool           | no                                                     | `false`                                      | —                                                            |
| `spec.clusterLink.consumerOffsetSyncBaseline`             | string         | when `pauseConsumerOffsetSync` is set                  | —                                            | `enabled`, `disabled`                                        |
| `spec.gateway.namespace`                                  | string         | yes                                                    | —                                            | —                                                            |
| `spec.gateway.kubeconfig`                                 | string         | no                                                     | in-cluster in a pod, else `~/.kube/config`   | `~/` expanded                                                |
| `spec.gateway.cr-name`                                    | string         | yes                                                    | —                                            | K8s object name                                              |
| `spec.route.name`                                         | string         | yes                                                    | —                                            | must exist in the initial CR                                 |
| `spec.route.topicGroup`                                   | list           | unless `convertTo` is set                              | —                                            | exactly one entry; not with `convertTo`                     |
| `spec.route.convertTo`                                    | string         | no                                                     | —                                            | `static`                                                     |
| `spec.route.topicGroup[].topics`                          | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, literal names                         |
| `spec.route.topicGroup[].topicPatterns`                   | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, anchored RE2 patterns (`['.*']` = all) |
| `spec.route.targetStreamingDomain`                        | string         | yes                                                    | —                                            | must be declared in the initial CR's `spec.streamingDomains` |
| `spec.defaultPolicies.lagThreshold`                       | int            | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.promoteBatchSize`                   | int            | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.rolloutTimeout`                     | duration       | no                                                     | `0s`                                         | `>= 0s`                                                      |
| `spec.defaultPolicies.detectUnroutedProducersDuration`    | duration       | no                                                     | `0s`                                         | `0s`, or `>= 10s`                                            |
| `spec.defaultPolicies.consumerOffsetSyncDrainDuration`    | duration       | no                                                     | `0s`                                         | `>= 0s`                                                      |
| `spec.defaultPolicies.hotReloadTimeout`                   | duration       | no                                                     | `0s`                                         | `>= 0s`                                                      |
| `spec.defaultPolicies.gatewayConfigPort`                  | int            | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.detectUnroutedCommitsDuration`      | duration       | no                                                     | `0s`                                         | `0s`, or `>= 10s`                                           |
| `spec.defaultPolicies.offsetSyncConcurrency`              | int            | no                                                     | `0`                                          | `>= 0`                                                      |

## Example manifest

A fully-annotated, ready-to-copy `gateway-migration.yaml` — every field, with
comments. It is also available as a plain file at
[`gateway-examples/gateway-migration.yaml`](gateway-examples/gateway-migration.yaml)
(that link downloads the raw file rather than opening it in-browser).

```yaml
--8<-- "docs/assets/gateway-examples/gateway-migration.yaml"
```

A route conversion uses the same manifest with `spec.route.convertTo` in
place of `topicGroup` (see
[Converting a dynamic route to static](#converting-a-dynamic-route-to-static-specrouteconvertto)).
This shorter example is also available as a plain file at
[`gateway-examples/gateway-route-conversion.yaml`](gateway-examples/gateway-route-conversion.yaml).

```yaml
--8<-- "docs/assets/gateway-examples/gateway-route-conversion.yaml"
```

## Editor support

`gateway-examples/gateway-migration.yaml` carries a `# yaml-language-server:
$schema=…` modeline that fetches the manifest's JSON schema from the `main`
branch of the kcp repository on GitHub
(`internal/manifest/gatewaymigration.schema.json`), so VS Code with the
[Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
gives autocomplete and inline validation when it can reach GitHub. The schema
covers structure and types but does not enforce every rule — for example the
`10s` minimum and the `host:port` format are not checked by it — so `kcp
migration execute --dry-run` is the authoritative check.
