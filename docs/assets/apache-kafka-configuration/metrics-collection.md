---
title: Collect Broker Metrics From Apache Kafka Sources
---

# Collect Broker Metrics From Apache Kafka Sources

kcp collects broker metrics from Apache Kafka® clusters, including Confluent Platform and other distributions compatible with the Kafka API, through Jolokia or Prometheus. This page covers how to choose and configure a backend, which metrics kcp records, and how it calculates rates.

For Amazon MSK, `kcp discover` pulls cluster metrics from CloudWatch. Apache Kafka clusters have no equivalent metrics source, so [`kcp scan clusters`](../command-reference/scan/clusters.md) supports two collection backends, which you select with `--metrics <source>`:

| Backend      | Mode             | Required flags | Required `apache-kafka-credentials.yaml` block |
| ------------ | ---------------- | -------------- | ---------------------------------------------- |
| `jolokia`    | Live polling     | `--metrics jolokia` and `--metrics-duration`. `--metrics-interval` is optional, with a default of `10s`. | [`jolokia:`](credentials.md) |
| `prometheus` | Historical query | `--metrics prometheus` and `--metrics-range` | [`prometheus:`](credentials.md) |

Both backends produce the same `ProcessedClusterMetrics` shape in `kcp-state.json`, so reports and the UI work the same way whichever backend you use.

To collect metrics from Kafka Connect workers, such as connector count, task throughput, and byte rates, see [Collect Kafka Connect worker metrics](connect-metrics-collection.md).

## Jolokia instead of direct JMX

Kafka exposes operational metrics through Java Management Extensions (JMX), a Java-native monitoring protocol. kcp is written in Go, so it can't use JMX directly. Instead, it talks to [Jolokia](https://jolokia.org/), a lightweight JVM agent that exposes JMX MBeans as a JSON REST API over HTTP.

Install Jolokia on each broker as a JVM agent:

```
KAFKA_OPTS="-javaagent:/path/to/jolokia-agent.jar=port=8778,host=0.0.0.0"
```

Each broker runs its own Jolokia endpoint, so for a cluster with several brokers, list every endpoint under `jolokia.endpoints` in `apache-kafka-credentials.yaml`.

## How kcp calculates rates

Kafka's JMX rate metrics provide `OneMinuteRate`, `FiveMinuteRate`, and `FifteenMinuteRate`, which are exponentially weighted moving averages. They suit dashboards but aggregate poorly: consecutive samples are highly correlated, so a minimum, maximum, or average over them understates the true variance in traffic.

Instead, kcp reads the monotonic `Count` and derives the rate itself over each interval: `(Count_current − Count_previous) / elapsed` on Jolokia, and PromQL's `rate()` on [Prometheus](#prometheus-promql-queries). The result is independent data points that reflect real traffic.

## Scan duration and poll interval

| Flag                 | Backend    | Effect |
| -------------------- | ---------- | ------ |
| `--metrics-duration` | Jolokia    | How long kcp polls. Longer durations capture more data points and better represent typical cluster usage. For production clusters, poll for 15 to 30 minutes or more during a representative traffic period. |
| `--metrics-interval` | Jolokia    | How often kcp samples the counters. Shorter intervals, such as `1s`, give higher resolution. Longer intervals, such as `10s` or `30s`, produce smoother rates averaged over each interval. The default is `10s`. |
| `--metrics-range`    | Prometheus | How far back kcp queries, such as `7d` or `30d`. kcp selects the query step automatically: `≤1d → 1m`, `≤7d → 5m`, `≤30d → 1h`, `>30d → 2h`. |

A 30-minute scan with a 10-second interval produces 180 data points for each metric, which is enough to analyze throughput patterns.

## Metrics collected

Metric names match the equivalent CloudWatch metrics on MSK, so reports and the UI, which read the state file, treat MSK and Apache Kafka the same way.

| Metric                   | Description                                     | Type                                          |
| ------------------------ | ----------------------------------------------- | --------------------------------------------- |
| `BytesInPerSec`          | Bytes received by brokers per second            | Rate (from counter)                           |
| `BytesOutPerSec`         | Bytes sent to consumers per second              | Rate (from counter)                           |
| `MessagesInPerSec`       | Messages received per second                    | Rate (from counter)                           |
| `PartitionCount`         | Total partition replicas across queried brokers | Gauge                                         |
| `GlobalPartitionCount`   | Total unique partitions in the cluster          | Gauge (controller only)                       |
| `ClientConnectionCount`  | Active client connections across all listeners  | Gauge (aggregated)                            |
| `TotalLocalStorageUsage` | Total log storage in GiB                        | Gauge (aggregated, converted from bytes to GiB) |

## Jolokia authentication modes

You configure these modes in the `jolokia:` block of `apache-kafka-credentials.yaml`:

| Mode                  | Configuration |
| --------------------- | ------------- |
| Unauthenticated       | Omit both `auth` and `tls`. Jolokia is reachable without credentials. |
| Password (HTTP Basic) | Set `auth.username` and `auth.password`. |
| TLS                   | Set `tls.ca_cert`, or `tls.insecure_skip_verify: true` for a self-signed certificate. You can combine TLS with password authentication. |

Prometheus uses the same three modes through its own `auth` and `tls` sub-blocks. For the full schema, see the [Apache Kafka credentials file reference](credentials.md).

## Prometheus PromQL queries

