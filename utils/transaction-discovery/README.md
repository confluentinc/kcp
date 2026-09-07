# Transaction Discovery

A standalone POC utility for discovering and grouping transactional workloads prior to a Kafka migration. It observes a running Kafka cluster and derives the groups of topics that are coupled by a Kafka transaction and therefore must migrate toegether, atomically.

## How it works

This is achieved by reading the `__transaction_state` internal topic from the beginning for the source of truth. Therefore, **access to the `__transaction_state` is required**. The POC runs in a continuous fetch loop. decoding the `TransactionLogValue` records to reconstruct each transaction's topic footprint. As it decodes the records, it registers each `transactional.id` and its producer ID in a map which is later used for grouping topics into their transactional workloads.

As a transaction's footprint only records the topics it writes to, even in a consume-process-produce app, the POC also ensures that the 'consumed' input-topic is recorded as part of the transactional workload group to ensure that it is migrated alongside the write, output, topic. This is achieved in two ways:

1. **Consumer-group enrichment** (`--enrich-consumer-groups`): the topics a group commits offsets for are the topics it consumes; each group is tied to a transaction by the Kafka Streams `transactional.id`↔`group.id` naming convention.
2. **`__consumer_offsets` tail** (`--tail-consumer-offsets`): ties each _transactional_ offset commit to its transaction by **producer ID** (exact, no naming assumption), covering arbitrary non-Streams consumer + producer EOS apps. It reads an internal topic, so it is availability-gated and falls back to the naming heuristic where inaccessible.

Finally, when the POC reaches the set duration for observing a cluster, it creates a union from the footprint of topics that appear within the same transaction. For example: if a cluster has topics with transactions `A`, `B`, `C` and `D`, and:

- `A` and `B` share one transaction.
- `B` and `C` share another transaction.
- `D` has transactions spanning no other topics.

The final result returned by the POC would be two transactional workload groups: Group 1 consisting of topcis `A`, `B` and `C`; and Group 2 consisting of just topic `D`.

For now, the POC is purely for exploratory, testing purposes. The results are outputted to the terminal as well as to a `migration.yaml` and are not currently integrated with, or can be used by kcp.

## Requirements

- **Go 1.25+** to build (produces a single static binary with no runtime deps).
- A target cluster that **exposes the `__transaction_state` topic for reading**: self-managed Apache Kafka, Confluent Platform, and **AWS MSK Provisioned**.
- **Not supported:** Confluent Cloud and **MSK Serverless** — they do not expose `__transaction_state` (or the transaction admin APIs), so this cluster-internal approach cannot run there. The tool fails fast at preflight if the topic is not readable.

## Build

This directory is a self-contained Go module.

```bash
cd utils/transaction-discovery
make build            # -> bin/txn-discovery   (current platform, static)
make test             # unit tests
make dist             # cross-compiled static binaries in dist/ (linux/darwin/windows)
```

The binary is built with `CGO_ENABLED=0`, so it is a single static executable that cross-compiles cleanly for any customer OS/arch — no JRE, no librdkafka, nothing to install on the box that runs it.

## Required permissions

The tool only ever **reads**. Grant the principal it runs as:

| Resource                    | ACL        | Used for                                                                                                       |
| --------------------------- | ---------- | -------------------------------------------------------------------------------------------------------------- |
| Topic `__transaction_state` | `READ`     | the source of truth — reconstructing each transaction's topic footprint                                        |
| Topic `__consumer_offsets`  | `READ`     | recovering the _consumed_ input topics of read-process-write apps by exact producer-id correlation             |
| Consumer groups (all)       | `DESCRIBE` | listing groups and their committed offsets, to recover consumed inputs via the Kafka Streams naming convention |

If preflight fails with an authorization error, these ACLs are the first thing to check. The `__consumer_offsets` read and the group `DESCRIBE` are only needed for consumed-input recovery; if you cannot grant them, disable those sources with `--tail-consumer-offsets=false` / `--enrich-consumer-groups=false` and the tool still discovers the produced-topic groups from `__transaction_state` alone.

## Authentication

Auth is configurable so one binary covers AWS MSK, Confluent Platform, and self-managed Kafka. Prefer passing the SASL password via the `TXN_DISCOVERY_PASSWORD` environment variable rather than `--password` (keeps it out of your shell history and the process list).

| Cluster / listener                   | Flags                                                          |
| ------------------------------------ | -------------------------------------------------------------- |
| SASL/SCRAM over TLS (typical MSK)    | `--sasl scram-sha-512 --username U` + `TXN_DISCOVERY_PASSWORD` |
| SASL/PLAIN over TLS                  | `--sasl plain --username U` + `TXN_DISCOVERY_PASSWORD`         |
| Mutual TLS (client-certificate auth) | `--sasl none --tls-cert client.pem --tls-key client-key.pem`   |
| SASL over plaintext (no TLS)         | add `--tls=false` to any SASL option above                     |
| Plaintext, no auth (dev only)        | `--sasl none --tls=false`                                      |

