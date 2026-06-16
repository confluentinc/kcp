---
title: Connect metrics collection (Apache Kafka)
---

# Connect metrics collection — Apache Kafka

[`kcp scan self-managed-connectors`](../command-reference/scan/self-managed-connectors.md)
supports collecting Kafka Connect worker metrics using the same two backends
as [broker metrics collection](metrics-collection.md):

| Backend      | Mode                  | Required flags                                                  | Required `apache-kafka-credentials.yaml` block |
| ------------ | --------------------- | --------------------------------------------------------------- | ------------------------------------- |
| `jolokia`    | Live polling          | `--metrics jolokia` + `--metrics-duration` (`--metrics-interval` optional, default `10s`) | [`jolokia:`](apache-kafka-credentials.md) |
| `prometheus` | Historical query      | `--metrics prometheus` + `--metrics-range`                      | [`prometheus:`](apache-kafka-credentials.md) |

Both backends produce the same `ProcessedClusterMetrics` shape inside
`kcp-state.json`, stored under `self_managed_connectors.metrics` for the
matched cluster.

## How it works

1. `kcp scan self-managed-connectors` discovers connectors via the Connect REST
   API (`--connect-rest-url`).
2. If `--metrics` is set, it then collects Connect worker metrics from
   Jolokia or Prometheus using the credentials file (`--credentials-file`).
3. Both connector details and metrics are written to the state file.

The `--credentials-file` uses the same `apache-kafka-credentials.yaml` format as
`kcp scan clusters`, but the Jolokia endpoints should point to **Connect
worker** Jolokia instances, not Kafka broker instances.

## Metrics collected

Connect metrics span three levels: worker (cluster-wide), client (network),
and per-task (connector throughput).

| Metric                    | Description                                         | Type                | Level    |
| ------------------------- | --------------------------------------------------- | ------------------- | -------- |
| `connector-count`         | Number of connectors running on the worker          | Gauge               | Worker   |
| `task-count`              | Number of tasks running on the worker               | Gauge               | Worker   |
| `incoming-byte-rate`      | Bytes/sec received from Kafka brokers               | Gauge (aggregated)  | Client   |
| `outgoing-byte-rate`      | Bytes/sec sent to Kafka brokers                     | Gauge (aggregated)  | Client   |
| `connection-count`        | Active connections to Kafka brokers                 | Gauge (aggregated)  | Client   |
| `request-rate`            | Requests/sec to Kafka brokers                       | Gauge (aggregated)  | Client   |
| `source-record-write-rate`| Records/sec written to Kafka (after transforms)     | Gauge (aggregated)  | Per-task |
| `source-record-poll-rate` | Records/sec polled from source system               | Gauge (aggregated)  | Per-task |

## Jolokia MBean paths

| Metric                    | MBean path                                                                    | Attribute                |
| ------------------------- | ----------------------------------------------------------------------------- | ------------------------ |
| `connector-count`         | `kafka.connect:type=connect-worker-metrics`                                   | `connector-count`        |
| `task-count`              | `kafka.connect:type=connect-worker-metrics`                                   | `task-count`             |
| `incoming-byte-rate`      | `kafka.connect:client-id=*,type=connect-metrics`                              | `incoming-byte-rate`     |
| `outgoing-byte-rate`      | `kafka.connect:client-id=*,type=connect-metrics`                              | `outgoing-byte-rate`     |
| `connection-count`        | `kafka.connect:client-id=*,type=connect-metrics`                              | `connection-count`       |
| `request-rate`            | `kafka.connect:client-id=*,type=connect-metrics`                              | `request-rate`           |
| `source-record-write-rate`| `kafka.connect:type=source-task-metrics,connector=*,task=*`                   | `source-record-write-rate`|
| `source-record-poll-rate` | `kafka.connect:type=source-task-metrics,connector=*,task=*`                   | `source-record-poll-rate` |

Client-level metrics (`incoming-byte-rate`, `outgoing-byte-rate`,
`connection-count`, `request-rate`) use wildcard MBean patterns and are summed
across all matching MBeans. Jolokia reads these directly from the Connect worker
JVM — no additional configuration is required beyond the Jolokia agent.

## Prometheus PromQL queries

