# Manifest reference

`kcp migrate apply -f migration.yaml` migrates a Kafka source — AWS MSK, Apache
Kafka®, or Confluent Platform — to a Confluent target (Confluent Cloud or
Confluent Platform) by declaring the desired end state in a single YAML manifest.
This page is the field-by-field reference for that manifest.

For an annotated, ready-to-copy manifest see
[`migration.example.yaml`](migration.example.yaml); for worked per-scenario
manifests see [examples/](examples/); for the credential file formats each
`credentials:` slot points at, see the [credential catalog](credentials/README.md).

## Execution model

The manifest is declarative: you describe the cluster link, topics, service
accounts, and ACLs you want on the target, and `apply` reconciles the target
toward that state.

- **Additive-only.** Reconcilers only ever create resources or confirm they are
  already present. Nothing on the target is deleted, altered, or pruned.
- **Idempotent.** Re-running `apply` against an unchanged source and manifest
  reports every resource as already present and makes no further change.
- **Reads the source live.** The source is connected to and read at apply time
  from `spec.source.bootstrapServers` plus its credentials file. Topic lists,
  topic configs, and ACLs always reflect the source's current state at the
  moment of the run — no intermediate state file is consulted.
- **`--dry-run`.** Preconditions and planning still run — live connections to
  source and target are opened and real state is read — but no mutation is
  performed. Output is rendered in future tense ("Planned") rather than past
  tense ("Applying"). Use it to review exactly what a real apply would do.
- **Drift reporting.** For the cluster link, mirror topics, and new topics, an
  existing resource that differs from what the manifest describes is reported as
  drift. Drift is report-only; existing target state is never overwritten.

Validation runs structurally first, with no I/O; any error aborts the run before
the source or target is touched. At least one of `spec.clusterLink`,
`spec.topics`, or `spec.acls` must be present, or the run refuses with "nothing
to apply".

## At a glance

```yaml
apiVersion: kcp.confluent.io/v1alpha1   # required, exact literal
kind: Migration                          # required, exact literal
metadata:
  name: my-migration                     # required, non-blank
spec:
  source:          { ... }               # required — cluster to migrate from
  target:          { ... }               # required — cluster to migrate to
  clusterLink:     { ... }               # optional — cluster link config
  topics:          { ... }               # optional — mirror or new topics
  serviceAccounts: { ... }               # optional — Confluent Cloud identity resolution
  acls:            { ... }               # optional — ACL migration (Confluent Cloud target)
```

`apiVersion` must equal `kcp.confluent.io/v1alpha1` and `kind` must equal
`Migration`, exactly. `metadata.name` is required and non-blank; it identifies
the migration and is not otherwise constrained. `spec.source` and `spec.target`
are always required; the remaining `spec` sections are opt-in and correspond to
independently reconciled units of work.

## `spec.source`

