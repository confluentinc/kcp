# consumer-group-scan integration suite

Validates the **production consumer-group discovery path** against real Kafka
brokers, one per group-protocol milestone, so every group *type* is exercised on
the broker version where that type is actually GA.

## What it proves

The test runs the exact production sequence — `client.NewConsumerGroupClient` →
`ListGroupsWithType` (ListGroups v5) → `DescribeGroups` (classic/unknown types
only) → `Coordinators` → `kafka.BuildConsumerGroups` — against five brokers:

| Broker | Kafka | Cell | Asserts |
|--------|-------|------|---------|
| kafka-3.7 | 3.7.0 | classic (fallback) | `type == ""` (ListGroups maxes at v4), full member detail |
| kafka-3.8 | 3.8.0 | classic | `type == "classic"`, member + client host + decoded topic |
| kafka-4.0 | 4.0.0 | classic + **consumer** | consumer group → `type == "consumer"`, **state Stable** (not the classic-API "Dead"), no members |
| kafka-4.1 | 4.1.0 | **share** | `type == "share"`, state Stable, no members |
| kafka-4.2 | 4.2.0 | **streams** | `type == "streams"`, state Stable, no members |

The non-classic cells encode the core design decision (see
`internal/services/kafka/kafka_service.go` `scanConsumerGroups`): consumer / share
/ streams groups are **not** described via the classic `DescribeGroups` API — it
returns a misleading `Dead`/empty answer for them — so they keep their accurate
`ListGroups v5` state and carry no members.

## Requirements

- **Docker** — pulls `apache/kafka` 3.7.0 / 3.8.0 / 4.0.0 / 4.1.0 / 4.2.0.
- **A JDK (`javac`/`java`)** — the streams cell needs a running Kafka Streams app
  (there is no console tool for streams groups). `setup.sh` compiles
  `StreamsDemo.java` against the broker's own libraries and launches it.

## Run it

```bash
make test-consumer-group-scan
```

`setup.sh` starts the brokers (and enables the `streams.version` feature on 4.2,
then compiles + launches the Streams app); `teardown.sh` stops everything and
removes the generated `klibs/` and `classes/` artifacts.

## CI

This suite runs in Semaphore as the dedicated **`integration: consumer group
scan`** block on the larger machine (five brokers + a Streams JVM), and **blocks
merges on failure**. Deterministic coverage of the mapping/type-dispatch logic
also lives in the fast unit tests (`internal/services/kafka`,
`internal/types`, `internal/client`) which run in the main `test-go` block.
