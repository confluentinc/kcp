---
title: Collect Kafka Connect Worker Metrics
---

# Collect Kafka Connect Worker Metrics

kcp collects Kafka Connect worker metrics, such as connector and task counts, client byte rates, and per-connector record rates, through the same Jolokia and Prometheus backends it uses for Apache Kafka® broker metrics. This page covers how collection works, which metrics kcp records, and how to configure and override the queries.

[`kcp scan self-managed-connectors`](../command-reference/scan/self-managed-connectors.md) collects the metrics with the same two backends as [broker metrics collection](metrics-collection.md):

| Backend      | Mode             | Required flags | Required `apache-kafka-credentials.yaml` block |
| ------------ | ---------------- | -------------- | ---------------------------------------------- |
| `jolokia`    | Live polling     | `--metrics jolokia` and `--metrics-duration`. `--metrics-interval` is optional, with a default of `10s`. | [`jolokia:`](credentials.md) |
| `prometheus` | Historical query | `--metrics prometheus` and `--metrics-range` | [`prometheus:`](credentials.md) |

Both backends produce the same `ProcessedClusterMetrics` shape in `kcp-state.json`, stored under `self_managed_connectors.metrics` for the matched cluster.

## How collection works

1. `kcp scan self-managed-connectors` discovers connectors through the Connect REST API (`--connect-rest-url`).
2. If you set `--metrics`, it collects Connect worker metrics from Jolokia or Prometheus, using the credentials file (`--credentials-file`).
3. kcp writes both the connector details and the metrics to the state file.

`--credentials-file` takes the same `apache-kafka-credentials.yaml` format as `kcp scan clusters`. For Connect metrics, point the Jolokia endpoints at the Jolokia agents on your Connect workers, not the ones on your Kafka brokers. Connect worker agents typically run on a different host or port.

## Metrics collected

Connect metrics span three levels: worker (cluster-wide), client (network), and per-task (connector throughput).

| Metric                     | Description                                                | Type               | Level    |
| -------------------------- | ---------------------------------------------------------- | ------------------ | -------- |
| `connector-count`          | Number of connectors running on the worker                 | Gauge              | Worker   |
| `task-count`               | Number of tasks running on the worker                      | Gauge              | Worker   |
| `incoming-byte-rate`       | Bytes per second received from Kafka brokers               | Gauge (aggregated) | Client   |
| `outgoing-byte-rate`       | Bytes per second sent to Kafka brokers                     | Gauge (aggregated) | Client   |
| `connection-count`         | Active connections to Kafka brokers                        | Gauge (aggregated) | Client   |
| `request-rate`             | Requests per second to Kafka brokers                       | Gauge (aggregated) | Client   |
| `source-record-write-rate` | Records per second written to Kafka (after transforms)     | Gauge (aggregated) | Per-task |
| `source-record-poll-rate`  | Records per second polled from the source system           | Gauge (aggregated) | Per-task |
| `sink-record-read-rate`    | Records per second read from Kafka                         | Gauge (aggregated) | Per-task |
| `sink-record-send-rate`    | Records per second sent to the sink system (after transforms) | Gauge (aggregated) | Per-task |

## Jolokia MBean paths

| Metric                     | MBean path                                                  | Attribute                  |
| -------------------------- | ----------------------------------------------------------- | -------------------------- |
| `connector-count`          | `kafka.connect:type=connect-worker-metrics`                 | `connector-count`          |
| `task-count`               | `kafka.connect:type=connect-worker-metrics`                 | `task-count`               |
| `incoming-byte-rate`       | `kafka.connect:client-id=*,type=connect-metrics`            | `incoming-byte-rate`       |
| `outgoing-byte-rate`       | `kafka.connect:client-id=*,type=connect-metrics`            | `outgoing-byte-rate`       |
| `connection-count`         | `kafka.connect:client-id=*,type=connect-metrics`            | `connection-count`         |
| `request-rate`             | `kafka.connect:client-id=*,type=connect-metrics`            | `request-rate`             |
| `source-record-write-rate` | `kafka.connect:type=source-task-metrics,connector=*,task=*` | `source-record-write-rate` |
| `source-record-poll-rate`  | `kafka.connect:type=source-task-metrics,connector=*,task=*` | `source-record-poll-rate`  |
| `sink-record-read-rate`    | `kafka.connect:type=sink-task-metrics,connector=*,task=*`   | `sink-record-read-rate`    |
| `sink-record-send-rate`    | `kafka.connect:type=sink-task-metrics,connector=*,task=*`   | `sink-record-send-rate`    |

