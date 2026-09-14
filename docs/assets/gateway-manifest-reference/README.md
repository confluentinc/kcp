# Gateway migration manifest reference

`kcp migration init|execute|lag-check` drive the gateway-orchestrated cutover —
Confluent Gateway fencing a cluster link, batching mirror-topic promotion, and
switching production traffic over — from a single YAML manifest,
`gateway-migration.yaml`. This page is the field-by-field reference for that
manifest.

For a fully-annotated, ready-to-copy manifest — every field, with comments —
see [gateway migration example](gateway-migration-example.md).

## Execution model

This manifest drives an imperative, resumable state machine:

- **`init`** validates the manifest and the live infrastructure it describes
  (gateway, Kubernetes objects, credentials), then snapshots the topology into
  `migration-state.json`. It reads the live initial gateway CR to resolve the
  route's mode and derive its bootstrap server id. `--skip-validate` skips the
  credential resolution and the gateway/Kubernetes resource validation, but still
  reads the initial CR (the derived id is part of the snapshot) — useful for
  testing.
- **`execute`** drives the fence → promote → switchover FSM forward, resuming
  from wherever the state file says it left off. It re-reads the manifest on
  every invocation.
- **`lag-check`** polls mirror-topic replication lag independently of `execute`.

**Drift between the manifest and the init-time snapshot** is handled
automatically, gated on how far the migration has progressed — there is no flag
to force it through:

- Before the point of no return, any difference is a hard stop: re-run `init` to
  adopt the new spec.
- Past the point of no return, re-running `init` would discard FSM position and
  strand a live cutover, so `execute` instead proceeds on the edited spec with a
  loud warning.

`spec.defaultPolicies` is the one section re-read fresh on **every** `execute`
run rather than snapshotted at `init` — each field is a default that a matching
CLI flag can override for a single run, without editing the file.

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
  topicGroup: [...] # required — one entry: the topics, route, and target domain
  defaultPolicies: { ... } # optional — execute-time policy defaults
