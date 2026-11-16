# Migration Resources Manifest Reference

`kcp migration resources create -f migration.yaml` migrates a Kafka source, whether Amazon MSK, Apache Kafka®, or Confluent Platform, to Confluent Cloud or Confluent Platform from a single YAML manifest that declares the desired end state. This page is the field-by-field reference for that manifest.

For an annotated, ready-to-copy manifest, see [`migration.example.yaml`](migration.example.yaml). For example manifests for each scenario, see [Examples](README.md#examples). For the credential file formats each `credentials:` slot points at, see the [credential file catalog](credentials/README.md).

## Execution model

The manifest is declarative: you describe the cluster link, topics, service accounts, and access control lists (ACLs) you want on the target, and `create` reconciles the target toward that state.

- **Additive only.** Reconcilers only create resources or confirm that they're already present. kcp never deletes, alters, or prunes anything on the target.
- **Idempotent.** If you re-run `create` against an unchanged source and manifest, it reports every resource as already present and makes no further changes.
- **Reads the source live.** kcp connects to the source at run time through `spec.source.bootstrapServers` and its credentials file. Topic lists, topic configurations, and ACLs always reflect the source's state at the moment of the run. kcp doesn't consult an intermediate state file.
- **`--dry-run`.** Preconditions and planning still run: kcp opens live connections to the source and target and reads real state, but makes no changes. The output uses future tense ("Planned") instead of past tense ("Applying"). Use it to review exactly what `create` would do.
- **Drift reporting.** For the cluster link, mirror topics, and new topics, kcp reports an existing resource that differs from the manifest as drift. Drift is report-only: kcp never overwrites existing target state.

Structural validation runs first, with no I/O, and any error stops the run before kcp touches the source or target. At least one of `spec.clusterLink`, `spec.topics`, or `spec.acls` must be present, or the run refuses with "nothing to create".

## At a glance

The manifest has this shape:

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

`apiVersion` must be exactly `kcp.confluent.io/v1alpha1`, and `kind` must be exactly `Migration`. `metadata.name` is required and can't be blank. It identifies the migration and has no other constraints. `spec.source` and `spec.target` are always required. The other `spec` sections are optional, and each one is a unit of work that kcp reconciles independently.

## `spec.source`

The cluster you migrate from. kcp reads it live at run time.

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum | yes | `msk`, `apache-kafka`, or `confluent-platform`. |
| `bootstrapServers` | `[]string` | yes | Non-empty. Each entry is `host:port`, with a numeric port from 1 to 65535. |
| `credentials` | string (path) | yes | Kafka credentials file, from the [Kafka family](credentials/README.md#kafka-credentials). |

`type` controls several capabilities elsewhere in the manifest:

- IAM source authentication and `spec.acls.iam` are valid only for `msk`.
- SCRAM against MSK must resolve to SCRAM-SHA-512.
- `spec.clusterLink.mode: source` is valid only for a `confluent-platform` or `confluent-cloud` source.

`bootstrapServers` is the only place the source addresses come from: always the manifest, never a credentials file. kcp uses the `credentials` file here only to read the source cluster ID. In destination mode, the cluster link's own source connection has its own credentials.

## `spec.target`

The cluster you migrate to. The required fields depend on `type`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | enum | yes | `confluent-cloud` or `confluent-platform`. |
| `kafka.restEndpoint` | string | yes | REST v3 endpoint for cluster link, topic, and ACL operations. |
| `clusterCredentials` | string (path) | yes | REST credentials for the `/kafka/v3` API, from the [REST family](credentials/README.md#rest-credentials). |
| `clusterId` | string | Confluent Cloud only | Required for `confluent-cloud`. Forbidden for `confluent-platform`. |
| `cloudCredentials` | string (path) | conditional | Confluent Cloud only. Required whenever `spec.acls` is present. From the [REST family](credentials/README.md#rest-credentials). |

For `confluent-cloud`, `clusterId` is required, and kcp uses it directly for link and REST operations. For `confluent-platform`, kcp discovers the cluster ID live from the REST endpoint on first use, so don't set `clusterId`.

`cloudCredentials` is a Confluent Cloud Cloud or Global API key. It's valid only for a `confluent-cloud` target, and it's required whenever `spec.acls` is present. kcp uses it to provision service accounts, because Confluent Cloud IAM v2 rejects a Kafka cluster API key. Whatever `spec.serviceAccounts.autoCreate` is set to, kcp also uses it to build the map from numeric IDs to `sa-` IDs that the ACL reconciler needs for idempotent re-runs.

## `spec.clusterLink`

Configures a cluster link between the source and the target. The section is optional, but required when `spec.topics.mode: mirror` is set.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `name` | string | yes | — | Link name. In source mode, both link objects share it. |
| `mode` | enum | no | `destination` | `destination` or `source`. |
| `source` | object | destination mode | — | `bootstrapServers[]` and `credentials`, for the link's connection to the source. |
| `sourceRest` | object | source mode | — | `endpoint` and `credentials` (REST family). |
| `destination` | object | source mode | — | `bootstrapServers[]` and `credentials`, for the source-side link's connection to the destination. |
| `prefix` | string | no | `""` | Prepended to mirror topic names. Immutable after the link exists. |
| `consumerOffsetSync` | object | no | enabled | See [`consumerOffsetSync`](#consumeroffsetsync). |
| `topicConfigSync.intervalMs` | int | no | `0` | `>= 0`. `0` means the server default. Written only when `> 0`. |
| `configs` | `map[string]string` | no | — | Free-form additional link configurations. See [`configs`](#configs). |

### Modes

- **`destination` (default).** One link object, created on the destination (target), that pulls from the source. Requires `spec.clusterLink.source`. Don't set `sourceRest` or `destination`. The destination connects to the source, so the source must be reachable from the destination.
- **`source`.** Two link objects that share one name: kcp creates a destination-side object first, then a source-side object on the source's own REST API, which starts the link outbound to the destination. Requires `sourceRest` and `destination`. Don't set `source`. Valid only when `spec.source.type` is `confluent-platform` or `confluent-cloud`, because the source must expose a REST API to host the source-side object. `msk` and `apache-kafka` sources can't initiate a link.

kcp rejects `bidirectional`, because it describes disaster recovery (DR) or active-active replication, not a migration. Use two unidirectional links instead.

### `prefix`

When set, mirror topic names become `prefix + sourceName`. It maps to the link configuration `cluster.link.prefix`, and it's immutable after the link exists. Set it only through this field. kcp rejects the read-only derived key `link.prefix` if you place it in `configs`.

### `consumerOffsetSync`

| Field | Type | Default | Notes |
|---|---|---|---|
| `enable` | `*bool` | `true` | True even when you omit the whole `consumerOffsetSync` block. |
| `intervalMs` | int | `0` | `>= 0`. `0` means the server default (`consumer.offset.sync.ms`). Written only when `> 0`. |
| `groupFilters` | `[]filter` | include-all | Consumer group filters for offset sync. |

If you omit `consumerOffsetSync` entirely, offset sync is still enabled for all consumer groups: when sync is enabled and you give no filters, the default is a single include-all filter (`name: "*"`, `patternType: LITERAL`, `filterType: INCLUDE`). Each `groupFilters` entry has `name`, `patternType` (`LITERAL` or `PREFIXED`), and `filterType` (`INCLUDE` or `EXCLUDE`).

### `configs`

A way to set arbitrary link configurations as a map of strings to strings. kcp merges it with the configurations derived from the typed fields. To keep the merge from conflicting, kcp rejects keys that a typed field owns: `cluster.link.prefix`, `consumer.offset.sync.enable`, `consumer.offset.sync.ms`, `consumer.offset.group.filters`, `topic.config.sync.ms`, and the read-only `link.prefix` alias (use `prefix` instead).

## `spec.topics`

Selects source topics and reproduces them on the target. The section is optional.

| Field | Type | Required | Notes |
|---|---|---|---|
| `mode` | enum | yes | `mirror` or `new`. |
| `include` | `[]string` | yes | Non-empty. Each entry is a `path.Match` glob. |
| `exclude` | `[]string` | no | Validated as globs. An exclude always wins over an include. |

### Modes

- **`mirror`.** Creates a read-only mirror topic for each selected source topic through the cluster link, so `spec.clusterLink.name` must be set. A mirror's name is the link's live `cluster.link.prefix` plus the source topic name. Before the link is readable, kcp falls back to the manifest's `clusterLink.prefix`. kcp reports an existing mirror as `Present`. If a plain topic or a mirror on a different link already has the target name, kcp reports it as drift and skips it.
- **`new`.** Creates a plain topic for each selected source topic, with no cluster link needed. kcp reproduces the partition count and every source topic configuration that's set explicitly (not left at its default). kcp creates topics with the target's default replication factor, which is 3 on Confluent Cloud. If an existing target topic's partition count differs from the source, kcp reports it as drift and leaves it unchanged.

### Selection

kcp selects a topic if it matches at least one `include` glob and no `exclude` glob. Internal topics, whose names start with `_`, such as `__consumer_offsets`, `_schemas`, and `_confluent-*`, are excluded by default, even under a broad `*` include. kcp includes them only when an include pattern that itself starts with `_` matches them, such as `_foo` or `_*`. The result is deduplicated and sorted.

## `spec.serviceAccounts`

Resolves each ACL principal to a target identity before kcp writes the ACLs. The section is optional, and valid only for a `confluent-cloud` target.

| Field | Type | Default | Notes |
|---|---|---|---|
| `autoCreate` | bool | `false` | Finds or creates a Confluent Cloud service account for each unmapped principal. |
| `mapping` | `map[string]string` | — | Explicit map from a source principal to a target ID. |

An explicit `mapping` entry always wins over `autoCreate`. Each mapping value must be an `sa-`, `u-`, or `pool-` ID, optionally prefixed with `User:`. kcp checks that `sa-` and `u-` IDs exist. It accepts a `pool-` ID with a warning that the ID isn't verified, because identity pools can't be looked up by ID. kcp treats an empty mapping value as no mapping and falls through to `autoCreate`.

When `autoCreate` is `true`, kcp derives a service account for every principal without a mapping entry, and finds or creates it. If kcp finds an existing account by name whose description doesn't record the expected source principal, that's an error (a naming collision). kcp skips the special principals `User:*` and `User:ANONYMOUS`, with a warning, unless you map them explicitly.

If `autoCreate` is off and a principal is left unmapped, `create` stops with an error that lists every unresolved principal.

## `spec.acls`

Migrates ACLs read from the source, optionally combined with grants derived from the MSK IAM authorization plane. The section is optional, and valid only for a `confluent-cloud` target.

| Field | Type | Required | Notes |
|---|---|---|---|
| `include` | `[]string` | yes | Non-empty. Each glob matches an ACL's principal or resource name. |
| `exclude` | `[]string` | no | Validated as globs. An exclude always wins. |
| `iam` | object | no | MSK IAM authorization plane. See [`spec.acls.iam`](#specaclsiam). |

`include` and `exclude` globs match either the ACL's principal or its resource name, not its resource type or operation. When `spec.acls` is present, the target's `cloudCredentials` is required. See [`spec.target`](#spectarget).

### `spec.acls.iam`

Valid only when `spec.source.type: msk` is set.

| Field | Type | Required | Notes |
|---|---|---|---|
| `clusterArn` | string | yes | Well-formed MSK cluster ARN. Scopes discovered grants to this cluster. |
| `principalArns` | `[]string` | one of the two | Named IAM role or user ARNs to translate. |
| `discoverAllRoles` | bool | one of the two | Enumerates every workload IAM role in the account. |
| `verifyEffectiveAccess` | bool | no | Default `false`. |

`clusterArn` must be a complete MSK cluster ARN (`arn:aws:kafka:<region>:<account>:cluster/<name>/<uuid>`). It scopes discovered grants by matching the cluster name and UUID.

`principalArns` and `discoverAllRoles` are mutually exclusive, and exactly one is required when the `iam` block is present. Each `principalArns` entry must be a well-formed IAM role or user ARN (`arn:aws:iam::<account>:role|user/<name>`). kcp normalizes STS assumed-role ARNs to their underlying role ARN. `discoverAllRoles` enumerates every IAM role in the account, excluding AWS service-linked roles and SSO-provisioned roles.

When `verifyEffectiveAccess` is `true`, kcp simulates each principal's identity-based policies and its attached permissions boundary through `iam:SimulatePrincipalPolicy`, and migrates only the grants that are effectively allowed.

### ACL pipeline

The ACL reconciler runs these steps in a fixed order:

1. Read the native source ACLs.
2. Optionally, gather and translate IAM-derived ACLs, applying the effective-access filter first when `verifyEffectiveAccess` is set.
3. Combine the two planes as a union.
4. Filter by `include` and `exclude`.
5. Normalize for Confluent Cloud: drop operations that Confluent Cloud doesn't accept, normalize hosts to `*`, and collapse redundant Describe and DescribeConfigs grants.
6. Deduplicate on the exact ACL tuple.
7. Resolve each distinct principal to a target identity, provisioning service accounts as needed.
8. Write the ACLs.

## Credential files

The manifest holds no secrets. Every `credentials:` slot points at an external YAML file that carries only authentication. Addresses (`bootstrapServers` and REST `endpoint`) stay in the manifest. kcp uses two credential families, one for each kind of endpoint. In each family, the presence of exactly one block selects the authentication method, and kcp decodes the file strictly, rejecting unknown or duplicate keys:

- **Kafka family.** `spec.source.credentials`, `spec.clusterLink.source.credentials`, and `spec.clusterLink.destination.credentials`.
- **REST family.** `spec.target.clusterCredentials`, `spec.target.cloudCredentials`, and `spec.clusterLink.sourceRest.credentials`.

For which slot uses which family, and every supported authentication method with a ready-to-copy file for each, see the [credential file catalog](credentials/README.md).

## Dependencies and ordering

Reconcilers run in two independent tracks. A failure in one track never skips the other, but within a track, execution stops at the first failure.

- **Track A: `clusterLink`, then `topics`.** kcp creates and verifies the cluster link first, because mirror topics depend on it.
- **Track B: `serviceAccounts`, then `acls`.** kcp resolves service accounts to identities first, so it writes the ACLs with the correct target principals.

kcp retries two transient races, for up to 30 seconds: creating the source-side link in source mode before the destination object has propagated, and creating mirror topics before the link is resolvable. kcp reports all other errors for each item.

## Field reference

| Path | Type | Required | Default | Allowed values |
|---|---|---|---|---|
| `apiVersion` | string | yes | — | `kcp.confluent.io/v1alpha1` |
| `kind` | string | yes | — | `Migration` |
| `metadata.name` | string | yes | — | non-blank |
| `spec.source.type` | enum | yes | — | `msk`, `apache-kafka`, `confluent-platform` |
| `spec.source.bootstrapServers` | `[]string` | yes | — | `host:port`, port 1 to 65535 |
| `spec.source.credentials` | string | yes | — | path (Kafka family) |
| `spec.target.type` | enum | yes | — | `confluent-cloud`, `confluent-platform` |
| `spec.target.kafka.restEndpoint` | string | yes | — | URL |
| `spec.target.clusterCredentials` | string | yes | — | path (REST family) |
| `spec.target.clusterId` | string | Confluent Cloud only | — | required for Confluent Cloud, forbidden for Confluent Platform |
| `spec.target.cloudCredentials` | string | Confluent Cloud, when `acls` is present | — | path (REST family) |
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
| `spec.serviceAccounts.mapping` | `map[string]string` | no | — | `sa-`, `u-`, or `pool-` IDs |
| `spec.acls.include` | `[]string` | yes (if section) | — | globs, non-empty |
| `spec.acls.exclude` | `[]string` | no | — | globs |
| `spec.acls.iam.clusterArn` | string | yes (if `iam`) | — | MSK cluster ARN |
| `spec.acls.iam.principalArns` | `[]string` | one of the two | — | IAM role or user ARNs |
| `spec.acls.iam.discoverAllRoles` | bool | one of the two | — | — |
| `spec.acls.iam.verifyEffectiveAccess` | bool | no | `false` | — |

## Related content

- [Manifests and credential files for kcp migrations](README.md)
- [Credential file catalog for the resources manifest](credentials/README.md)
- [Cutover migration manifest reference](cutover/overview.md)
- [`kcp migration resources create` command reference](../command-reference/migration/resources/create.md)
