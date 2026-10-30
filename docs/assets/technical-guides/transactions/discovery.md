# Discover transactional workloads

Most topics in a Kafka cluster are independent of one another. You can move `clickstream` to Confluent Cloud today and `user-profiles` next month, and neither will notice. Transactional workloads are the exception. When a producer writes to several topics inside one Kafka transaction, those writes commit or abort together, and downstream consumers reading with `isolation.level=read_committed` rely on that all-or-nothing behavior. If you cut those topics over on different days, a single transaction ends up split across two clusters, and the guarantee the application was built on is gone.

This guide walks through finding those workloads before you plan a migration. You will run a small standalone utility, `txn-discovery`, against your source cluster, let it watch for a while, and get back a list of **transaction groups**: sets of topics that must move together. Everything it doesn't put in a group can be migrated one topic at a time.

!!! warning "Proof of concept"
    `txn-discovery` is an exploratory utility that lives in the KCP repository under `utils/transaction-discovery`. It is not part of the `kcp` binary and its output is not consumed by any `kcp` command yet. Treat the results as input to your own migration planning, and review them before acting on them.

## Prerequisites and considerations

**Cluster support**

- Supported: self-managed Apache Kafka, Confluent Platform, and AWS MSK Provisioned. These expose the `__transaction_state` internal topic for reading.
- Not supported: Confluent Cloud and MSK Serverless. They don't expose `__transaction_state`, so the utility stops at its first check with an error naming the topic.

**Access**

