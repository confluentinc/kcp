# Validation and field reference

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
  - spec.staticMigration.policies.detectUnroutedProducersDuration: must be at least 10s when set (0 skips the check) — a shorter window cannot span a producer's metadata refresh
```

Key rules, beyond required/optional per field above:

- `apiVersion`/`kind` must be the exact literals; `metadata.name` non-blank.
- `spec.source.type` and `spec.target.type` must be one of their listed enum
  values.
- Every `bootstrapServers` entry must be `host:port`.
- Exactly one of `spec.staticMigration`, `spec.dynamicMigration` and
  `spec.dynamicToStaticConversion` must be set. A manifest still using
  `spec.route` or `spec.defaultPolicies` is rejected with a hint to move to the
  new shape.
- `spec.staticMigration.consumerOffsetSync.baseline` is required when `pause`
  is set, and if set at all must be `enabled` or `disabled`.
- `spec.clusterLink.name` must not be blank (existence itself isn't checked
  until the first `execute` run touches the destination); `spec.clusterLink.linkCredentials` must
  not be blank.
- Every credentials field (`spec.source.credentials`,
  `spec.target.kafka.clusterCredentials`, `spec.clusterLink.linkCredentials`)
  must be a non-blank file path; an inline mapping is rejected at parse.
- `spec.gateway.namespace` and `cr-name` must not be blank; the retired
  `crs`/`routes` keys must not be set at all (they fail the strict decode).
- The declared section's `route.name` and `route.targetStreamingDomain` must
  not be blank. A migration's `topicGroup` must have exactly one entry; a
  `dynamicToStaticConversion` has no `topicGroup` field.
- Each entry must set at least one of `topics` / `topicPatterns` (both is
  allowed). Neither present is rejected. `topics`/`topicPatterns`, if present,
  must be non-empty with no blank entries; each `topicPatterns` entry must
  compile as an anchored RE2 regular expression.
- Duration policies are written with a unit (`0s`, `10m`); a bare number is a
  parse error.
- No `policies` field may be negative;
  `detectUnroutedProducersDuration` and `detectUnroutedCommitsDuration`, if
  greater than zero, must be at least `10s` (`0` skips the check).

Rules that need the contents of a credentials file run when that file is read,
not when the manifest is parsed: the `iam`-needs-an-`msk`-source rule, the
rejection of `iam` on the destination, and the credentials file's own
validation. `execute` (including `--dry-run`) reads the source, destination and
REST credentials files; `lag-check` reads only the REST one.

`kcp migration cutover execute --dry-run` performs the same validation `execute` would (manifest structure, credentials, the effective policy, and the reconcile preconditions against the live gateway CR, clusters and cluster link) and prints a reconcile plan, but takes no actions and runs no migration steps — useful for validating your infrastructure and manifest while iterating before scheduling a live cutover.

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
| `spec.gateway.namespace`                                  | string         | yes                                                    | —                                            | —                                                            |
| `spec.gateway.kubeconfig`                                 | string         | no                                                     | in-cluster in a pod, else `~/.kube/config`   | `~/` expanded                                                |
| `spec.gateway.cr-name`                                    | string         | yes                                                    | —                                            | K8s object name                                              |
| `spec.<section>.route.name`                               | string         | yes                                                    | —                                            | must exist in the initial CR                                 |
| `spec.<section>.route.targetStreamingDomain`              | string         | yes                                                    | —                                            | must be declared in the initial CR's `spec.streamingDomains` |
| `spec.<migration>.topicGroup`                             | list           | yes (migrations only)                                  | —                                            | exactly one entry                                            |
| `spec.<migration>.topicGroup[].topics`                    | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, literal names                         |
| `spec.<migration>.topicGroup[].topicPatterns`             | `[]string`     | at least one of topics/topicPatterns                   | —                                            | non-empty if present, anchored RE2 patterns (`['.*']` = all) |
| `spec.staticMigration.consumerOffsetSync.pause`           | bool           | no                                                     | `false`                                      | —                                                            |
| `spec.staticMigration.consumerOffsetSync.baseline`        | string         | when `pause` is set                                    | —                                            | `enabled`, `disabled`                                        |
| `spec.<section>.policies.lagThreshold`                    | int            | no                                                     | `0`                                          | `>= 0`; static, dynamic                                      |
| `spec.<section>.policies.promoteBatchSize`                | int            | no                                                     | `0`                                          | `>= 0`; static, dynamic                                      |
| `spec.<section>.policies.rolloutTimeout`                  | duration       | no                                                     | `0s`                                         | `>= 0s`; all                                                 |
| `spec.<section>.policies.detectUnroutedProducersDuration` | duration       | no                                                     | `0s`                                         | `0s`, or `>= 10s`; static, dynamic                           |
| `spec.staticMigration.policies.consumerOffsetSyncDrainDuration` | duration | no                                                     | `0s`                                         | `>= 0s`                                                      |
| `spec.<section>.policies.hotReloadTimeout`                | duration       | no                                                     | `0s`                                         | `>= 0s`; all                                                 |
| `spec.<section>.policies.gatewayConfigPort`               | int            | no                                                     | `0`                                          | `>= 0`; all                                                  |
| `spec.dynamicToStaticConversion.policies.detectUnroutedCommitsDuration` | duration | no                                                 | `0s`                                         | `0s`, or `>= 10s`                                            |
| `spec.dynamicToStaticConversion.policies.offsetSyncConcurrency` | int      | no                                                     | `0`                                          | `>= 0`                                                       |