The cluster being migrated from, read live at apply time.

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum | yes | `msk`, `apache-kafka`, or `confluent-platform`. |
| `bootstrapServers` | `[]string` | yes | Non-empty; each entry `host:port` with a numeric port 1-65535. |
| `credentials` | string (path) | yes | Kafka credentials file ([Kafka family](credentials/README.md#kafka-credentials)). |

`type` gates several capabilities elsewhere in the manifest: IAM source
authentication and `spec.acls.iam` are legal only for `msk`; SCRAM against MSK
must resolve to SCRAM-SHA-512; and `spec.clusterLink.mode: source` is legal only
for a `confluent-platform` or `confluent-cloud` source.

`bootstrapServers` is the sole source of the source addresses — they always come
from the manifest, never from a credentials file. The `credentials` file here is
used only to read the source cluster id; the cluster link's own source
connection (in destination mode) carries its own separate credentials.

## `spec.target`

The cluster being migrated to. Required fields depend on `type`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum | yes | `confluent-cloud` or `confluent-platform`. |
| `kafka.restEndpoint` | string | yes | REST v3 endpoint for cluster-link, topic, and ACL operations. |
| `clusterCredentials` | string (path) | yes | REST credentials for the `/kafka/v3` surface ([REST family](credentials/README.md#rest-credentials)). |
| `clusterId` | string | Confluent Cloud only | Required for `confluent-cloud`; forbidden for `confluent-platform`. |
| `cloudCredentials` | string (path) | conditional | Confluent Cloud only; required whenever `spec.acls` is present ([REST family](credentials/README.md#rest-credentials)). |

For `confluent-cloud`, `clusterId` is required and is used directly for link and
REST operations. For `confluent-platform`, the cluster id is discovered live from
the REST endpoint on first use, so `clusterId` must not be set.

`cloudCredentials` is a Confluent Cloud Cloud/Global API key. It is valid only
for a `confluent-cloud` target, and is **required whenever `spec.acls` is
present**: it is used both to provision service accounts (Confluent Cloud IAM v2
rejects a Kafka-cluster API key) and, regardless of `spec.serviceAccounts.autoCreate`,
to build the numeric-id → `sa-`-id map the ACL reconciler needs for idempotent
re-apply.

## `spec.clusterLink`

Optional. Required when `spec.topics.mode: mirror`. Configures a cluster link
between source and target.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `name` | string | yes | — | Link name. In source mode, shared by both link objects. |
| `mode` | enum | no | `destination` | `destination` or `source`. |
| `source` | object | destination mode | — | `bootstrapServers[]` + `credentials`. Link → source connection. |
| `sourceRest` | object | source mode | — | `endpoint` + `credentials` (REST family). |
| `destination` | object | source mode | — | `bootstrapServers[]` + `credentials`. Source-side link → destination connection. |
| `prefix` | string | no | `""` | Prepended to mirror topic names; immutable once the link exists. |
| `consumerOffsetSync` | object | no | enabled | See below. |
| `topicConfigSync.intervalMs` | int | no | `0` | `>= 0`; `0` = server default; written only when `> 0`. |
| `configs` | `map[string]string` | no | — | Free-form additional link configs; see below. |

### Modes

- **`destination`** (default) — one link object, created on the destination
  (target), pulling from the source. Requires `spec.clusterLink.source`;
  `sourceRest` and `destination` must not be set. The destination dials the
  source, so the source must be reachable from the destination.
- **`source`** — two link objects sharing one name: a destination-side object
  created first, then a source-side object created on the source's own REST
  surface, which initiates the link outbound to the destination. Requires
  `sourceRest` and `destination`; `source` must not be set. Legal only when
  `spec.source.type` is `confluent-platform` or `confluent-cloud`, since the
  source must expose a REST surface to host the source-side object — `msk` and
  `apache-kafka` sources cannot initiate a link.

`bidirectional` is explicitly rejected: it describes DR / active-active, not a
migration; use two unidirectional links instead.

### `prefix`

When set, mirror topic names become `prefix + sourceName`. It maps to the link
config `cluster.link.prefix` and is immutable once the link exists. Set it
through this field only; the read-only derived key `link.prefix` is rejected if
placed in `configs`.

### `consumerOffsetSync`

| Field | Type | Default | Notes |
|---|---|---|---|
| `enable` | `*bool` | `true` | True even when the whole `consumerOffsetSync` block is omitted. |
| `intervalMs` | int | `0` | `>= 0`; `0` = server default (`consumer.offset.sync.ms`); written only when `> 0`. |
| `groupFilters` | `[]filter` | include-all | See below. |

Omitting `consumerOffsetSync` entirely still enables offset sync for **all**
consumer groups: when sync is enabled and no filters are given, the default is a
single include-all filter (`name: "*"`, `patternType: LITERAL`, `filterType:
INCLUDE`). Each `groupFilters` entry has `name`, `patternType` (`LITERAL` or
`PREFIXED`), and `filterType` (`INCLUDE` or `EXCLUDE`).

### `configs`

An escape hatch for arbitrary link configs as a string → string map, merged with
the configs derived from the typed fields above. Keys owned by a typed field are
rejected so the merge can never conflict: `cluster.link.prefix`,
`consumer.offset.sync.enable`, `consumer.offset.sync.ms`,
`consumer.offset.group.filters`, `topic.config.sync.ms`, and the read-only
`link.prefix` alias (use `prefix` instead).

## `spec.topics`

Optional. Selects source topics and reproduces them on the target.

| Field | Type | Required | Notes |
|---|---|---|---|
| `mode` | enum | yes | `mirror` or `new`. |
| `include` | `[]string` | yes | Non-empty; each a `path.Match` glob. |
| `exclude` | `[]string` | no | Glob-validated; exclude always wins over include. |

### Modes

- **`mirror`** — creates a read-only mirror topic per selected source topic via
  the cluster link, so `spec.clusterLink.name` must be set. Mirror names are the
  link's live `cluster.link.prefix` + source topic name (falling back to the
  manifest's `clusterLink.prefix` before the link is readable). An existing
  mirror is `Present`; a target name already taken — by a plain topic, or by a
  mirror on a different link — is reported as drift and skipped.
- **`new`** — creates a plain topic per selected source topic; no cluster link
  needed. Partition count and every explicitly-set (non-default) source topic
  config are reproduced. Topics are created at the target's default replication
  factor (Confluent Cloud uses RF=3). An existing target topic whose partition
  count differs from the source is reported as drift and left untouched.

### Selection

A topic is selected if it matches at least one `include` glob and no `exclude`
glob. Internal topics — names starting with `_`, e.g. `__consumer_offsets`,
`_schemas`, `_confluent-*` — are excluded by default even under a broad `*`
include; they are included only when an include pattern that itself starts with
`_` matches them (e.g. `_foo` or `_*`). The result is deduplicated and sorted.

## `spec.serviceAccounts`

Optional; valid only for a `confluent-cloud` target. Resolves each ACL principal
to a target identity before ACLs are written.

| Field | Type | Default | Notes |
|---|---|---|---|
| `autoCreate` | bool | `false` | Find-or-create a Confluent Cloud service account per unmapped principal. |
| `mapping` | `map[string]string` | — | Explicit source principal → target id. |

An explicit `mapping` entry always wins over `autoCreate`. Mapping values must be
a `sa-`, `u-`, or `pool-` id, optionally `User:`-prefixed. `sa-` and `u-` ids are
checked for existence; a `pool-` id is accepted with a not-verified warning
(identity pools cannot be looked up by id). An empty mapping value is treated as
no mapping and falls through to `autoCreate`.

When `autoCreate` is `true`, a service account is derived and find-or-created for
every principal without a mapping entry. An existing account found by name whose
description does not record the expected source principal is a hard error (naming
collision). The special principals `User:*` and `User:ANONYMOUS` are skipped,
with a warning, unless explicitly mapped.

With `autoCreate` off and a principal left unmapped, apply stops with an error
listing every unresolved principal.

## `spec.acls`

Optional; valid only for a `confluent-cloud` target. Migrates ACLs read from the
source, optionally unioned with grants derived from the AWS MSK IAM authorization
plane.

| Field | Type | Required | Notes |
|---|---|---|---|
| `include` | `[]string` | yes | Non-empty; glob matched against an ACL's principal **or** resource name. |
| `exclude` | `[]string` | no | Glob-validated; exclude always wins. |
| `iam` | object | no | AWS MSK IAM authorization plane; see below. |

`include`/`exclude` globs match either the ACL's principal or its resource name
(not resource type or operation). When `spec.acls` is present, the target's
`cloudCredentials` is required (see [`spec.target`](#spectarget)).

### `spec.acls.iam`

Valid only when `spec.source.type: msk`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `clusterArn` | string | yes | Well-formed MSK cluster ARN; scopes discovered grants to this cluster. |
| `principalArns` | `[]string` | one of the two | Named IAM role/user ARNs to translate. |
| `discoverAllRoles` | bool | one of the two | Enumerate every workload IAM role in the account. |
| `verifyEffectiveAccess` | bool | no | Default `false`; see below. |

`clusterArn` must be a fully-populated MSK cluster ARN
(`arn:aws:kafka:<region>:<account>:cluster/<name>/<uuid>`); it scopes discovered
grants by matching the cluster name and UUID.

`principalArns` and `discoverAllRoles` are mutually exclusive, and exactly one is
required when the `iam` block is present. Each `principalArns` entry must be a
well-formed IAM role or user ARN (`arn:aws:iam::<account>:role|user/<name>`); STS
assumed-role ARNs are normalized to their underlying role ARN. `discoverAllRoles`
enumerates every IAM role in the account, excluding AWS service-linked and
SSO-provisioned roles.

`verifyEffectiveAccess`, when `true`, simulates each principal's identity-based
policies and its attached permissions boundary via `iam:SimulatePrincipalPolicy`,
and migrates only the grants that are effectively allowed.

### ACL pipeline

The ACL reconciler runs a fixed pipeline: read the native source ACLs; optionally
gather and translate IAM-derived ACLs (applying the effective-access filter first
when `verifyEffectiveAccess` is set); union the two planes; filter by
`include`/`exclude`; normalize for Confluent Cloud (drop CC-invalid operations,
normalize hosts to `*`, collapse redundant Describe/DescribeConfigs grants);
deduplicate on the exact ACL tuple; resolve each distinct principal to a target
identity, provisioning service accounts as needed; and write the ACLs.

## Credential files

The manifest holds no secrets. Every `credentials:` slot points at an external
YAML file that carries auth only; addresses (`bootstrapServers`, REST `endpoint`)
stay in the manifest. Two credential families exist, one per endpoint kind, each
with the auth method selected by the presence of exactly one block and decoded
strictly (unknown or duplicate keys are rejected):

- **Kafka family** — `spec.source.credentials`,
  `spec.clusterLink.source.credentials`,
  `spec.clusterLink.destination.credentials`.
- **REST family** — `spec.target.clusterCredentials`,
  `spec.target.cloudCredentials`, `spec.clusterLink.sourceRest.credentials`.

Which slot uses which family, and every supported auth method with a copy-ready
file per method, is documented in the [credential catalog](credentials/README.md).

## Dependencies and ordering

Reconcilers run in two independent tracks. Tracks run independently — a failure
in one never skips the other — but within a track execution is fail-fast.

- **Track A: `clusterLink` → `topics`.** The cluster link is created and verified
  first because mirror topics depend on it.
- **Track B: `serviceAccounts` → `acls`.** Service accounts resolve to identities
  before ACLs are written with the correct target principals.

Transient races are retried narrowly (up to 30s): the source-side link create in
source mode against a not-yet-propagated destination object, and mirror-topic
creates against a not-yet-resolvable link. All other errors are reported per item.

## Field reference

| Path | Type | Required | Default | Allowed values |
|---|---|---|---|---|
| `apiVersion` | string | yes | — | `kcp.confluent.io/v1alpha1` |
| `kind` | string | yes | — | `Migration` |
| `metadata.name` | string | yes | — | non-blank |
| `spec.source.type` | enum | yes | — | `msk`, `apache-kafka`, `confluent-platform` |
| `spec.source.bootstrapServers` | `[]string` | yes | — | `host:port`, port 1-65535 |
| `spec.source.credentials` | string | yes | — | path (Kafka family) |
| `spec.target.type` | enum | yes | — | `confluent-cloud`, `confluent-platform` |
| `spec.target.kafka.restEndpoint` | string | yes | — | URL |
| `spec.target.clusterCredentials` | string | yes | — | path (REST family) |
| `spec.target.clusterId` | string | Confluent Cloud only | — | required for Confluent Cloud, forbidden for Confluent Platform |
| `spec.target.cloudCredentials` | string | Confluent Cloud + when `acls` present | — | path (REST family) |
| `spec.clusterLink.name` | string | yes (if section) | — | — |
| `spec.clusterLink.mode` | enum | no | `destination` | `destination`, `source` |
| `spec.clusterLink.source.bootstrapServers` | `[]string` | destination mode | — | `host:port` |
| `spec.clusterLink.source.credentials` | string | destination mode | — | path (Kafka family) |
| `spec.clusterLink.sourceRest.endpoint` | string | source mode | — | URL |
| `spec.clusterLink.sourceRest.credentials` | string | source mode | — | path (REST family) |
| `spec.clusterLink.destination.bootstrapServers` | `[]string` | source mode | — | `host:port` |
| `spec.clusterLink.destination.credentials` | string | source mode | — | path (Kafka family) |
| `spec.clusterLink.prefix` | string | no | `""` | — |
| `spec.clusterLink.consumerOffsetSync.enable` | bool | no | `true` | — |
| `spec.clusterLink.consumerOffsetSync.intervalMs` | int | no | `0` | `>= 0` |
| `spec.clusterLink.consumerOffsetSync.groupFilters[].name` | string | yes (if entry) | — | — |
| `spec.clusterLink.consumerOffsetSync.groupFilters[].patternType` | enum | yes (if entry) | — | `LITERAL`, `PREFIXED` |
| `spec.clusterLink.consumerOffsetSync.groupFilters[].filterType` | enum | yes (if entry) | — | `INCLUDE`, `EXCLUDE` |
| `spec.clusterLink.topicConfigSync.intervalMs` | int | no | `0` | `>= 0` |
| `spec.clusterLink.configs` | `map[string]string` | no | — | non-managed keys only |
| `spec.topics.mode` | enum | yes (if section) | — | `mirror`, `new` |
| `spec.topics.include` | `[]string` | yes (if section) | — | globs, non-empty |
| `spec.topics.exclude` | `[]string` | no | — | globs |
| `spec.serviceAccounts.autoCreate` | bool | no | `false` | — |
| `spec.serviceAccounts.mapping` | `map[string]string` | no | — | `sa-`/`u-`/`pool-` ids |
| `spec.acls.include` | `[]string` | yes (if section) | — | globs, non-empty |
| `spec.acls.exclude` | `[]string` | no | — | globs |
| `spec.acls.iam.clusterArn` | string | yes (if `iam`) | — | MSK cluster ARN |
| `spec.acls.iam.principalArns` | `[]string` | one of the two | — | IAM role/user ARNs |
| `spec.acls.iam.discoverAllRoles` | bool | one of the two | — | — |
| `spec.acls.iam.verifyEffectiveAccess` | bool | no | `false` | — |
