# Transaction discovery

A standalone utility for planning Kafka migrations: it observes a running cluster
and derives the groups of topics that are coupled by a Kafka **transaction** and
therefore **must migrate together, atomically**. A topic-by-topic migration that
splits such a group across clusters breaks the transaction's atomicity /
exactly-once (EOS) guarantees, so knowing these groups up front is a prerequisite
for a safe cutover.

> **Status: prototype.** This is a working proof of concept. The discovered
> `migration.yaml` is a **review artifact** — inspect it before using it to drive
> any automated migration (see [Output](#output) and [Known limitations](#known-limitations)).

It reads the cluster's own internal logs, so it needs **no agent, no code changes,
and no access to your application data** — only read access to two internal topics
and to consumer-group metadata (see [Required permissions](#required-permissions)).

## Requirements

- **Go 1.25+** to build (produces a single static binary with no runtime deps).
- A target cluster that **exposes the `__transaction_state` topic for reading**:
  self-managed Apache Kafka, Confluent Platform, and **AWS MSK Provisioned**.
- **Not supported:** Confluent Cloud and **MSK Serverless** — they do not expose
  `__transaction_state` (or the transaction admin APIs), so this cluster-internal
  approach cannot run there. The tool fails fast at preflight if the topic is not
  readable.

## Build

This directory is a self-contained Go module.

```bash
cd utils/transaction-discovery
make build            # -> bin/txn-discovery   (current platform, static)
make test             # unit tests
make dist             # cross-compiled static binaries in dist/ (linux/darwin/windows)
```

The binary is built with `CGO_ENABLED=0`, so it is a single static executable that
cross-compiles cleanly for any customer OS/arch — no JRE, no librdkafka, nothing to
install on the box that runs it.

## Required permissions

The tool only ever **reads**. Grant the principal it runs as:

| Resource | ACL | Used for |
|---|---|---|
| Topic `__transaction_state` | `READ` | the source of truth — reconstructing each transaction's topic footprint |
| Topic `__consumer_offsets` | `READ` | recovering the *consumed* input topics of read-process-write apps by exact producer-id correlation |
| Consumer groups (all) | `DESCRIBE` | listing groups and their committed offsets, to recover consumed inputs via the Kafka Streams naming convention |

If preflight fails with an authorization error, these ACLs are the first thing to
check. The `__consumer_offsets` read and the group `DESCRIBE` are only needed for
consumed-input recovery; if you cannot grant them, disable those sources with
`--tail-consumer-offsets=false` / `--enrich-consumer-groups=false` and the tool
still discovers the produced-topic groups from `__transaction_state` alone.

## Authentication

Auth is configurable so one binary covers AWS MSK, Confluent Platform, and
self-managed Kafka. Prefer passing the SASL password via the
`TXN_DISCOVERY_PASSWORD` environment variable rather than `--password` (keeps it
out of your shell history and the process list).

| Cluster / listener | Flags |
|---|---|
| SASL/SCRAM over TLS (typical MSK) | `--sasl scram-sha-512 --username U` + `TXN_DISCOVERY_PASSWORD` |
| SASL/PLAIN over TLS | `--sasl plain --username U` + `TXN_DISCOVERY_PASSWORD` |
| Mutual TLS (client-certificate auth) | `--sasl none --tls-cert client.pem --tls-key client-key.pem` |
| SASL over plaintext (no TLS) | add `--tls=false` to any SASL option above |
| Plaintext, no auth (dev only) | `--sasl none --tls=false` |

For a self-signed cluster (common on self-managed / Confluent Platform), point
`--ca-cert` at the cluster's CA bundle. `--tls-insecure` skips certificate
verification and is for local testing only.

> **Not yet implemented:** AWS MSK **IAM** (`AWS_MSK_IAM`), SASL/**OAUTHBEARER**,
> and **Kerberos/GSSAPI**. If your cluster requires one of these, it needs to be
> added before the tool can connect.

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
  --duration 15m

# Mutual TLS (client certificate), no SASL
bin/txn-discovery \
  --brokers broker1:9093,broker2:9093 \
  --sasl none \
  --ca-cert /path/to/ca.pem \
  --tls-cert /path/to/client.pem \
  --tls-key  /path/to/client-key.pem \
  --duration 15m
```

**Choosing `--duration`:** the tool reports what it *observed* in the window. A
longer window captures more of the transactional workload (including infrequent
jobs) and is the main lever for completeness — see
[Known limitations](#known-limitations). It stops early on `Ctrl-C` / `SIGTERM`
and writes its output with whatever it has seen so far.

### Key flags

| Flag | Default | Purpose |
|---|---|---|
| `--brokers` | (required) | comma-separated bootstrap brokers |
| `--sasl` | `scram-sha-512` | `none` \| `plain` \| `scram-sha-256` \| `scram-sha-512` |
| `--tls` / `--ca-cert` / `--tls-cert` / `--tls-key` | `--tls` on | TLS, custom CA, and mutual-TLS client cert/key |
| `--duration` | `5m` | total observation window |
| `--interval` | `30s` | consumed-input recovery refresh/flush cadence |
| `--enrich-consumer-groups` | `true` | recover consumed inputs via consumer-group offsets (Streams naming) |
| `--tail-consumer-offsets` | `true` | recover consumed inputs by exact producer-id correlation (tails `__consumer_offsets`) |
| `--out` | `migration.yaml` | where to write the discovered groups |
| `--stats-out` | (off) | also write a JSON diagnostics report (reader keep-up metrics + full per-transaction footprints) |
| `--dry-run` | `false` | print the summary, don't write the file |

Run `txn-discovery --help` for the full list.

## Output

Two things:

1. **A terminal summary** — run totals, the discovered transaction groups with their
   topics, the individual (uncoupled) topics, and a read-process-write section noting
   any consumed input topics that were recovered and folded into their group. A
   "Keep-up" block reports how well the `__transaction_state` reader kept up (records
   seen, lag, any decode failures) — a large, non-decreasing lag or a decode-failure
   warning is your signal that a run did not observe the whole window.
2. **`migration.yaml`** — the reviewable deliverable. Each group lists its topics and
   transactional ids and has a `bootstrap_url` field for you to fill in per target
   cluster. Read-process-write groups carry a `warning` describing how their consumed
   inputs were recovered (or a caution to verify them). **Review this file before
   acting on it** — treat the schema as provisional.

## How it works

`__transaction_state` is the single source of truth; three observation sources feed
one grouping engine:

1. **Preflight** — probe that `__transaction_state` is readable. This both fails fast
   on bad brokers/auth/ACLs and rejects clusters that don't expose it (Confluent
   Cloud / MSK Serverless). There is no admin-sampling fallback.
2. **`__transaction_state` reader (source of truth)** — a *continuous* fetch loop that
   reads the coordinator's internal log **from the earliest offset**, decoding
   `TransactionLogValue` records to reconstruct each transaction's topic footprint. It
   reads from the start (not the tail) so it picks up footprints the compacted log
   still retains from before the run, and stays in the loop so short transactions
   aren't compacted away between reads. As it decodes it registers every
   `transactional.id` and its producer id in a shared **catalog** the next two sources
   read — so the tool never calls the transaction admin APIs.
3. **Consumed-input recovery** — a transaction footprint records only the topics a
   read-process-write app *writes*, never those it *consumes*, yet those inputs must
   migrate with the outputs. Two complementary sources recover them and emit them under
   the same `transactional.id` (grouping then folds them in):
   - **Consumer-group enrichment** (`--enrich-consumer-groups`) — the topics a group
     commits offsets for are the topics it consumes; each group is tied to a
     transaction by the Kafka Streams `transactional.id`↔`group.id` naming convention.
   - **`__consumer_offsets` tail** (`--tail-consumer-offsets`) — ties each
     *transactional* offset commit to its transaction by **producer id** (exact, no
     naming assumption), covering arbitrary non-Streams consumer+producer EOS apps. It
     reads an internal topic, so it is availability-gated and falls back to the naming
     heuristic where inaccessible.
4. **Accumulate** — union footprints per `transactional.id` across the whole window.
5. **Group** — union-find over topics: any two topics seen in the same transaction
   merge (transitively). Kafka-internal `__`-prefixed topics are dropped *before*
   grouping (otherwise the shared `__consumer_offsets` would chain every EOS app into
   one group).
6. **Report** — the terminal summary and `migration.yaml`.

> **`__transaction_state` decoding:** there is no off-the-shelf Go decoder for this
> internal format, so `internal/txnlog` is a hand port of Kafka's
> `TransactionLogValue.json` schema (both the v0 classic and v1 flexible/tagged-field
> encodings), covered by golden-vector unit tests.

## Known limitations

- **Compaction is the residual blind spot.** A footprint can only be reconstructed
  from a record that still carries a non-empty partition set. Reading from the
  earliest offset captures the maximum the compacted log retains, but a rare workload
  (e.g. a nightly/weekly job) whose footprint-bearing records were already compacted
  down to their final empty-footprint record before the run is unrecoverable. A longer
  `--duration` mitigates this but never guarantees completeness — the tool reports what
  it observed; it does not claim certainty.

- **Coupling is per-`transactional.id` over the read window.** For each txn-id the tool
  unions every footprint the retained log holds, assuming *one `transactional.id` = one
  stable logical producer*. Two notes on why this is usually right:
  - One producer writing different topics in different transactions **should** merge —
    that producer is one process bound to one cluster and cannot be split at cutover.
  - Kafka fences producers sharing a `transactional.id`, so two live apps cannot
    concurrently reuse one id.

  The genuinely awkward case is **sequential repurposing**: a `transactional.id`
  reassigned to a *different* workload while the *old* footprint records are still in
  the retained window — old and new then superimpose into one group. It is narrow in
  practice (`__transaction_state` is compacted by txn-id, so stale footprints for an
  active id are usually cleaned away) and, for a migration, this over-grouping is
  **conservative**: it errs toward moving too much together, never toward splitting
  things that must stay atomic.

- **Managed offerings are out of scope.** Confluent Cloud and MSK Serverless expose
  neither `__transaction_state` nor the transaction admin APIs, so the tool fails fast
  at preflight. Discovering transactional groups there would need a different vantage
  point (e.g. observing the request stream at a gateway).

## Layout

```
cmd/txn-discovery      CLI entry point (cobra)
internal/config        connection + run configuration
internal/kafka         franz-go client construction (SASL, TLS, mutual TLS)
internal/discovery     Source interface, the __transaction_state reader (source of
                       truth), the shared txn-id/producer-id catalog, the two
                       consumed-input recovery sources, and the accumulator
internal/txnlog        __transaction_state record decoder (v0 + v1), golden-tested
internal/grouping      union-find grouping + internal-topic filtering (unit-tested)
internal/report        terminal summary + migration.yaml writer
```

Each data source implements `discovery.Source` and is appended to the `sources` slice
in `cmd/txn-discovery/main.go`; the accumulator and grouping stages need no changes to
add another.