The client-level metrics (`incoming-byte-rate`, `outgoing-byte-rate`, `connection-count`, and `request-rate`) use wildcard MBean patterns, and kcp sums them across all matching MBeans. Jolokia reads these directly from the Connect worker JVM, so they need no configuration beyond the Jolokia agent.

## Prometheus PromQL queries

| Metric                     | PromQL query                                                              |
| -------------------------- | ------------------------------------------------------------------------- |
| `connector-count`          | `sum(kafka_connect_worker_connector_count)`                               |
| `task-count`               | `sum(kafka_connect_worker_task_count)`                                    |
| `source-record-write-rate` | `sum by (connector) (kafka_connect_source_task_source_record_write_rate)` |
| `source-record-poll-rate`  | `sum by (connector) (kafka_connect_source_task_source_record_poll_rate)`  |
| `sink-record-read-rate`    | `sum by (connector) (kafka_connect_sink_task_sink_record_read_rate)`      |
| `sink-record-send-rate`    | `sum by (connector) (kafka_connect_sink_task_sink_record_send_rate)`      |
| `incoming-byte-rate`       | `sum(kafka_connect_metrics_incoming_byte_rate)`                           |
| `outgoing-byte-rate`       | `sum(kafka_connect_metrics_outgoing_byte_rate)`                           |
| `connection-count`         | `sum(kafka_connect_metrics_connection_count)`                             |
| `request-rate`             | `sum(kafka_connect_metrics_request_rate)`                                 |

The Prometheus JMX Exporter produces these metric names with standard Kafka Connect JMX rules. The worker-level and task-level metrics (`kafka_connect_worker_*` and `kafka_connect_source_task_*`) are typically exported by default. The client-level metrics (`kafka_connect_metrics_*`) need two things in the JMX Exporter configuration:

- An allowlist entry for the `kafka.connect:type=connect-metrics,client-id=*` MBean pattern.
- A rule that captures the `client-id` key. A rule that matches only `type=connect-metrics`, without `client-id`, doesn't match the real ObjectName.

If these metrics aren't exported, kcp logs a warning and collects the remaining metrics.

## Configure the credentials file

Use the same `apache-kafka-credentials.yaml`, with the `jolokia` or `prometheus` block pointing at the Connect worker endpoints:

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

When you collect broker metrics with `kcp scan clusters --metrics jolokia`, the endpoints point at the Jolokia agents on your brokers. When you collect Connect metrics with `kcp scan self-managed-connectors --metrics jolokia`, the same file format applies, but the endpoints point at the agents on your Connect workers.

## Metric-name overrides