For these queries to return data, your Prometheus instance must scrape Kafka broker metrics, typically through the [JMX Exporter](https://github.com/prometheus/jmx_exporter). kcp submits one query for each metric in [Metrics collected](#metrics-collected):

| Metric                   | PromQL query |
| ------------------------ | ------------ |
| `BytesInPerSec`          | `sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[<window>]))` |
| `BytesOutPerSec`         | `sum(rate(kafka_server_brokertopicmetrics_bytesoutpersec_total[<window>]))` |
| `MessagesInPerSec`       | `sum(rate(kafka_server_brokertopicmetrics_messagesinpersec_total[<window>]))` |
| `PartitionCount`         | `sum(kafka_server_replicamanager_partitioncount)` |
| `GlobalPartitionCount`   | `kafka_controller_kafkacontroller_value{name="GlobalPartitionCount"}` |
| `ClientConnectionCount`  | `sum(kafka_server_socketservermetrics_connection_count)` |
| `TotalLocalStorageUsage` | `sum(kafka_log_log_size) / (1024*1024*1024)` |

`<window>` is a rate window that kcp selects automatically to be at least four times the query step, with a minimum of `5m`. kcp derives the step from `--metrics-range`, as described in [Scan duration and poll interval](#scan-duration-and-poll-interval).

### `GlobalPartitionCount`

This metric comes from the `kafka.controller:type=KafkaController,name=GlobalPartitionCount` MBean, which exists only on controller nodes. For Jolokia, kcp queries all broker endpoints and uses the first successful response. For Prometheus, the JMX Exporter must scrape the controller pods or brokers for this metric to be available. With the default JMX Exporter configuration, the metric is exposed as `kafka_controller_kafkacontroller_value{name="GlobalPartitionCount"}`.

If kcp doesn't find `GlobalPartitionCount`, it logs the omission, at WARN for Jolokia and at DEBUG for Prometheus, and leaves the metric out of the results. kcp still collects all other metrics.

Make sure your Prometheus instance scrapes the Kafka controller nodes, not only the broker nodes. In Kubernetes with KRaft mode, controllers might run as separate pods, such as `kafka-kraftcontroller-*`, that need their own `PodMonitor` or `ServiceMonitor`.

### Custom metric names

The metric names in the queries, such as `kafka_server_brokertopicmetrics_*` and `kafka_server_replicamanager_*`, are the defaults the Prometheus JMX Exporter produces with a standard Kafka configuration. If your exporter uses relabeling rules that rename these metrics, the queries return empty results. If that happens, you have three options:

- Fix the exporter's relabeling.
- Add a Prometheus recording rule that republishes the series under its default name.
- Without changing your monitoring stack, point kcp at the names your exporter exposes with `prometheus.metric_names` in `apache-kafka-credentials.yaml`.

The same mechanism exists for non-standard JMX MBean names through `jolokia.mbean_overrides`. For the label list and how overrides behave, see [Metric-name overrides](credentials.md#metric-name-overrides).

## What an override does not change

`prometheus.metric_names` and `jolokia.mbean_overrides` change which identifier kcp queries, either a Prometheus series name or a JMX object name. They don't change how kcp reads the result. The series or MBean you point at must match the shape of the default it replaces. If it doesn't, kcp records a wrong value with no error and, unlike a missing metric, no empty-result warning:

| Metrics                                                           | Target must be |
| ----------------------------------------------------------------- | -------------- |
| `BytesInPerSec`, `BytesOutPerSec`, `MessagesInPerSec`             | A monotonic counter in bytes, or in messages. Jolokia reads its `Count`, and Prometheus wraps it in `rate()`. |
| `PartitionCount`, `ClientConnectionCount`, `GlobalPartitionCount` | A plain gauge. |
| `TotalLocalStorageUsage`                                          | Raw bytes. kcp divides by 1,024³ to report GiB. |

!!! warning
    The sharpest trap is a counter versus a gauge. kcp treats the three `*PerSec` metrics as counters and derives the rate itself, with `rate()` on Prometheus and `Δcount / elapsed` on Jolokia. If you point one at a pre-averaged rate gauge, such as an exporter mapping of `OneMinuteRate` or a native exporter that already publishes a per-second value, kcp takes a rate of a rate and the result is meaningless. Because the query still returns data, no warning fires. A unit mismatch, such as bits instead of bytes, or GiB instead of bytes for storage, fails the same silent way: the number is wrong.

An override also doesn't change the Jolokia attribute, which is fixed: `Count` for counters and `Value` for gauges. The override changes the object name, not the attribute, so the MBean you name must expose its value under the same attribute.

If your setup differs in unit, shape, read attribute, or per-broker aggregation, a name override alone isn't enough. This is common when the relabeling that renamed a metric also changed its `type:` or dropped a label. Fix it at the exporter, or add a Prometheus recording rule that republishes the series in the shape this section describes.

## Filter Prometheus queries by cluster

If one Prometheus instance scrapes several Kafka clusters, use `filter.labels` in `apache-kafka-credentials.yaml` to scope queries to one cluster. For details, see the [`prometheus` field reference](credentials.md#prometheus).

## Examples

These commands collect metrics with each backend:

```bash
# Live polling for 5 minutes, 10s interval (default)
kcp scan clusters --source-type apache-kafka --state-file kcp-state.json \
  --credentials-file apache-kafka-credentials.yaml \
  --metrics jolokia --metrics-duration 5m

# Historical pull from Prometheus, last 30 days
kcp scan clusters --source-type apache-kafka --state-file kcp-state.json \
  --credentials-file apache-kafka-credentials.yaml \
  --metrics prometheus --metrics-range 30d
```

After either run, inspect the result with [`kcp ui`](../command-reference/ui.md), or generate a metrics report with [`kcp report metrics`](../command-reference/report/metrics.md).

## Related content

- [Apache Kafka credentials file reference](credentials.md)
- [Collect Kafka Connect worker metrics](connect-metrics-collection.md)
- [Get started with kcp](../getting-started.md)
- [`kcp scan clusters` command reference](../command-reference/scan/clusters.md)
- GitHub: [Prometheus JMX Exporter](https://github.com/prometheus/jmx_exporter)
