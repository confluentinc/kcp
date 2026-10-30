# Policies in the Migration Manifest

Each declared section of the migration manifest has its own optional `policies` block, and every field
in it is a default that a matching `kcp migration cutover execute` flag can override
for a single run. The block is re-read fresh from the manifest on every
`execute`, never fixed once.

A field a section does not have is rejected by the
strict decode, and a policy flag the declared type does not have is an error
(it names the flag and the section). Duration values need a unit, such as `0s`, `90s`, or
`10m`. In the manifest, a bare number such as `0` or `90` is a parse error (the
command-line flags do accept a plain `0`). Negative values are rejected.

| Field                             | `staticMigration` | `dynamicMigration` | `dynamicToStaticConversion` |
| --------------------------------- | :---: | :---: | :---: |
| `lagThreshold`                    | yes | yes | — |
| `promoteBatchSize`                | yes | yes | — |
| `rolloutTimeout`                  | yes | yes | yes |
| `detectUnroutedProducersDuration` | yes | yes | — |
| `consumerOffsetSyncDrainDuration` | yes | — | — |
| `detectUnroutedCommitsDuration`   | — | — | yes |
| `offsetSyncConcurrency`           | — | — | yes |
| `hotReloadTimeout`                | yes | yes | yes |
| `gatewayConfigPort`               | yes | yes | yes |

| Field                             | Type     | Default | Override flag                           | Notes                                                                                                                                                                                                                                                                          |
| --------------------------------- | -------- | ------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `lagThreshold`{ #policies-lagthreshold }                    | int      | `0`     | `--lag-threshold`                       | Replication lag tolerated per topic: each selected topic's lag (the sum of its partition lags) must be at or below this before the run fences. `0` is the strictest (fully caught up).                                                                                        |
| `promoteBatchSize`{ #policies-promotebatchsize }                | int      | `0`     | `--promote-batch-size`                  | Max mirror topics promoted per batch. `0` promotes all at once. When set, each batch is promoted and confirmed stopped before the next is submitted.                                                                                                                           |
| `rolloutTimeout`{ #policies-rollouttimeout }                  | duration | `0s`    | `--rollout-timeout`                     | Max wait, during fence and switchover, for the operator to accept the change and for Confluent Gateway to roll out (for example, `10m`). It also bounds the per-pod configuration wait after a pod roll is observed. `0s` means no deadline: kcp waits until convergence or cancellation.                                                                                                            |
| `detectUnroutedProducersDuration`{ #policies-detectunroutedproducersduration } | duration | `0s`    | `--detect-unrouted-producers-duration`  | After fencing, kcp takes two source-offset snapshots this far apart to catch producers still bypassing the Gateway. If any partition's offset advanced, the run stops before promote. `0s` (the default) skips the check entirely. When set, the minimum is `10s`, because a shorter window can't span a producer's metadata refresh. Not available on a conversion, which uses `detectUnroutedCommitsDuration` instead. |
| `consumerOffsetSyncDrainDuration`{ #policies-consumeroffsetsyncdrainduration } | duration | `0s`    | `--consumer-offset-sync-drain-duration` | `staticMigration` only. Wait after fencing, before disabling the link's consumer offset sync, letting final offsets propagate. Has no effect unless `consumerOffsetSync.pause` is set. `0s` means no wait.                                           |
| `hotReloadTimeout`{ #policies-hotreloadtimeout }                | duration | `0s`    | `--hot-reload-timeout`                  | Max wait for every Gateway pod to report the new configuration revision when the Gateway supports hot-reload (for example, `90s`). Unlike `rolloutTimeout`, this is never unbounded: a hot-reload moves no Kubernetes signal, so `0s` uses the built-in 90-second budget rather than waiting forever. |
| `gatewayConfigPort`{ #policies-gatewayconfigport }               | int      | `0`     | `--gateway-config-port`                 | Port serving the Gateway's `/config` endpoint, polled per pod to confirm a configuration revision was applied. `0` uses the Gateway default (`9180`).                                                                                                                                 |
| `detectUnroutedCommitsDuration`{ #policies-detectunroutedcommitsduration }   | duration | `0s`    | `--detect-unrouted-commits-duration`    | `dynamicToStaticConversion` only. Window between the two committed-offset snapshots taken after fencing, to catch a consumer committing to the source directly, bypassing the Gateway. `0s` skips the check. When set, the minimum is `10s`. |
| `offsetSyncConcurrency`{ #policies-offsetsyncconcurrency }           | int      | `0`     | `--offset-sync-concurrency`             | `dynamicToStaticConversion` only. Number of workers, each with its own broker connection, reading and writing consumer-group offsets. `0` uses the built-in `8`. |

## Related content

- [Migration sections in the migration manifest](migrations.md)
- [Migration manifest validation and field reference](validation-and-field-reference.md)
- [Run a migration cutover](running-a-cutover.md)
