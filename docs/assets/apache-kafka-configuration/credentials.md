---
title: Apache Kafka credentials
---

# `apache-kafka-credentials.yaml`

The Apache Kafka® source has no automated discovery step — instead, you
hand-author an `apache-kafka-credentials.yaml` describing how `kcp` should connect to
your cluster(s). It is consumed by [`kcp scan clusters --source-type apache-kafka`](../command-reference/scan/clusters.md)
and any downstream `kcp create-asset` commands that target an Apache Kafka source.

For comparison, the MSK equivalent (`msk-credentials.yaml`) is generated for
you by [`kcp discover`](../command-reference/discover.md) — there is no MSK
counterpart to this file because MSK metadata comes from the AWS APIs.

## Minimal example

```yaml
clusters:
  - id: prod-kafka
    bootstrap_servers:
      - broker1.example.com:9092
      - broker2.example.com:9092
    auth_method:
      sasl_scram:
        use: true
        username: admin
        password: changeme
        mechanism: SHA256
```

## Full example — multiple clusters, mixed auth, metrics

```yaml
# Apache Kafka Credentials Configuration
# Configure your Apache Kafka cluster connection details.

clusters:
  # Production cluster: SASL/SCRAM + Jolokia metrics collection.
  - id: production-kafka-us-east
    bootstrap_servers:
      - broker1.prod.example.com:9092
      - broker2.prod.example.com:9092
      - broker3.prod.example.com:9092
    auth_method:
      sasl_scram:
        use: true
        username: admin
        password: changeme
        mechanism: SHA256
    jolokia:
      endpoints:
        - http://broker1.prod.example.com:8778/jolokia
        - http://broker2.prod.example.com:8778/jolokia
        - http://broker3.prod.example.com:8778/jolokia
      auth:
        username: monitorRole
        password: monitorPass
    # Alternative: query Prometheus instead of polling Jolokia.
    # prometheus:
    #   url: http://prometheus.prod.example.com:9090
    #   auth:
    #     username: promuser
    #     password: prompass
    metadata:
      environment: production
      location: us-datacenter-1

  # Staging cluster: mTLS.
  - id: staging-kafka
    bootstrap_servers:
      - broker1.staging.example.com:9093
    auth_method:
      tls:
        use: true
        ca_cert: /path/to/ca-cert.pem
        client_cert: /path/to/client-cert.pem
        client_key: /path/to/client-key.pem
    metadata:
      environment: staging

  # Dev cluster: no auth (test environments only).
  - id: dev-kafka
    bootstrap_servers:
      - localhost:9092
    auth_method:
      unauthenticated_plaintext:
        use: true
    metadata:
      environment: development
```

## Field reference

### Top-level

| Field      | Type     | Required | Description                                          |
| ---------- | -------- | -------- | ---------------------------------------------------- |
| `clusters` | list     | yes      | One or more cluster entries — `kcp` scans each one.  |

### Per-cluster

| Field               | Type             | Required | Description                                                                                                                                  |
| ------------------- | ---------------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`                | string           | yes      | Unique identifier for the cluster. Used as the cluster key in `kcp-state.json` and as the `--cluster-id` value for downstream `create-asset` commands. |
| `bootstrap_servers` | list of strings  | yes      | Broker `host:port` addresses.                                                                                                                |
| `auth_method`       | object           | yes      | Authentication method. Choose **exactly one** sub-block (see below).                                                                         |
| `jolokia`           | object           | no       | Jolokia HTTP endpoints for live JMX metrics. Required if you pass `--metrics jolokia` on `kcp scan clusters` or `kcp scan self-managed-connectors`. For Connect metrics, point endpoints to Connect workers rather than brokers. |
| `prometheus`        | object           | no       | Prometheus HTTP API for historical metrics. Required if you pass `--metrics prometheus` on `kcp scan clusters` or `kcp scan self-managed-connectors`. |
| `metadata`          | map<string,string> | no     | Free-form labels surfaced in reports and the UI (e.g. `environment`, `location`).                                                            |

### `auth_method` — pick one

| Sub-block                   | Use case                              | Required fields                                                  |
| --------------------------- | ------------------------------------- | ---------------------------------------------------------------- |
| `sasl_scram`                | SASL/SCRAM-SHA-256 or SHA-512         | `use: true`, `username`, `password`, `mechanism` (`SHA256`/`SHA512`) |
| `sasl_plain`                | SASL/PLAIN                            | `use: true`, `username`, `password`                              |
| `tls`                       | TLS / mTLS with client certs          | `use: true`, `ca_cert`, `client_cert`, `client_key`              |
| `unauthenticated_plaintext` | No auth (test environments only)      | `use: true`                                                      |

!!! note "SCRAM mechanism for Apache Kafka vs MSK"

    Apache Kafka supports both `SHA256` and `SHA512`. `SHA256` is the more common default, so `kcp` does not infer one for you — set `mechanism` explicitly.

    AWS MSK only supports `SHA512`; `kcp discover` sets that automatically when generating `msk-credentials.yaml`.

### `jolokia` — optional, for live metrics

```yaml
jolokia:
  endpoints:
    - http://broker1:8778/jolokia
    - http://broker2:8778/jolokia
  auth:                         # optional — omit for unauthenticated Jolokia
    username: monitorRole
    password: secret
  tls:                          # optional — omit for plain HTTP
    ca_cert: /path/to/ca.pem
    insecure_skip_verify: false
  mbean_overrides:              # optional — see "Metric-name overrides" below
    BytesInPerSec: "acme.kafka:type=BrokerTopicMetrics,name=BytesInPerSec"