If your Prometheus exporter relabels the standard Connect series, or your Jolokia agent exposes Connect MBeans under non-standard object names, the default queries return empty results. As with [broker metric-name overrides](credentials.md#metric-name-overrides), repoint them for each cluster instead of patching names in source:

- `prometheus.connect_metric_names` maps a logical Connect label to the base series name your exporter exposes.
- `jolokia.connect_mbean_overrides` maps a logical Connect label to the MBean object name your agent exposes.

These keys are separate from the broker `metric_names` and `mbean_overrides`, so one block can carry both a broker set and a Connect set.

Both use the same 10 logical Connect labels as keys:

`connector-count`, `task-count`, `incoming-byte-rate`, `outgoing-byte-rate`, `connection-count`, `request-rate`, `source-record-write-rate`, `source-record-poll-rate`, `sink-record-read-rate`, `sink-record-send-rate`.

For example, if your exporter publishes the worker-level gauges under an `acme_` prefix, map only the labels that changed. Every label you omit keeps its default:

```yaml
prometheus:
  url: http://prometheus.example.com:9090
  filter:
    labels:
      job: confluent/connect-jmx-exporter
  connect_metric_names:
    connector-count: acme_connect_connector_count   # default: kafka_connect_worker_connector_count
    task-count: acme_connect_task_count             # default: kafka_connect_worker_task_count
```

kcp puts the name into its own query wrapping, so `task-count` now runs `sum(acme_connect_task_count)` instead of the default, with `filter.labels` still applied. Per-connector labels keep their `sum by (connector) (...)` grouping the same way. The Jolokia equivalent maps the same logical label to a full MBean object name, including the domain, instead of a series name:

```yaml
jolokia:
  endpoints:
    - http://connect-worker1.example.com:8781/jolokia
  connect_mbean_overrides:
    task-count: "acme.connect:type=connect-worker-metrics"   # default domain: kafka.connect
```

- **List only the labels you need to change.** Unlisted labels keep their defaults. kcp treats a key with an empty value, such as `task-count: ""`, as no override and ignores it without a warning, so set a real name or omit the key.
- **Keys are validated when the file loads.** An unknown or misspelled label, including one with the wrong case, is an error that lists the valid labels. kcp validates the keys whenever the credentials file loads, whichever backend `--metrics` selects for the scan.
- **An overridden metric that's still empty logs a warning.** If an overridden Connect metric still returns no data, kcp logs it at WARN, while a missing default metric logs at DEBUG. You configured the override to fix an empty result, so a result that's still empty is worth surfacing.
- **On Jolokia, that warning is fully reliable only for `connector-count` and `task-count`.** These two gauges each read a single object name. The other eight labels, the four client-level aggregates (`incoming-byte-rate`, `outgoing-byte-rate`, `connection-count`, and `request-rate`) and the four per-connector metrics, read a wildcard MBean pattern instead, so kcp detects "not found" only when the pattern matches no MBeans. If the pattern matches MBeans that lack the expected attribute, or for per-connector metrics the `connector` property, the read succeeds with no error: the aggregate totals `0`, and the per-connector series is left out. An override on these eight labels that matches but is misconfigured might not produce a warning at all.
- **An override renames a metric but doesn't reinterpret it.** The series or MBean you point at must have the same shape as the default. Every Connect metric here is a gauge, either a point-in-time count or an already-computed rate, not a raw counter that kcp derives a rate from. On Jolokia, the attribute kcp reads is fixed to the metric's own label: for example, `task-count` always reads the `task-count` attribute from whatever MBean you point it at. The override changes the object name, not the attribute, so your custom MBean must expose an attribute with the same name. If you point at a source with a different shape, scope, or attribute, you get a wrong value with no error, not an empty result.

### Per-connector metrics

Four of the 10 labels are per-connector instead of cluster-wide: `source-record-write-rate`, `source-record-poll-rate`, `sink-record-read-rate`, and `sink-record-send-rate`. kcp attributes these to individual connectors by grouping on a `connector` label: `sum by (connector) (<series>)` on Prometheus, and the `connector=*,task=*` MBean wildcard on Jolokia, as shown in [Jolokia MBean paths](#jolokia-mbean-paths). An override for one of these labels must keep that grouping. If your override points at a relabeled series or MBean that drops it:

- **Prometheus.** kcp skips a query result that has no `connector` label and logs it at WARN, instead of attributing it to the wrong connector or to the whole cluster.
- **Jolokia.** The override value must still end in `connector=*,task=*`, or the equivalent wildcard your agent uses, for the per-task read to resolve to individual connectors at all. An override that drops the wildcard breaks per-connector attribution for that metric, not only its name.

To rename one of these four labels, rename the type or attribute part of the pattern and keep the wildcard. Don't swap in an unrelated fixed MBean or series.

## Filter Prometheus queries by Connect cluster

If one Prometheus instance scrapes several Connect clusters, kcp combines all their metrics by default. To scope queries to one Connect cluster, use `filter.labels` in the credentials file:

```yaml
prometheus:
  url: http://prometheus:9090
  filter:
    labels:
      job: confluent/connect-jmx-exporter
```

This adds `{job="confluent/connect-jmx-exporter"}` to every PromQL query, so kcp gets metrics only for that Connect cluster. The label name and value depend on your Prometheus scrape configuration. Common labels include `job`, `component`, `namespace`, and `pod`.

To find the available labels, query Prometheus directly:

```bash
curl -s -G 'http://prometheus:9090/api/v1/query' \
  --data-urlencode 'query=kafka_connect_worker_connector_count' | jq '.data.result[].metric'
```

## Examples

These commands discover connectors and collect metrics with each backend:

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

After either run, the state file contains both the connector details and the metrics under the matched cluster's `self_managed_connectors` block.

## Related content

- [Apache Kafka credentials file reference](credentials.md)
- [Collect broker metrics from Apache Kafka sources](metrics-collection.md)
- [Get started with kcp](../getting-started.md)
- [`kcp scan self-managed-connectors` command reference](../command-reference/scan/self-managed-connectors.md)