| Metric                    | PromQL query                                                  |
| ------------------------- | ------------------------------------------------------------- |
| `connector-count`         | `sum(kafka_connect_worker_connector_count)`                   |
| `task-count`              | `sum(kafka_connect_worker_task_count)`                        |
| `source-record-write-rate`| `sum(kafka_connect_source_task_source_record_write_rate)`     |
| `source-record-poll-rate` | `sum(kafka_connect_source_task_source_record_poll_rate)`      |
| `incoming-byte-rate`      | `sum(kafka_connect_network_io_incoming_byte_rate)`            |
| `outgoing-byte-rate`      | `sum(kafka_connect_network_io_outgoing_byte_rate)`            |
| `connection-count`        | `sum(kafka_connect_network_io_connection_count)`              |
| `request-rate`            | `sum(kafka_connect_network_io_request_rate)`                  |

These metric names are produced by the Prometheus JMX Exporter with standard
Kafka Connect JMX rules. The worker-level and task-level metrics
(`kafka_connect_worker_*`, `kafka_connect_source_task_*`) are typically exported
by default. The client-level metrics (`kafka_connect_network_io_*`) require the
JMX exporter to whitelist the `kafka.connect:client-id=*,type=connect-metrics`
MBean pattern — if these are not exported, `kcp` logs a warning and continues
collecting the remaining metrics.

## Credentials file

The same `apache-kafka-credentials.yaml` is used, with the `jolokia` or `prometheus`
block pointing to the Connect worker endpoints:

```yaml
clusters:
  - id: my-kafka-cluster
    bootstrap_servers:
      - broker1:9092
    auth_method:
      unauthenticated_plaintext:
        use: true
    # For Connect metrics via Jolokia — point to Connect workers, not brokers
    jolokia:
      endpoints:
        - http://connect-worker1:8781/jolokia
      auth:                         # optional
        username: monitorRole
        password: secret
    # Alternative: query Prometheus for historical Connect metrics
    # prometheus:
    #   url: http://prometheus:9090
    #   filter:                     # scope queries to this Connect cluster
    #     labels:
    #       job: confluent/connect-jmx-exporter
```

> [!NOTE]
> **Jolokia endpoints for Connect vs brokers**
>
> When collecting **broker** metrics (`kcp scan clusters --metrics jolokia`),
> the Jolokia endpoints point to **Kafka broker** Jolokia agents. When
> collecting **Connect** metrics (`kcp scan self-managed-connectors --metrics jolokia`),
> the same credentials file is used but the endpoints should point to
> **Connect worker** Jolokia agents, which typically run on a different
> host and/or port.

## Filtering by Connect cluster (Prometheus)

When a single Prometheus instance scrapes multiple Connect clusters, all
metrics are combined by default. Use `filter.labels` in the credentials file
to scope queries to a specific Connect cluster:

```yaml
prometheus:
  url: http://prometheus:9090
  filter:
    labels:
      job: confluent/connect-jmx-exporter
```

This appends `{job="confluent/connect-jmx-exporter"}` to every PromQL query,
returning metrics only for that Connect cluster. The label name and value
depend on your Prometheus scrape configuration — common labels include `job`,
`component`, `namespace`, or `pod`.

To discover available labels, query Prometheus directly:

```bash
curl -s -G 'http://prometheus:9090/api/v1/query' \
  --data-urlencode 'query=kafka_connect_worker_connector_count' | jq '.data.result[].metric'
```

## Worked examples

```bash
# Discover connectors + collect live Jolokia metrics for 5 minutes
kcp scan self-managed-connectors \
  --state-file kcp-state.json \
  --connect-rest-url http://connect:8083 \
  --cluster-id my-kafka-cluster \
  --use-unauthenticated \
  --credentials-file apache-kafka-credentials.yaml \
  --metrics jolokia --metrics-duration 5m --metrics-interval 10s

# Discover connectors + pull historical Prometheus metrics (last 7 days)
kcp scan self-managed-connectors \
  --state-file kcp-state.json \
  --connect-rest-url http://connect:8083 \
  --cluster-id my-kafka-cluster \
  --use-unauthenticated \
  --credentials-file apache-kafka-credentials.yaml \
  --metrics prometheus --metrics-range 7d
```

After either run, the state file contains both connector details and metrics
under the matched cluster's `self_managed_connectors` block.