For a self-signed cluster (common on self-managed / Confluent Platform), point `--ca-cert` at the cluster's CA bundle. `--tls-insecure` skips certificate verification and is for local testing only.

## Run

```bash
# SASL/SCRAM over TLS (e.g. AWS MSK Provisioned; MSK uses a public CA, so no --ca-cert)
export TXN_DISCOVERY_PASSWORD='…'
bin/txn-discovery \
  --brokers broker1:9096,broker2:9096,broker3:9096 \
  --sasl scram-sha-512 \
  --username my-user \
  --duration 15m \
  --interval 30s

# Confluent Platform / self-managed over TLS with a private CA
export TXN_DISCOVERY_PASSWORD='…'
bin/txn-discovery \
  --brokers broker1:9093,broker2:9093 \
  --sasl plain --username my-user \
  --ca-cert /path/to/ca.pem \
  --duration 12h

# Mutual TLS (client certificate), no SASL
bin/txn-discovery \
  --brokers broker1:9093,broker2:9093 \
  --sasl none \
  --ca-cert /path/to/ca.pem \
  --tls-cert /path/to/client.pem \
  --tls-key  /path/to/client-key.pem \
  --duration 168h
```

**Choosing `--duration`:** the tool reports what it _observed_ in the window. A longer window captures more of the transactional workload (including infrequent jobs) and is the main lever for completeness — see [Known limitations](#known-limitations). It stops early on `Ctrl-C` / `SIGTERM` and writes its output with whatever it has seen so far.

### Key flags

| Flag                                               | Default          | Purpose                                                                                         |
| -------------------------------------------------- | ---------------- | ----------------------------------------------------------------------------------------------- |
| `--brokers`                                        | (required)       | comma-separated bootstrap brokers                                                               |
| `--sasl`                                           | `scram-sha-512`  | `none` \| `plain` \| `scram-sha-256` \| `scram-sha-512`                                         |
| `--tls` / `--ca-cert` / `--tls-cert` / `--tls-key` | `--tls` on       | TLS, custom CA, and mutual-TLS client cert/key                                                  |
| `--duration`                                       | `5m`             | total observation window                                                                        |
| `--interval`                                       | `30s`            | consumed-input recovery refresh/flush cadence                                                   |
| `--enrich-consumer-groups`                         | `true`           | recover consumed inputs via consumer-group offsets (Streams naming)                             |
| `--tail-consumer-offsets`                          | `true`           | recover consumed inputs by exact producer-id correlation (tails `__consumer_offsets`)           |
| `--out`                                            | `migration.yaml` | where to write the discovered groups                                                            |
| `--stats-out`                                      | (off)            | also write a JSON diagnostics report (reader keep-up metrics + full per-transaction footprints) |
| `--dry-run`                                        | `false`          | print the summary, don't write the file                                                         |

Run `txn-discovery --help` for the full list.

## Output

Two things:

1. **A terminal summary** — run totals, the discovered transaction groups with their topics, the individual (uncoupled) topics, and a read-process-write section noting any consumed input topics that were recovered and folded into their group. A "Keep-up" block reports how well the `__transaction_state` reader kept up (records seen, lag, any decode failures) — a large, non-decreasing lag or a decode-failure warning is your signal that a run did not observe the whole window.
2. **`migration.yaml`** — the reviewable deliverable. Each group lists its topics and transactional ids and has a `bootstrap_url` field for you to fill in per target cluster. Read-process-write groups carry a `warning` describing how their consumed inputs were recovered (or a caution to verify them). **Review this file before acting on it** — treat the schema as provisional.

## Known limitations

- **Compaction is the residual blind spot.** A footprint can only be reconstructed from a record that still carries a non-empty partition set. Reading from the earliest offset captures the maximum the compacted log retains, but a rare workload (e.g. a nightly/weekly job) whose footprint-bearing records were already compacted down to their final empty-footprint record before the run is unrecoverable. A longer `--duration` mitigates this but never guarantees completeness — the tool reports what it observed; it does not claim certainty.

- **Sequential repurposing**: a `transactional.id` reassigned to a _different_ workload while the _old_ footprint records are still in the retained window — old and new then superimpose into one group. It is narrow in practice (`__transaction_state` is compacted by txn-id, so stale footprints for an active id are usually cleaned away) and, for a migration, this over-grouping is **conservative**: it errs toward moving too much together, never toward splitting things that must stay atomic.

- **Managed offerings are out of scope.** Confluent Cloud and MSK Serverless expose neither `__transaction_state` nor the transaction admin APIs, so the tool fails fast at preflight. Discovering transactional groups there would need a different vantage point (e.g. observing the request stream at a gateway).
