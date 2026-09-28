# Gateway migration manifest reference

`kcp migration execute|lag-check` drive the gateway-orchestrated cutover —
Confluent Gateway fencing a cluster link, batching mirror-topic promotion, and
switching production traffic over — from a single YAML manifest,
`gateway-migration.yaml`. This page is the field-by-field reference for that
manifest.

For a fully-annotated, ready-to-copy manifest — every field, with comments —
see [gateway migration example](gateway-migration-example.md).

## Execution model

This manifest drives an imperative, resumable state machine:

- **`execute`** validates the manifest and live infrastructure, reads the live initial gateway CR to resolve the route's mode and derive its bootstrap server id, and drives the fence → promote → switchover FSM forward. Every run reconciles live from the manifest and the current cluster state, so an interrupted run is safely continued by re-running the same command — it resumes from whatever the live world already reflects. It re-reads the manifest (topology and policy) on every invocation.
  - **`--dry-run`** validates the entire setup without changing anything: confirms the cluster link is active, all topics in the group are replicating, and the gateway CR exists and matches expectations. Nothing is changed and no FSM transitions occur. Useful for iterating on the manifest and author's infrastructure before scheduling a live cutover.
- **`lag-check`** polls mirror-topic replication lag independently of `execute`.

Every `execute` reconciles the current manifest live against the cluster. A completed migration re-reconciles to nothing to do — `execute` then runs no state machine and reports that — and an interrupted one continues from the live state. To migrate a different topology, edit the manifest (or use a new `metadata.name`) and run `execute` again.

**Resume an interrupted migration with the same topic set (dynamic routes).**
On a dynamic route, kcp recognises the fence it added by its exact topic set.
If a run is interrupted after fencing, resume it with a manifest that resolves
to the same topics. Resuming with a different set — a topic added or removed,
or a `topicPatterns` entry that now also matches a topic created on the source
since the interrupted run — is a different migration: kcp fences the new set,
treats the earlier fence as operator-authored, and leaves it on the route, so
those topics stay blocked after switchover. Finish the interrupted migration
first, then migrate the changed set. If this has already happened, remove the
leftover `blocked` entry from the route's `rules.fencing` by hand. Static
routes are unaffected: their fence is a single route-level block.

Each `spec.defaultPolicies` field is a default that a matching CLI flag can
override for a single run, without editing the file.

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
rejected outright rather than silently accepted and then failing opaquely at
connection time. Unlike the source leg, a `ca_cert` inside `sasl_plain` is
honoured here too: it names a private CA the destination client trusts
directly, on top of (or instead of) the public trust store `tls: true`
selects.

Unlike the shared table above, a destination `sasl_plain` with **neither**
`ca_cert` nor `tls` set does not fall back to `SASL_PLAINTEXT`: it defaults to
`tls: true` against the public trust store, matching the destination client's
pre-existing behavior — the destination is always a managed/production
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
| `pauseConsumerOffsetSync` | bool       | no       | `false` | Static routes only. Disable the link's `consumer.offset.sync.enable` right after fencing, and set it back to `consumerOffsetSyncBaseline` after the switch. If a run stops before that restore, the restore is still owed and re-running `execute` performs it. |
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
CR and the switched CR from the live initial CR at cutover — a fence block
injected onto the route named in `spec.route`, and that route's
`streamingDomain` flipped to its declared target. The old `crs.initial` nesting
is flattened to a single `cr-name`, and the retired `crs`/`routes` keys are
removed from the schema entirely: a stale manifest that still uses them fails
the strict decode with an unknown-field error.

| Field        | Type   | Required | Notes                                                                                               |
| ------------ | ------ | -------- | --------------------------------------------------------------------------------------------------- |
| `namespace`  | string | yes      | Kubernetes namespace where the gateway is deployed.                                                 |
| `kubeconfig` | string | no       | Path to the kubeconfig to use. The **one** field in this manifest where a leading `~/` is expanded. |
| `cr-name`    | string | yes      | The **name** of the initial gateway custom resource — read live from the cluster on each `execute` run, not a file path. |

## `spec.route`

