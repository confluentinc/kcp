---
title: Metrics collection (OSK)
---

# Metrics collection — OSK

For MSK, `kcp discover` pulls cluster metrics straight from CloudWatch. OSK
clusters do not have an equivalent metrics surface, so [`kcp scan clusters`](../command-reference/scan/clusters.md)
supports two collection backends, selected with `--metrics <source>`:

> [!TIP]
> **Connect cluster metrics**
>
> For collecting metrics from Kafka Connect workers (connector count, task
> throughput, byte rates), see
> [Connect metrics collection](connect-metrics-collection.md).

| Backend      | Mode                  | Required flags                                                  | Required `osk-credentials.yaml` block |
| ------------ | --------------------- | --------------------------------------------------------------- | ------------------------------------- |
| `jolokia`    | Live polling          | `--metrics jolokia` + `--metrics-duration` (`--metrics-interval` optional, default `10s`) | [`jolokia:`](osk-credentials.md) |
| `prometheus` | Historical query      | `--metrics prometheus` + `--metrics-range`                      | [`prometheus:`](osk-credentials.md) |

Both backends produce the same `ProcessedClusterMetrics` shape inside
`kcp-state.json`, so reports and the UI work identically regardless of which
one you used.

## Why Jolokia, not direct JMX?

Kafka exposes operational metrics via JMX (Java Management Extensions), a
Java-native monitoring protocol. `kcp` is written in Go, so it cannot speak
JMX directly. Instead, it talks to [Jolokia](https://jolokia.org/) — a
lightweight JVM agent that exposes JMX MBeans as a JSON REST API over HTTP.

Jolokia must be installed on each broker as a JVM agent:

```
KAFKA_OPTS="-javaagent:/path/to/jolokia-agent.jar=port=8778,host=0.0.0.0"
```

Each broker runs its own Jolokia endpoint, so for a multi-broker cluster you
list every endpoint under `jolokia.endpoints` in `osk-credentials.yaml`.

## Counter-based rates, not EWMA

Kafka's JMX rate metrics (`BytesInPerSec`, `MessagesInPerSec`, …) ship several
pre-computed fields including `OneMinuteRate`, `FiveMinuteRate` and
`FifteenMinuteRate`. These are **exponentially weighted moving averages
(EWMA)** — useful for dashboards, but unsuitable for aggregation: consecutive
EWMA samples are highly correlated, so a min/max/average over them
under-states the true variance in traffic.

For the **Jolokia** backend, `kcp` reads the `Count` field — a monotonic
counter of total bytes (or messages) since broker start — and computes the
actual rate over each sample interval:

```
rate = (Count_current - Count_previous) / elapsed_seconds
```

This produces independent data points with real variance, accurately reflecting
traffic over each interval.

The **Prometheus** backend achieves the same result using PromQL's `rate()`
function, which computes per-second rates from counters stored in Prometheus
(see [Prometheus PromQL queries](#prometheus-promql-queries) below).

## Scan duration and poll interval

| Flag                  | Backend     | Effect                                                                                                                                                                                                                                |
| --------------------- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--metrics-duration`  | Jolokia     | How long `kcp` polls. Longer durations capture more data points and are more representative of typical cluster usage. For production clusters, run for **15–30 minutes or more** during a representative traffic period.              |
| `--metrics-interval`  | Jolokia     | How frequently `kcp` samples the counters. Shorter intervals (e.g. `1s`) give higher resolution; longer intervals (e.g. `10s`, `30s`) produce smoother rates averaged over each interval. Default: `10s`.                             |
| `--metrics-range`     | Prometheus  | How far back in time `kcp` queries. Common values: `7d`, `30d`. The query step is auto-selected: `≤1d → 1m`, `≤7d → 5m`, `≤30d → 1h`, `>30d → 2h`.                                                                                    |

A 30-minute scan with a 10-second interval produces 180 data points per
metric — plenty for meaningful analysis of throughput patterns.

## Metrics collected

Names align with the equivalent CloudWatch metric on MSK so that
state-file consumers (reports, UI) treat MSK and OSK identically.

| Metric                    | Description                                       | Type                              |
| ------------------------- | ------------------------------------------------- | --------------------------------- |
| `BytesInPerSec`           | Bytes received by brokers per second              | Rate (from counter)               |
| `BytesOutPerSec`          | Bytes sent to consumers per second                | Rate (from counter)               |
| `MessagesInPerSec`        | Messages received per second                      | Rate (from counter)               |
| `PartitionCount`          | Total partition replicas across queried brokers   | Gauge                             |
| `GlobalPartitionCount`    | Total unique partitions in the cluster            | Gauge (controller only)           |
| `ClientConnectionCount`   | Active client connections across all listeners    | Gauge (aggregated)                |
| `TotalLocalStorageUsage`  | Total log storage in GiB                          | Gauge (aggregated, bytes → GiB)   |

## Jolokia authentication modes

Configured under the `jolokia:` block in `osk-credentials.yaml`:

| Mode                  | Configuration                                                                                                                |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Unauthenticated       | Omit both `auth` and `tls`. Jolokia is reachable without credentials.                                                        |
| Password (HTTP Basic) | Set `auth.username` / `auth.password`.                                                                                       |
| TLS                   | Set `tls.ca_cert` (or `tls.insecure_skip_verify: true` for self-signed). Combinable with password auth.                      |

Prometheus uses the same three modes via its own `auth` and `tls` sub-blocks.
See the [`osk-credentials.yaml` reference](osk-credentials.md) for the full schema.

## Prometheus PromQL queries

Your Prometheus instance must be scraping Kafka broker metrics — typically via a
[JMX Exporter](https://github.com/prometheus/jmx_exporter) — for the queries
below to return data. `kcp` submits one query per metric listed in
[Metrics collected](#metrics-collected) above:

| Metric                  | PromQL query                                                                  |
| ----------------------- | ----------------------------------------------------------------------------- |
| `BytesInPerSec`         | `sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[<window>]))`    |
| `BytesOutPerSec`        | `sum(rate(kafka_server_brokertopicmetrics_bytesoutpersec_total[<window>]))`   |
| `MessagesInPerSec`      | `sum(rate(kafka_server_brokertopicmetrics_messagesinpersec_total[<window>]))` |
| `PartitionCount`        | `sum(kafka_server_replicamanager_partitioncount)`                             |
| `GlobalPartitionCount`  | `kafka_controller_kafkacontroller_value{name="GlobalPartitionCount"}`          |
| `ClientConnectionCount` | `sum(kafka_server_socketservermetrics_connection_count)`                      |
| `TotalLocalStorageUsage`| `sum(kafka_log_log_size) / (1024*1024*1024)`                                 |

`<window>` is a rate window automatically selected to be at least 4× the query
step (minimum `5m`). The step itself is derived from `--metrics-range` as
described in [Scan duration and poll interval](#scan-duration-and-poll-interval).

**Note on `GlobalPartitionCount`:** This metric comes from the
`kafka.controller:type=KafkaController,name=GlobalPartitionCount` MBean, which
only exists on controller nodes. For **Jolokia**, `kcp` queries all broker
endpoints and uses the first successful response. For **Prometheus**, the JMX
Exporter must be scraping the controller pods/brokers for this metric to be
available. With the default JMX Exporter configuration, the metric is exposed as
`kafka_controller_kafkacontroller_value{name="GlobalPartitionCount"}`. If
`GlobalPartitionCount` is not found, `kcp` will log an info message and the
metric will be omitted from results — all other metrics will still be collected.

Ensure your Prometheus instance is scraping the Kafka controller nodes (not just
broker nodes). In Kubernetes with KRaft mode, controllers may run as separate
pods (e.g. `osk-kraftcontroller-*`) that require their own `PodMonitor` or
`ServiceMonitor`.

These metric names (`kafka_server_brokertopicmetrics_*`,
`kafka_server_replicamanager_*`, etc.) are the defaults produced by the
Prometheus JMX Exporter with a standard Kafka configuration. If your exporter
uses custom relabelling rules that rename these metrics, the queries will return
empty results.

## Filtering by cluster (Prometheus)

When a single Prometheus instance scrapes multiple Kafka clusters, use
`filter.labels` in `osk-credentials.yaml` to scope queries to a specific
cluster. See the [`prometheus` field reference](osk-credentials.md#prometheus--optional-for-historical-metrics)
for details.

## Worked examples

```bash
# Live polling for 5 minutes, 10s interval (default)
kcp scan clusters --source-type osk --state-file kcp-state.json \
  --credentials-file osk-credentials.yaml \
  --metrics jolokia --metrics-duration 5m

# Historical pull from Prometheus, last 30 days
kcp scan clusters --source-type osk --state-file kcp-state.json \
  --credentials-file osk-credentials.yaml \
  --metrics prometheus --metrics-range 30d
```

After either run, inspect the result with [`kcp ui`](../command-reference/ui.md) or generate a metrics
report with [`kcp report metrics`](../command-reference/report/metrics.md).
