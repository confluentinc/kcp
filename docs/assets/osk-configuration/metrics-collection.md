---
title: Metrics collection (OSK)
---

# Metrics collection — OSK

For MSK, `kcp discover` pulls cluster metrics straight from CloudWatch. OSK
clusters do not have an equivalent metrics surface, so [`kcp scan clusters`](../command-reference/scan/clusters.md)
supports two collection backends, selected with `--metrics <source>`:

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

Instead, `kcp` reads the `Count` field — a monotonic counter of total bytes
(or messages) since broker start — and computes the actual rate over each
sample interval:

```
rate = (Count_current - Count_previous) / elapsed_seconds
```

This produces independent data points with real variance, accurately reflecting
traffic over each interval.

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
| `PartitionCount`          | Total partitions across queried brokers           | Gauge                             |
| `GlobalPartitionCount`    | Same as `PartitionCount` (summed across brokers)  | Gauge                             |
| `ClientConnectionCount`   | Active client connections across all listeners    | Gauge (aggregated)                |
| `TotalLocalStorageUsage`  | Total log storage in GB                           | Gauge (aggregated, bytes → GB)    |

## Jolokia authentication modes

Configured under the `jolokia:` block in `osk-credentials.yaml`:

| Mode                  | Configuration                                                                                                                |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Unauthenticated       | Omit both `auth` and `tls`. Jolokia is reachable without credentials.                                                        |
| Password (HTTP Basic) | Set `auth.username` / `auth.password`.                                                                                       |
| TLS                   | Set `tls.ca_cert` (or `tls.insecure_skip_verify: true` for self-signed). Combinable with password auth.                      |

Prometheus uses the same three modes via its own `auth` and `tls` sub-blocks.
See the [`osk-credentials.yaml` reference](osk-credentials.md) for the full schema.

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