Required. Names the route to fence and switch over, the target streaming
domain it switches to, and the topic selection(s) that migrate.

| Field                   | Type       | Required | Notes                                                                                                                              |
| ----------------------- | ---------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `name`                  | string     | yes      | A `spec.routes[].name` in the initial CR to fence and switch over. Must be non-blank and exist in the CR.                            |
| `topicGroup`            | list       | yes      | A list validated to **exactly one** entry today (one route, one migration per file). See below.                                     |
| `targetStreamingDomain` | string     | yes      | The streaming domain this route switches to once unfenced. Must already be declared in the initial CR's `spec.streamingDomains`.    |

### `spec.route.topicGroup`

| Field           | Type       | Required | Notes                                                                                                            |
| --------------- | ---------- | -------- | ------------------------------------------------------------------------------------------------------------------ |
| `topics`        | `[]string` | see note | A flat list of **literal** topic names, exact-matched against the link's active mirror topics — **not** globs.    |
| `topicPatterns` | `[]string` | see note | A list of **anchored full-match** regular expressions (RE2). `['.*']` selects every active mirror topic.          |

**At least one of `topics` / `topicPatterns` is required.** If `topics` is set
it is authoritative — `topicPatterns` is ignored. There is no
omit-`topics`-means-all default anymore: to migrate every active mirror topic,
write an explicit match-all pattern, `topicPatterns: ['.*']`, with `topics`
absent.

