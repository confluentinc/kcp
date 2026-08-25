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
  `migration-state.json`. `--skip-validate` creates the migration record without
  touching infrastructure or resolving credentials — useful for testing.
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
apiVersion: kcp.confluent.io/v1alpha1   # required, exact literal
kind: GatewayMigration                   # required, exact literal
metadata:
  name: my-migration                     # required, non-blank — the migration identity
interpolate: true                        # optional; opt this file in to ${ENV_VAR} resolution
spec:
  source:          { ... }               # required — cluster being migrated from
  target:          { ... }               # required — cluster being migrated to
  clusterLink:     { ... }               # required — an ALREADY-EXISTING cluster link
  gateway:          { ... }              # required — namespace, kubeconfig, gateway CRs
  topics:          [ ... ]               # optional — literal topic names; omit = every active mirror
  defaultPolicies: { ... }               # optional — execute-time policy defaults
```

`apiVersion` must equal `kcp.confluent.io/v1alpha1` and `kind` must equal
`GatewayMigration`, exactly. `metadata.name` is required and non-blank: it is
written into the state file as the migration's identity (pre-manifest,
uuid-keyed migrations keep working, addressed instead with `--migration-id`).
`spec.source`, `spec.target`, `spec.clusterLink`, and `spec.gateway` are always
required; `spec.topics` and `spec.defaultPolicies` are optional.

## `interpolate`

A top-level boolean (not `spec.interpolate` — a referenced credentials file uses
the identical key with no envelope, so both spellings are the same field).
Absent (the default) means every value in the file is literal.

Syntax is intentionally narrow: only `${VAR}` is a reference — a bare `$VAR` is
left alone — `$${` escapes a literal `${`, and **an unset variable is a hard
error naming the variable** (never a silent empty string, which would attempt
auth with an empty secret). Only string fields are interpolated; numeric and
duration fields (e.g. `spec.defaultPolicies.rolloutTimeout`) must be written as
literals.

This flag governs only its own file. An **inline** credentials block inside this
manifest is resolved under this same flag; a credentials file referenced **by
path** does not inherit it — that file needs its own top-level `interpolate:
true` to opt in independently.

## `spec.source`

The cluster being migrated from. `kcp` only ever reads from it.

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum | yes | `msk` or `apache-kafka`. Gates auth: `iam` is valid only for `msk`. |
| `bootstrapServers` | `[]string` | yes | Non-empty; each entry `host:port`. |
| `credentials` | path or inline | yes | Kafka-family credentials — see [Credentials](#credentials) below. |

`confluent-platform` is not a valid value here — this manifest only ever
points at a link that already exists, so it never needs to act as a
source-side link initiator.

## `spec.target`

The cluster being migrated to.

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum | yes | `confluent-cloud` or `confluent-platform`. |
| `clusterId` | string | yes | Required for **both** destination types — this kind never discovers it live. |
| `kafka.bootstrapServers` | `[]string` | yes | Destination Kafka bootstrap. |
| `kafka.restEndpoint` | string | yes | Destination Admin REST endpoint (cluster link + topic operations). |
| `kafka.credentials` | path or inline | yes | Destination Kafka leg, used as SASL/PLAIN against the bootstrap. **Only `sasl_plain` is accepted** — see below. |
| `kafka.restCredentials` | path or inline | no | Destination REST leg. Optional — derived from `credentials` when omitted. See below. |

`kafka.credentials` is checked against the destination client, which is
hardcoded to SASL/PLAIN over TLS: any other auth block is rejected outright
rather than silently accepted and ignored. A `ca_cert` inside `sasl_plain` is
also rejected for the destination in this release — the client always dials
the public trust store, so a private CA here would read as configured while
actually connecting on system roots. Set `tls: true` (no `ca_cert`) for a
public-CA destination.

`kafka.restCredentials` is **optional and derived in full** from `credentials`
when omitted: `api_key`/`api_secret` come from `sasl_plain.username`/`password`,
and `insecure_skip_verify` from the sibling `insecure_skip_tls_verify`. Spell it
out only when the REST endpoint sits behind a different, private CA than the
Kafka listener — and when you do, only the flat `api_key`/`api_secret` form is
accepted (`basic`, `bearer`, `mtls` are all rejected, for the same reason as
above: a form the client can't act on is worse silently dropped than refused).
A block that is present is used exactly as written, never partially derived, so
it must restate the key and secret even if they match `credentials`.

## `spec.clusterLink`

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `name` | string | yes | — | Name of a cluster link that **already exists** on the destination. This kind never creates one. |
| `pauseConsumerOffsetSync` | bool | no | `false` | Disable the link's `consumer.offset.sync.enable` during execute and restore it after switchover. Requires the link to currently have it enabled. |

## `spec.gateway`

There is no `crs.fenced` or `crs.switchover` file: kcp derives both the fenced
CR and the switched CR from the live initial CR at cutover — a fence block
injected onto each named route, and each route's `streamingDomain` flipped to
its declared switchover target. Setting `crs.switchover` is a validation
error, not a silent no-op — it stays detectable on the manifest struct
specifically so a stale manifest fails loudly instead of being read as valid.

| Field | Type | Required | Notes |
|---|---|---|---|
| `namespace` | string | yes | Kubernetes namespace where the gateway is deployed. |
| `kubeconfig` | string | no | Path to the kubeconfig to use. The **one** field in this manifest where a leading `~/` is expanded. |
| `crs.initial` | string | yes | The **name** of the initial gateway custom resource — read live from the cluster at `init`, not a file path. |
| `fence.routes[].name` | string | yes | A `spec.routes[].name` in the initial CR to fence at cutover. Must be non-blank and unique within the list. |
| `fence.routes[].switchover.streamingDomain.name` | string | yes | The streaming domain this route switches to once unfenced. Must already be declared in the initial CR's `spec.streamingDomains`. |
| `fence.routes[].switchover.streamingDomain.bootstrapServerId` | string | yes | A bootstrap server id declared on that streaming domain. |

## `spec.topics`

Optional. A flat list of **literal** topic names, exact-matched against the
cluster link's active mirror topics — **not** globs. Omit the key entirely to
cut over every active mirror topic; an explicitly empty list means the opposite
and is rejected. A name that matches no active mirror topic is a hard error.

`lag-check` ignores this field entirely and always watches every mirror topic.

## `spec.defaultPolicies`

Optional. Every field is a default that a matching `kcp migration execute` flag
can override for a single run; the section is re-read fresh from the manifest
on every `execute`, never frozen at `init`.

| Field | Type | Default | Override flag | Notes |
|---|---|---|---|---|
| `lagThreshold` | int | `0` | `--lag-threshold` | Total replication lag (sum of all partition lags) tolerated before proceeding. `0` is the strictest. |
| `promoteBatchSize` | int | `0` | `--promote-batch-size` | Max mirror topics promoted per batch. `0` promotes all at once; when set, each batch is promoted and confirmed stopped before the next is submitted. |
| `rolloutTimeout` | duration | `0` | `--rollout-timeout` | Max wait for the operator to report the gateway `Ready` during fence and switchover (e.g. `10m`). `0` means no deadline — waits until convergence or cancellation. |
| `detectUnroutedProducersDuration` | duration | `0` | `--detect-unrouted-producers-duration` | Window to monitor source offsets after fencing for producers still bypassing the gateway; a detected increase aborts before switchover. `0` **skips the check entirely**; minimum `10s` when set — shorter can't span a producer's metadata refresh. |
| `consumerOffsetSyncDrainDuration` | duration | `0` | `--consumer-offset-sync-drain-duration` | Wait after fencing, before disabling the link's consumer offset sync, letting final offsets propagate. Has no effect unless `pauseConsumerOffsetSync` is set. `0` means no wait. |
| `hotReloadTimeout` | duration | `0` | `--hot-reload-timeout` | Max wait for every gateway pod to report the new config revision when the gateway supports hot-reload (e.g. `90s`). Unlike `rolloutTimeout` this is never unbounded: a hot-reload moves no Kubernetes signal, so `0` uses the built-in 90s budget rather than waiting forever. |
| `gatewayConfigPort` | int | `0` | `--gateway-config-port` | Port serving the gateway's `/config` endpoint, polled per pod to confirm a config revision was applied. `0` uses the persisted value, falling back to the gateway default (`9180`). |

## Credentials

Every `credentials`-shaped slot (`spec.source.credentials`,
`spec.target.kafka.credentials`, `spec.target.kafka.restCredentials`) accepts
**either** a path to an external file **or** the same content written inline —
the two spellings are a copy-paste apart, since the inline mapping is the file's
top-level content dropped straight into the manifest:

```yaml
# path form
credentials: /etc/kcp/source-creds.yaml

# inline form — identical shape, no envelope
credentials:
  sasl_scram:
    username: ${MSK_USERNAME}
    password: ${MSK_PASSWORD}
    mechanism: SHA512
```

Prefer the path form for a credential mounted as a Kubernetes Secret file;
prefer inline with `${ENV_VAR}` references for everything else. A manifest with
inline credentials is secret-bearing — `kcp` warns (not errors) if the file is
group- or world-readable, so keep it `0600`.

### Kafka credentials (`spec.source.credentials`, `spec.target.kafka.credentials`)

Specify **exactly one** method block — its **presence** selects it (no
`auth_method:` wrapper, no `use:` flag). An optional top-level
`insecure_skip_tls_verify: false` sibling applies to test environments only.

| Method | Required fields | Notes |
|---|---|---|
| `iam` | `region` | MSK source only; Confluent Cloud can't present IAM (a link to MSK uses SCRAM instead). |
| `sasl_scram` | `username`, `password` | Optional `mechanism` (`SHA256`/`SHA512`; MSK requires `SHA512`), `ca_cert`. Always TLS (SASL_SSL). |
| `sasl_plain` | `username`, `password` | Optional `ca_cert`, `tls`. `ca_cert` present ⇒ SASL_SSL against that CA; `tls: true` ⇒ SASL_SSL over the system/public trust store; neither ⇒ SASL_PLAINTEXT. |
| `mtls` | `client_cert`, `client_key` | Optional `ca_cert`. Client is authenticated via its certificate. |
| `unauthenticated_tls` | — | Optional `ca_cert`. One-way TLS; client is not authenticated. |
| `unauthenticated_plaintext` | — | No auth, no TLS. Test/lab only. |

`ca_cert` (on `sasl_scram`, `sasl_plain`, `mtls`, `unauthenticated_tls`) is a
PEM file path used to verify the broker's TLS certificate. Supply it only for a
**private/internal CA**; public-CA brokers (AWS MSK, Confluent Cloud) validate
against the system trust store and need no `ca_cert`.

### REST credentials (`spec.target.kafka.restCredentials`)

Specify **exactly one** block (or the `api_key`/`api_secret` pair).

| Method | Required fields | Notes |
|---|---|---|
| `api_key` + `api_secret` | `api_key`, `api_secret` | Confluent Cloud; flat top-level pair; public CA. |
| `basic` | `username`, `password` | e.g. Confluent Platform MDS. |
| `bearer` | `token` | e.g. MDS/OAuth. |
| `mtls` | `client_cert`, `client_key` | Auth at the TLS layer. |

`basic`, `bearer`, and `mtls` each accept an optional `ca_cert` and
`insecure_skip_verify` to reach a TLS endpoint fronted by a private/internal CA
(e.g. self-managed CP/MDS). Public-CA endpoints (Confluent Cloud via `api_key`)
need neither.

Two restrictions are specific to **this** manifest, narrower than the two
tables above:

- `spec.source.credentials.iam` is valid only when `spec.source.type: msk`.
- `spec.target.kafka.credentials` accepts **only `sasl_plain`**, and
  `spec.target.kafka.restCredentials`, if spelled out, accepts **only the flat
  `api_key`/`api_secret` form** — every other block in the tables above is
  rejected on these two slots specifically, because the destination clients
  this manifest drives can't act on it.

## How the commands read this file

| Command | Flag | Required | Notes |
|---|---|---|---|
| `kcp migration init` | `--migration-yaml` | yes | Path to this manifest. |
| | `--migration-state-file` | no (default `migration-state.json`) | Created if absent; the new migration is appended if it exists. |
| | `--skip-validate` | no | Skip infrastructure validation and credential resolution — creates migration metadata only. |
| `kcp migration execute` | `--migration-yaml` | yes | Path to this manifest. |
| | `--migration-state-file` | yes | Produced by `init`. |
| | `--migration-id` | no | Address a migration by id instead of `metadata.name` — needed only for migrations registered before `metadata.name` became the identity. |
| | `--lag-threshold`, `--promote-batch-size`, `--rollout-timeout`, `--detect-unrouted-producers-duration`, `--consumer-offset-sync-drain-duration`, `--hot-reload-timeout`, `--gateway-config-port` | no | Per-run overrides of the matching `spec.defaultPolicies` field for this run only. |
| `kcp migration lag-check` | `--migration-yaml` | yes | Path to this manifest. |
| | `--poll-interval` | no (default `1`) | Poll interval in seconds, `1`-`60`. |

Every path in the manifest resolves relative to the **process working
directory**, not the manifest's own location — the one exception is
`spec.gateway.kubeconfig`, where a leading `~/` is expanded.

## Validation

There is no separate `kcp migration validate` subcommand. Every read of this
manifest — by `init`, `execute`, or `lag-check` alike — parses, resolves
interpolation, and structurally validates in one step, so all three commands
get validation automatically and none can skip it by accident.

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
  until `init` touches the destination).
- `spec.gateway.namespace` and `crs.initial` must not be blank; `crs.switchover`
  must not be set at all.
- Every `fence.routes[]` entry needs a non-blank, unique `name` and a
  non-blank `switchover.streamingDomain.{name,bootstrapServerId}` — a route
  cannot be named to fence without also declaring where it switches to.
- `spec.topics`, if present, must be non-empty with no blank entries.
- No `spec.defaultPolicies` field may be negative;
  `detectUnroutedProducersDuration`, if greater than zero, must be at least
  `10s`.

`--skip-validate` on `init` bypasses infrastructure/credential validation only
— structural validation above still always runs.

## Field reference

| Path | Type | Required | Default | Allowed values |
|---|---|---|---|---|
| `apiVersion` | string | yes | — | `kcp.confluent.io/v1alpha1` |
| `kind` | string | yes | — | `GatewayMigration` |
| `metadata.name` | string | yes | — | non-blank |
| `interpolate` | bool | no | `false` | — |
| `spec.source.type` | enum | yes | — | `msk`, `apache-kafka` |
| `spec.source.bootstrapServers` | `[]string` | yes | — | `host:port` |
| `spec.source.credentials` | path or inline | yes | — | Kafka family; `iam` only if `type: msk` |
| `spec.target.type` | enum | yes | — | `confluent-cloud`, `confluent-platform` |
| `spec.target.clusterId` | string | yes | — | required for both target types |
| `spec.target.kafka.bootstrapServers` | `[]string` | yes | — | `host:port` |
| `spec.target.kafka.restEndpoint` | string | yes | — | URL |
| `spec.target.kafka.credentials` | path or inline | yes | — | `sasl_plain` only |
| `spec.target.kafka.restCredentials` | path or inline | no | derived from `credentials` | `api_key`/`api_secret` form only |
| `spec.clusterLink.name` | string | yes | — | must reference an existing link |
| `spec.clusterLink.pauseConsumerOffsetSync` | bool | no | `false` | — |
| `spec.gateway.namespace` | string | yes | — | — |
| `spec.gateway.kubeconfig` | string | no | — | `~/` expanded |
| `spec.gateway.crs.initial` | string | yes | — | K8s object name |
| `spec.gateway.fence.routes[].name` | string | yes | — | must exist in the initial CR, unique |
| `spec.gateway.fence.routes[].switchover.streamingDomain.name` | string | yes | — | must be declared in the initial CR's `spec.streamingDomains` |
| `spec.gateway.fence.routes[].switchover.streamingDomain.bootstrapServerId` | string | yes | — | must be declared on that streaming domain |
| `spec.topics` | `[]string` | no | omitted = every active mirror topic | non-empty if present, literal names |
| `spec.defaultPolicies.lagThreshold` | int | no | `0` | `>= 0` |
| `spec.defaultPolicies.promoteBatchSize` | int | no | `0` | `>= 0` |
| `spec.defaultPolicies.rolloutTimeout` | duration | no | `0` | `>= 0` |
| `spec.defaultPolicies.detectUnroutedProducersDuration` | duration | no | `0` | `0`, or `>= 10s` |
| `spec.defaultPolicies.consumerOffsetSyncDrainDuration` | duration | no | `0` | `>= 0` |
| `spec.defaultPolicies.hotReloadTimeout` | duration | no | `0` | `>= 0` |
| `spec.defaultPolicies.gatewayConfigPort` | int | no | `0` | `>= 0` |

## Editor support

`gateway-examples/gateway-migration.yaml` carries a `# yaml-language-server:
$schema=…` modeline pointing at `internal/manifest/gatewaymigration.schema.json`,
so VS Code with the
[Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
gives autocomplete and inline validation automatically.