```

`apiVersion` must equal `kcp.confluent.io/v1alpha1` and `kind` must equal
`GatewayMigration`, exactly. `metadata.name` is required and non-blank: it is
written into the state file as the migration's identity (pre-manifest,
uuid-keyed migrations keep working, addressed instead with `--migration-id`).
`spec.source`, `spec.target`, `spec.clusterLink`, `spec.gateway`, and
`spec.topicGroup` are always required; `spec.defaultPolicies` is optional.

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
| `pauseConsumerOffsetSync` | bool       | no       | `false` | Disable the link's `consumer.offset.sync.enable` during execute and restore it after switchover. Requires the link to currently have it enabled. |

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
injected onto the route named in `spec.topicGroup`, and that route's
`streamingDomain` flipped to its declared target. The old `crs.initial` nesting
is flattened to a single `cr-name`, and the retired `crs`/`routes` keys are
removed from the schema entirely: a stale manifest that still uses them fails
the strict decode with an unknown-field error.

| Field        | Type   | Required | Notes                                                                                               |
| ------------ | ------ | -------- | --------------------------------------------------------------------------------------------------- |
| `namespace`  | string | yes      | Kubernetes namespace where the gateway is deployed.                                                 |
| `kubeconfig` | string | no       | Path to the kubeconfig to use. The **one** field in this manifest where a leading `~/` is expanded. |
| `cr-name`    | string | yes      | The **name** of the initial gateway custom resource — read live from the cluster at `init`, not a file path. |

## `spec.topicGroup`

Required — a list validated to **exactly one** entry today (one route, one
migration per file). Each entry pairs a topic selection with the route it
migrates and the target streaming domain that route switches to.

| Field                   | Type       | Required | Notes                                                                                                                                                    |
| ----------------------- | ---------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `topics`                | `[]string` | see note | A flat list of **literal** topic names, exact-matched against the link's active mirror topics — **not** globs.                                            |
| `topicPatterns`         | `[]string` | see note | A list of **anchored full-match** regular expressions (RE2). `['.*']` selects every active mirror topic.                                                  |
| `route`                 | string     | yes      | A `spec.routes[].name` in the initial CR to fence and switch over. Must be non-blank and exist in the CR.                                                 |
| `targetStreamingDomain` | string     | yes      | The streaming domain this route switches to once unfenced. Must already be declared in the initial CR's `spec.streamingDomains`.                          |

**At least one of `topics` / `topicPatterns` is required.** If `topics` is set
it is authoritative — `topicPatterns` is ignored. There is no
omit-`topics`-means-all default anymore: to migrate every active mirror topic,
write an explicit match-all pattern, `topicPatterns: ['.*']`, with `topics`
absent.

The route's **migration mode** — all-at-once (static) vs topic-based (dynamic)
— is **not** declared here; kcp reads it from the live CR's route binding at
`init` (a singular `streamingDomain` ⇒ static, a plural `streamingDomains` ⇒
dynamic). The **bootstrap server id** the route binds to is likewise **derived**
from the target domain's declaration in the live CR at `init`, not written in
the manifest. (Topic-based/dynamic routes are not yet implemented; a route that
resolves to dynamic is refused at `init`. On a static route, `topicPatterns` is
only consulted when `topics` is absent, and only the match-all pattern is
expanded — any other pattern is refused, and a union with `topics` isn't
implemented, until general pattern expansion lands alongside the topic-based
migration engine.)

`lag-check` ignores the topic selection entirely and always watches every mirror
topic.

## `spec.defaultPolicies`

Optional. Every field is a default that a matching `kcp migration execute` flag
can override for a single run; the section is re-read fresh from the manifest
on every `execute`, never frozen at `init`.

| Field                             | Type     | Default | Override flag                           | Notes                                                                                                                                                                                                                                                                          |
| --------------------------------- | -------- | ------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `lagThreshold`                    | int      | `0`     | `--lag-threshold`                       | Total replication lag (sum of all partition lags) tolerated before proceeding. `0` is the strictest.                                                                                                                                                                           |
| `promoteBatchSize`                | int      | `0`     | `--promote-batch-size`                  | Max mirror topics promoted per batch. `0` promotes all at once; when set, each batch is promoted and confirmed stopped before the next is submitted.                                                                                                                           |
| `rolloutTimeout`                  | duration | `0`     | `--rollout-timeout`                     | Max wait for the operator to report the gateway `Ready` during fence and switchover (e.g. `10m`). `0` means no deadline — waits until convergence or cancellation.                                                                                                             |
| `detectUnroutedProducersDuration` | duration | `0`     | `--detect-unrouted-producers-duration`  | Window to monitor source offsets after fencing for producers still bypassing the gateway; a detected increase aborts before switchover. `0` **skips the check entirely**; minimum `10s` when set — shorter can't span a producer's metadata refresh.                           |
| `consumerOffsetSyncDrainDuration` | duration | `0`     | `--consumer-offset-sync-drain-duration` | Wait after fencing, before disabling the link's consumer offset sync, letting final offsets propagate. Has no effect unless `pauseConsumerOffsetSync` is set. `0` means no wait.                                                                                               |
| `hotReloadTimeout`                | duration | `0`     | `--hot-reload-timeout`                  | Max wait for every gateway pod to report the new config revision when the gateway supports hot-reload (e.g. `90s`). Unlike `rolloutTimeout` this is never unbounded: a hot-reload moves no Kubernetes signal, so `0` uses the built-in 90s budget rather than waiting forever. |
| `gatewayConfigPort`               | int      | `0`     | `--gateway-config-port`                 | Port serving the gateway's `/config` endpoint, polled per pod to confirm a config revision was applied. `0` uses the persisted value, falling back to the gateway default (`9180`).                                                                                            |

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
| `kcp migration init`      | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest.                                                                                                                   |
|                           | `--migration-state-file`                                                                                                                                                                         | no (default `migration-state.json`) | Created if absent; the new migration is appended if it exists.                                                                           |
|                           | `--skip-validate`                                                                                                                                                                                | no                                  | Skip credential resolution and gateway/Kubernetes resource validation. Still reads the initial CR to derive the snapshot's bootstrap id.  |
| `kcp migration execute`   | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest.                                                                                                                   |
|                           | `--migration-state-file`                                                                                                                                                                         | yes                                 | Produced by `init`.                                                                                                                      |
|                           | `--migration-id`                                                                                                                                                                                 | no                                  | Address a migration by id instead of `metadata.name` — needed only for migrations registered before `metadata.name` became the identity. |
|                           | `--lag-threshold`, `--promote-batch-size`, `--rollout-timeout`, `--detect-unrouted-producers-duration`, `--consumer-offset-sync-drain-duration`, `--hot-reload-timeout`, `--gateway-config-port` | no                                  | Per-run overrides of the matching `spec.defaultPolicies` field for this run only.                                                        |
| `kcp migration lag-check` | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest. Reads only the destination REST leg (`spec.clusterLink.linkCredentials`), honoured in whichever form it resolves — `api_key`/`basic`/`bearer`/`mtls`; it never dials the source or destination Kafka legs. |
|                           | `--poll-interval`                                                                                                                                                                                | no (default `1`)                    | Poll interval in seconds, `1`-`60`.                                                                                                      |

Every path in the manifest resolves relative to the **process working
directory**, not the manifest's own location — the one exception is
`spec.gateway.kubeconfig`, where a leading `~/` is expanded.

## Validation

There is no separate `kcp migration validate` subcommand. Every read of this
manifest — by `init`, `execute`, or `lag-check` alike — parses and structurally
validates in one step, so all three commands get validation automatically and
none can skip it by accident.

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
  until `init` touches the destination); `spec.clusterLink.linkCredentials` must
  not be blank.
- Every credentials field (`spec.source.credentials`,
  `spec.target.kafka.clusterCredentials`, `spec.clusterLink.linkCredentials`)
  must be a non-blank file path; an inline mapping is rejected at parse.
- `spec.gateway.namespace` and `cr-name` must not be blank; the retired
  `crs`/`routes` keys must not be set at all (they fail the strict decode).
- `spec.topicGroup` must have exactly one entry, with a non-blank `route` and
  `targetStreamingDomain`.
- Each entry must set at least one of `topics` / `topicPatterns` (both is
  allowed). Neither present is rejected. `topics`/`topicPatterns`, if present,
  must be non-empty with no blank entries; each `topicPatterns` entry must
  compile as an anchored RE2 regular expression.
- No `spec.defaultPolicies` field may be negative;
  `detectUnroutedProducersDuration`, if greater than zero, must be at least
  `10s`.

`--skip-validate` on `init` bypasses infrastructure/credential validation only
— structural validation above still always runs.

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
| `spec.topicGroup`                                         | list           | yes                                                    | —                                            | exactly one entry                                           |
| `spec.topicGroup[].topics`                                | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, literal names                         |
| `spec.topicGroup[].topicPatterns`                         | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, anchored RE2 patterns (`['.*']` = all) |
| `spec.topicGroup[].route`                                 | string         | yes                                                    | —                                            | must exist in the initial CR                                 |
| `spec.topicGroup[].targetStreamingDomain`                 | string         | yes                                                    | —                                            | must be declared in the initial CR's `spec.streamingDomains` |
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