- A principal that can read internal topics. The exact ACLs are listed in [Grant permissions](#grant-permissions).
- Network access from the machine running the utility to the source brokers.

**Build tooling**

- Go 1.25 or later to build the utility. The result is a single static binary with no runtime dependencies, so you can build once and copy it to whichever host can reach the cluster.

**Time**

- The utility reports what it *observed*, so it needs to run long enough to see your transactional workloads. Plan for anywhere from a few minutes (to try it out) up to a week (to catch infrequent jobs). See [Choose an observation window](#choose-an-observation-window).

## What this guide covers

1. How the utility works out which topics belong together.
2. Building the utility and granting it read-only permissions.
3. Running discovery and choosing how long to observe.
4. Reading the terminal summary and `migration.yaml`.
5. Understanding read-process-write groups.
6. The limits of what discovery can see.

## How the utility finds transaction groups

Kafka records every transaction in an internal topic called `__transaction_state`. Each time a producer with a `transactional.id` adds partitions to a transaction, the transaction coordinator logs it, and that log is the one place that knows which topics a given transaction touched.

`txn-discovery` reads `__transaction_state` from the earliest offset and keeps going for the length of the window. From each record it works out the transaction's **footprint**: the set of topics it wrote to, recorded against its `transactional.id`.

Grouping then comes down to one rule: *topics that appear in the same transaction belong together*, and the rule is transitive. Take four topics, `A` to `D`:

- One transaction writes to `A` and `B`.
- Another writes to `B` and `C`.
- `D` only ever appears in transactions that touch no other topic.

`A` and `B` are coupled, `B` and `C` are coupled, so all three form one group. `D` stands alone. Internal topics (anything starting with `__`) are left out before grouping. Every exactly-once application commits to `__consumer_offsets`, and keeping it in would chain every unrelated workload into one enormous group.

### Recovering topics a transaction only reads

A transaction's footprint only lists topics it *writes*. That's a problem for consume-transform-produce applications, the Kafka Streams and exactly-once consumer-plus-producer pattern. Such an application reads from an input topic, writes results to an output topic, and commits the consumed offsets as part of the same transaction. The footprint shows the output topic, but the input topic is invisible, even though moving the output without its input breaks the exactly-once chain at cutover.

To close that gap, the utility has two ways of finding the consumed inputs:

| Mechanism                  | Flag                       | How it works                                                                                                                                          | Limits                                                                               |
| -------------------------- | -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| Consumer-group enrichment  | `--enrich-consumer-groups` | Lists consumer groups and their committed offsets, and ties a group to a transaction through the Kafka Streams `transactional.id` / `group.id` naming. | Relies on the naming convention, so it mostly covers Kafka Streams.                  |
| `__consumer_offsets` tail  | `--tail-consumer-offsets`  | Watches transactional offset commits and ties each one to its transaction by producer ID.                                                             | Needs read access to an internal topic, and only sees commits made during the window. |

Both are on by default. The producer ID match is exact and makes no assumptions about naming, so it also covers hand-written consumer-plus-producer applications. Where `__consumer_offsets` can't be read, the utility falls back to the naming heuristic and says so.

## Build the utility

The utility is a self-contained Go module inside the KCP repository.

```bash
cd utils/transaction-discovery
make build    # produces bin/txn-discovery for the current platform
```

To build for another operating system or architecture, for example when the bastion host differs from your workstation, cross-compile all targets at once:

```bash
make dist     # static binaries for linux, darwin, and windows in dist/
```

## Grant permissions

The utility only reads. Give the principal it connects as the following ACLs:

| Resource                    | ACL        | Used for                                                                          |
| --------------------------- | ---------- | --------------------------------------------------------------------------------- |
| Topic `__transaction_state` | `READ`     | Reconstructing each transaction's footprint. Required.                            |
| Topic `__consumer_offsets`  | `READ`     | Recovering consumed inputs by producer ID.                                        |
| Consumer groups (all)       | `DESCRIBE` | Recovering consumed inputs through the Kafka Streams naming convention.           |

If you can't grant the last two, turn those sources off with `--tail-consumer-offsets=false` and `--enrich-consumer-groups=false`. Discovery still works from `__transaction_state` alone, but consumed input topics won't be recovered, so read-process-write groups will be incomplete.

!!! tip
    If the first run fails with an authorization error, check these ACLs first.

## Run discovery

Pick the invocation that matches how your cluster authenticates. Passing the password through `TXN_DISCOVERY_PASSWORD` keeps it out of your shell history and the process list.

=== "SASL/SCRAM over TLS (typical MSK)"

    MSK uses a public CA, so no `--ca-cert` is needed.

    ```bash
    export TXN_DISCOVERY_PASSWORD='<password>'
    bin/txn-discovery \
      --brokers broker1:9096,broker2:9096,broker3:9096 \
      --sasl scram-sha-512 \
      --username my-user \
      --duration 15m
    ```

=== "SASL/PLAIN over TLS (private CA)"

    ```bash
    export TXN_DISCOVERY_PASSWORD='<password>'
    bin/txn-discovery \
      --brokers broker1:9093,broker2:9093 \
      --sasl plain --username my-user \
      --ca-cert /path/to/ca.pem \
      --duration 12h
    ```

=== "Mutual TLS"

    ```bash
    bin/txn-discovery \
      --brokers broker1:9093,broker2:9093 \
      --sasl none \
      --ca-cert /path/to/ca.pem \
      --tls-cert /path/to/client.pem \
      --tls-key /path/to/client-key.pem \
      --duration 168h
    ```

Other combinations follow the same pattern: add `--tls=false` to any SASL option for a plaintext listener, or `--sasl none --tls=false` for an unauthenticated development cluster. `--tls-insecure` skips certificate verification and is for local testing only.

Before it starts observing, the utility checks that it can read `__transaction_state`. If it can't, it exits immediately and tells you whether the problem is connectivity, credentials, or a cluster type that doesn't expose the topic.

### Choose an observation window

`--duration` is the main lever on how complete the result is, and it defaults to five minutes. A transaction can only join a group if the utility sees it, so a nightly or weekly job that doesn't run during a short window is invisible.

| Goal                                      | Suggested window                                   |
| ----------------------------------------- | -------------------------------------------------- |
| Check that credentials and ACLs work      | `5m` (the default)                                 |
| Cover steady, always-on workloads         | An hour or more                                    |
| Include daily and weekly batch jobs       | Long enough to span their schedule, for example `168h` |

The utility stops early on `Ctrl-C` or `SIGTERM` and writes whatever it has seen so far, so a long run that you need to cut short isn't wasted.

### Useful flags

| Flag                      | Default          | Purpose                                                                 |
| ------------------------- | ---------------- | ----------------------------------------------------------------------- |
| `--duration`              | `5m`             | Total observation window.                                               |
| `--interval`              | `30s`            | How often consumed-input recovery refreshes.                            |
| `--out`                   | `migration.yaml` | Where to write the discovered groups.                                   |
| `--dry-run`               | `false`          | Print the summary without writing the file.                             |
| `--stats-out`             | off              | Also write a JSON diagnostics report with every transaction footprint.  |

Run `txn-discovery --help` for the complete list.

## Read the results

Suppose your cluster runs four transactional applications:

- `checkout-service` writes `orders` and `order-events` in one transaction.
- `fulfilment-service` writes `order-events` and `shipments` in one transaction.
- `payments-processor`, a Kafka Streams application, consumes `payment-requests` and produces `payments-approved` and `payments-ledger`.
- `audit-service` writes only to `audit-trail`.

When the window ends, the utility prints a summary.

```text
============================================================
 Transaction discovery summary
============================================================
  window                 : 15m0s (enrichment interval 30s)
  sources                : transaction-state-log, consumer-groups, consumer-offsets-log
  transactional producers: 4 observed
  transactions           : 1842 committed, 3 aborted (completions seen on the transaction-state log this window)
  topics                 : 7 total — 6 in 2 groups, 1 individual
  read-process-write     : 1 group; 1 consumed input topic(s) recovered

Transaction groups (topics coupled by a shared transaction — migrate each group atomically):

  group-1 (2 ids, 3 topics)
    order-events
    orders
    shipments

  group-2 (1 id, 3 topics)  [read-process-write]
    payment-requests   <- consumed input, recovered
    payments-approved
    payments-ledger

Individual topics (no cross-topic transaction — can migrate one at a time): 1
    audit-trail

Read-process-write (exactly-once consume-transform-produce) apps detected.
  2 topic(s) are produced inside consume-transform-produce transactions.
  Consumed input topic(s) recovered and folded into their group(s):
    - exact producer-id correlation via __consumer_offsets: payment-requests
```

Read it top to bottom:

- **`group-1`** is the transitive rule at work. Neither application writes to all three topics, but both write to `order-events`, which links them.
- **`group-2`** contains `payment-requests` even though the payments application never *wrote* to it. It was recovered as a consumed input and is flagged in the listing.
- **`audit-trail`** was written to inside a transaction but never alongside another topic, so it's listed as an individual topic.

Groups are numbered by size, largest first. Long groups are truncated to 20 topics in the terminal; the full list is always in the YAML.

!!! note "Check the keep-up block"
    Below the summary, a `Keep-up` block reports how well the `__transaction_state` reader kept pace: records seen, lag, and any records it failed to decode. A lag that grows and never recovers, or a decode-failure warning, means the run did not observe the whole window. Rerun with a longer `--duration`, or from a host with a better network path to the brokers, before trusting the result.

### The `migration.yaml` file

The same result is written to `migration.yaml`, which is the file you review and keep.

```yaml
generated_by: kcp txn-discovery (POC)
generated_at: "2026-10-29T10:15:00Z"
observation_window: 15m0s
groups:
    - name: group-1
      bootstrap_url: ""
      read_process_write: false
      topics:
        - order-events
        - orders
        - shipments
      transactional_ids:
        - checkout-service
        - fulfilment-service
    - name: group-2
      bootstrap_url: ""
      read_process_write: true
      warning: 'read-process-write group: consumed input topic(s) recovered and included via exact producer-id correlation via __consumer_offsets.'
      topics:
        - payment-requests
        - payments-approved
        - payments-ledger
      transactional_ids:
        - payments-processor-3f2c9a1e-7b44-4d0a-9c1d-5e8a2b6f0c11-1
individual_topic_count: 1
individual_topics:
    - audit-trail
```

Each group lists its topics and the `transactional.id` values behind them, which tells you which applications to coordinate with. The `bootstrap_url` field is left empty for you to fill in for the target cluster. The schema is provisional and may change.

## Read-process-write groups

A group marked `read_process_write: true` contains at least one consume-transform-produce application. These deserve a closer look because the input topics are found indirectly, and the `warning` field tells you how:

- **Recovered by producer ID** is the strongest case. The utility saw the application commit offsets inside a transaction and matched it exactly.
- **Recovered through Kafka Streams naming** relies on the `transactional.id` and `group.id` sharing a name. It's reliable for Kafka Streams and unreliable for anything else.
- **Not captured** means the group has a read-process-write producer but no input was recovered. The warning says to review the group before cutover, and you should: find out what the application consumes and add it to the group by hand.

If the `__consumer_offsets` tail was inactive, the summary tells you that non-Streams applications may be unrecovered. Treat every read-process-write group as one you verify with its owning team.

## Understand what discovery can miss

Discovery tells you what it observed. It doesn't prove that nothing else exists.

**Compaction.** `__transaction_state` is compacted by `transactional.id`. A transaction's footprint can only be rebuilt from a record that still lists its partitions, and once a job finishes, its latest record has an empty partition set. If a rare job ran and was compacted down to that final record before your window opened, it can't be recovered. Reading from the earliest offset gets the most that the log retains, and a longer window helps catch jobs while they run, but neither guarantees completeness.

**Reused transactional IDs.** If a `transactional.id` was reassigned to a different workload while the old footprint records are still retained, the two workloads merge into one group. This is rare, and it errs in the safe direction: you move too much together, never too little.

**Managed clusters.** Confluent Cloud and MSK Serverless don't expose the data discovery depends on, so it can't run there at all.

Given those limits, a useful last step is to compare the discovered `transactional_ids` against what your application teams tell you they run. Anything you expect that isn't listed is worth a conversation.

## Use the results in your migration plan

Treat the output as the shape of your cutover schedule:

- **Each group is one unit of cutover.** Define a single migration group in your plan that contains all of its topics, so they flip together.
- **Individual topics are free to move on their own**, in whatever order suits you.
- **Involve the owners.** The `transactional_ids` identify the applications behind each group. Those teams need to know which topics their application's cutover is tied to.

## Related content

- [Source compatibility](../../source-compatibility.md)
- [Migration manifest reference](../../migration-assets/cutover/overview.md)