`topicPatterns` are resolved against the live source on every run, so the
selected set can change between runs without the manifest changing. On a
dynamic route that matters when resuming an interrupted migration — see
[Execution model](#execution-model).

The route's **migration mode** — all-at-once (static) vs topic-based (dynamic)
— is **not** declared here; kcp reads it from the live CR's route binding on
every execute run (a singular `streamingDomain` ⇒ static, a plural `streamingDomains` ⇒
dynamic). The **bootstrap server id** the route binds to is likewise **derived**
from the target domain's declaration in the live CR, not written in
the manifest. `kcp migration execute` resolves the mode from the live gateway CR
each run and dispatches to the matching engine and FSM — AAO's for static routes,
TBM's for dynamic. A dynamic-mode migration refuses
`spec.clusterLink.pauseConsumerOffsetSync`: a dynamic route requires consumer
offset sync to be disabled.

`lag-check` ignores the topic selection entirely and always watches every mirror
topic.

## `spec.defaultPolicies`

Optional. Every field is a default that a matching `kcp migration execute` flag
can override for a single run; the section is re-read fresh from the manifest
on every `execute`, never fixed once.

| Field                             | Type     | Default | Override flag                           | Notes                                                                                                                                                                                                                                                                          |
| --------------------------------- | -------- | ------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `lagThreshold`                    | int      | `0`     | `--lag-threshold`                       | Total replication lag (sum of all partition lags) tolerated before proceeding. `0` is the strictest.                                                                                                                                                                           |
| `promoteBatchSize`                | int      | `0`     | `--promote-batch-size`                  | Max mirror topics promoted per batch. `0` promotes all at once; when set, each batch is promoted and confirmed stopped before the next is submitted.                                                                                                                           |
| `rolloutTimeout`                  | duration | `0`     | `--rollout-timeout`                     | Max wait for the operator to report the gateway `Ready` during fence and switchover (e.g. `10m`). `0` means no deadline — waits until convergence or cancellation.                                                                                                             |
| `detectUnroutedProducersDuration` | duration | `0`     | `--detect-unrouted-producers-duration`  | Window to monitor source offsets after fencing for producers still bypassing the gateway; a detected increase aborts before switchover. `0` **skips the check entirely**; minimum `10s` when set — shorter can't span a producer's metadata refresh.                           |
| `consumerOffsetSyncDrainDuration` | duration | `0`     | `--consumer-offset-sync-drain-duration` | Wait after fencing, before disabling the link's consumer offset sync, letting final offsets propagate. Has no effect unless `pauseConsumerOffsetSync` is set. `0` means no wait.                                                                                               |
| `hotReloadTimeout`                | duration | `0`     | `--hot-reload-timeout`                  | Max wait for every gateway pod to report the new config revision when the gateway supports hot-reload (e.g. `90s`). Unlike `rolloutTimeout` this is never unbounded: a hot-reload moves no Kubernetes signal, so `0` uses the built-in 90s budget rather than waiting forever. |
| `gatewayConfigPort`               | int      | `0`     | `--gateway-config-port`                 | Port serving the gateway's `/config` endpoint, polled per pod to confirm a config revision was applied. `0` uses the gateway default (`9180`).                                                                                                                                 |

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
`insecure_skip_tls_verify: false` sibling applies to test environments only.

| Method                      | Required fields             | Notes                                                                                                                                                         |
| --------------------------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `iam`                       | `region`                    | MSK source only; Confluent Cloud can't present IAM (a link to MSK uses SCRAM instead).                                                                        |
| `sasl_scram`                | `username`, `password`      | Optional `mechanism` (`SHA256`/`SHA512`; MSK requires `SHA512`), `ca_cert`. Always TLS (SASL_SSL).                                                            |
| `sasl_plain`                | `username`, `password`      | Optional `ca_cert`, `tls`. `ca_cert` present ⇒ SASL_SSL against that CA; `tls: true` ⇒ SASL_SSL over the system/public trust store; neither ⇒ SASL_PLAINTEXT. |
| `mtls`                      | `client_cert`, `client_key` | Optional `ca_cert`. Client is authenticated via its certificate.                                                                                              |
| `unauthenticated_tls`       | —                           | Optional `ca_cert`. One-way TLS; client is not authenticated.                                                                                                 |
| `unauthenticated_plaintext` | —                           | No auth, no TLS. Test/lab only.                                                                                                                               |

`ca_cert` (on `sasl_scram`, `sasl_plain`, `mtls`, `unauthenticated_tls`) is a
PEM file path used to verify the broker's TLS certificate. Supply it only for a
**private/internal CA**; public-CA brokers (AWS MSK, Confluent Cloud) validate
against the system trust store and need no `ca_cert`.

### REST credentials (`spec.clusterLink.linkCredentials`)

Specify **exactly one** block (or the `api_key`/`api_secret` pair).

| Method                   | Required fields             | Notes                                            |
| ------------------------ | --------------------------- | ------------------------------------------------ |
| `api_key` + `api_secret` | `api_key`, `api_secret`     | Confluent Cloud; flat top-level pair; public CA. |
| `basic`                  | `username`, `password`      | e.g. Confluent Platform MDS.                     |
| `bearer`                 | `token`                     | e.g. MDS/OAuth.                                  |
| `mtls`                   | `client_cert`, `client_key` | Auth at the TLS layer.                           |

`basic`, `bearer`, and `mtls` each accept an optional `ca_cert` and
`insecure_skip_verify` to reach a TLS endpoint fronted by a private/internal CA
(e.g. self-managed CP/MDS). Public-CA endpoints (Confluent Cloud via `api_key`)
need neither.

One restriction is specific to **this** manifest, narrower than the two
tables above:

- `spec.source.credentials.iam` is valid only when `spec.source.type: msk`.
  `spec.target.kafka.clusterCredentials.iam` is rejected outright — the
  destination is Confluent Cloud/Platform, never MSK.

## How the commands read this file

| Command                   | Flag                                                                                                                                                                                             | Required                            | Notes                                                                                                                                    |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `kcp migration execute`   | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest.                                                                                                                   |
|                           | `--lag-threshold`, `--promote-batch-size`, `--rollout-timeout`, `--detect-unrouted-producers-duration`, `--consumer-offset-sync-drain-duration`, `--hot-reload-timeout`, `--gateway-config-port` | no                                  | Per-run overrides of the matching `spec.defaultPolicies` field for this run only.                                                        |
| `kcp migration lag-check` | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest. Reads only the destination REST leg (`spec.clusterLink.linkCredentials`), honoured in whichever form it resolves — `api_key`/`basic`/`bearer`/`mtls`; it never dials the source or destination Kafka legs. |
|                           | `--poll-interval`                                                                                                                                                                                | no (default `1`)                    | Poll interval in seconds, `1`-`60`.                                                                                                      |

Every path in the manifest resolves relative to the **process working
directory**, not the manifest's own location — the one exception is
`spec.gateway.kubeconfig`, where a leading `~/` is expanded.

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
- `spec.clusterLink.name` must not be blank (existence itself isn't checked
  until the first `execute` run touches the destination); `spec.clusterLink.linkCredentials` must
  not be blank.
- Every credentials field (`spec.source.credentials`,
  `spec.target.kafka.clusterCredentials`, `spec.clusterLink.linkCredentials`)
  must be a non-blank file path; an inline mapping is rejected at parse.
- `spec.gateway.namespace` and `cr-name` must not be blank; the retired
  `crs`/`routes` keys must not be set at all (they fail the strict decode).
- `spec.route.name` and `spec.route.targetStreamingDomain` must not be blank;
  `spec.route.topicGroup` must have exactly one entry.
- Each entry must set at least one of `topics` / `topicPatterns` (both is
  allowed). Neither present is rejected. `topics`/`topicPatterns`, if present,
  must be non-empty with no blank entries; each `topicPatterns` entry must
  compile as an anchored RE2 regular expression.
- No `spec.defaultPolicies` field may be negative;
  `detectUnroutedProducersDuration`, if greater than zero, must be at least
  `10s`.

`kcp migration execute --dry-run` performs the same validation `execute` would (manifest structure, credentials, cluster link and gateway CR existence/health) and prints a reconcile plan, but takes no actions and runs no FSM transitions — useful for validating your infrastructure and manifest while iterating before scheduling a live cutover.

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
| `spec.target.kafka.restEndpoint`                          | string         | yes                                                    | —                                            | URL                                                          |
| `spec.target.kafka.clusterCredentials`                    | path           | yes                                                    | —                                            | file path; Kafka family except `iam`                        |
| `spec.clusterLink.name`                                   | string         | yes                                                    | —                                            | must reference an existing link                              |
| `spec.clusterLink.bootstrapServers`                       | `[]string`     | no                                                     | —                                            | repeats `spec.target.kafka.bootstrapServers`; unvalidated   |
| `spec.clusterLink.linkCredentials`                        | path           | yes                                                    | —                                            | file path; `api_key`/`api_secret`, `basic`, `bearer`, or `mtls` |
| `spec.clusterLink.pauseConsumerOffsetSync`                | bool           | no                                                     | `false`                                      | —                                                            |
| `spec.gateway.namespace`                                  | string         | yes                                                    | —                                            | —                                                            |
| `spec.gateway.kubeconfig`                                 | string         | no                                                     | —                                            | `~/` expanded                                                |
| `spec.gateway.cr-name`                                    | string         | yes                                                    | —                                            | K8s object name                                              |
| `spec.route.name`                                         | string         | yes                                                    | —                                            | must exist in the initial CR                                 |
| `spec.route.topicGroup`                                   | list           | yes                                                    | —                                            | exactly one entry                                           |
| `spec.route.topicGroup[].topics`                          | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, literal names                         |
| `spec.route.topicGroup[].topicPatterns`                   | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, anchored RE2 patterns (`['.*']` = all) |
| `spec.route.targetStreamingDomain`                        | string         | yes                                                    | —                                            | must be declared in the initial CR's `spec.streamingDomains` |
| `spec.defaultPolicies.lagThreshold`                       | int            | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.promoteBatchSize`                   | int            | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.rolloutTimeout`                     | duration       | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.detectUnroutedProducersDuration`    | duration       | no                                                     | `0`                                          | `0`, or `>= 10s`                                             |
| `spec.defaultPolicies.consumerOffsetSyncDrainDuration`    | duration       | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.hotReloadTimeout`                   | duration       | no                                                     | `0`                                          | `>= 0`                                                       |
| `spec.defaultPolicies.gatewayConfigPort`                  | int            | no                                                     | `0`                                          | `>= 0`                                                       |

## Editor support

`gateway-examples/gateway-migration.yaml` carries a `# yaml-language-server:
$schema=…` modeline pointing at `internal/manifest/gatewaymigration.schema.json`,
so VS Code with the
[Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
gives autocomplete and inline validation automatically.