```

| Field                       | Required | Description                                                          |
| --------------------------- | -------- | -------------------------------------------------------------------- |
| `endpoints`                 | yes      | List of Jolokia HTTP endpoints — one per broker.                     |
| `auth.username` / `password`| no       | HTTP basic auth credentials.                                         |
| `tls.ca_cert`               | no       | CA certificate for HTTPS Jolokia endpoints.                          |
| `tls.insecure_skip_verify`  | no       | Skip TLS verification (test environments only).                      |
| `mbean_overrides`           | no       | Map of logical metric label → MBean object name for agents that expose non-standard MBean names. See [Metric-name overrides](#metric-name-overrides). |

### `prometheus` — optional, for historical metrics

```yaml
prometheus:
  url: http://prometheus:9090
  auth:                         # optional — omit for unauthenticated
    username: promuser
    password: prompass
  tls:                          # optional — omit for plain HTTP
    ca_cert: /path/to/ca.pem
    insecure_skip_verify: false
  filter:                       # optional — scope queries to a specific target
    labels:
      job: confluent/kafka-jmx-exporter
  metric_names:                 # optional — see "Metric-name overrides" below
    BytesInPerSec: acme_broker_bytesin_total
    MessagesInPerSec: acme_broker_messagesin_total
```

| Field                       | Required | Description                                                          |
| --------------------------- | -------- | -------------------------------------------------------------------- |
| `url`                       | yes      | Prometheus server URL.                                               |
| `auth.username` / `password`| no       | HTTP basic auth credentials.                                         |
| `tls.ca_cert`               | no       | CA certificate for HTTPS Prometheus endpoints.                       |
| `tls.insecure_skip_verify`  | no       | Skip TLS verification (test environments only).                      |
| `filter.labels`             | no       | Map of Prometheus label selectors to scope queries. When set, all PromQL queries include these as `{key="value"}` filters. Useful when a single Prometheus scrapes multiple clusters. |
| `metric_names`              | no       | Map of logical metric label → base Prometheus series name for exporters that relabel the standard series. See [Metric-name overrides](#metric-name-overrides). |

`jolokia` and `prometheus` are mutually exclusive per scan invocation — `--metrics`
selects which one `kcp` reads. You can keep both blocks in the file and switch
between them by changing the flag.

### Metric-name overrides

If your Prometheus exporter relabels the standard broker series, or your Jolokia
agent exposes broker MBeans under non-standard object names, the built-in queries
return empty results. Rather than patching those names in source, repoint them per
cluster: `prometheus.metric_names` maps a logical label to the base **series name**
your exporter exposes, and `jolokia.mbean_overrides` maps a logical label to the
**MBean object name** your agent exposes.

Both key on the same seven logical labels:

`BytesInPerSec`, `BytesOutPerSec`, `MessagesInPerSec`, `PartitionCount`,
`GlobalPartitionCount`, `ClientConnectionCount`, `TotalLocalStorageUsage`.

- **Only labels you need to change** must appear; unlisted labels keep their
  defaults. A key with an empty value (`BytesInPerSec: ""`) is treated as *no
  override* and silently ignored — set a real name or omit the key entirely.
- **Keys are validated at load time** — an unknown or misspelled label (wrong case
  included) is a hard error listing the valid labels, not a silent no-op. Both
  blocks are validated whenever the file loads, regardless of which one `--metrics`
  selects, so a typo in the block you are *not* currently scanning with still fails
  the scan.
- **Prometheus overrides replace the base series name only.** `kcp` keeps its own
  wrapping (`sum(rate(<name>[<window>]))`, `sum(<name>)`, the GiB conversion) and
  `filter.labels` injection, so the override is a rename, not a full-query rewrite.
  For `GlobalPartitionCount` the `{name="GlobalPartitionCount"}` discriminator is
  preserved on top of the overridden series name — so your relabelled series must
  still carry the `name="GlobalPartitionCount"` label, or the preserved
  discriminator filters it down to nothing.
- If an overridden metric **still** returns no data, `kcp` logs it at **WARN**
  (a plain missing default is logged at DEBUG) — the override was configured
  precisely to fix an empty result, so a still-empty result is worth surfacing.

## Where to go next

- [`kcp scan clusters`](../command-reference/scan/clusters.md) — pass this file with `--credentials-file`.
- [Metrics collection](metrics-collection.md) — design notes on Jolokia vs Prometheus, the broker metrics that `kcp` records, and how rates are computed.
- [Connect metrics collection](connect-metrics-collection.md) — collecting metrics from Kafka Connect workers.
