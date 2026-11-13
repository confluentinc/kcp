---
title: Apache Kafka Credentials File Reference
---

# Apache Kafka Credentials File Reference

The `apache-kafka-credentials.yaml` file tells kcp how to connect to each Apache Kafka® cluster you migrate from, whether self-managed Apache Kafka, Confluent Platform, or another distribution compatible with the Kafka API. This page covers examples for common setups and every field the file accepts.

Apache Kafka sources have no automated discovery step, so you write this file yourself. [`kcp scan clusters --source-type apache-kafka`](../command-reference/scan/clusters.md) reads it, as do the `kcp iac` commands that target an Apache Kafka source.

For MSK, you don't write this file. [`kcp discover`](../command-reference/discover.md) generates the MSK equivalent, `msk-credentials.yaml`, for you, because MSK metadata comes from the AWS APIs.

## Minimal example

This example connects to one cluster with SASL/SCRAM-SHA-256:

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

## SASL over TLS with a private CA

This example connects with SASL/SCRAM-SHA-512 and trusts a private certificate authority (CA):

```yaml
clusters:
  - id: private-ca-kafka
    bootstrap_servers:
      - broker1.example.com:9093
    auth_method:
      sasl_scram:
        use: true
        username: admin
        password: changeme
        mechanism: SHA512
        ca_cert: /path/to/ca.pem
```

## Full example with several clusters

This example configures three clusters with different authentication methods, and collects Jolokia metrics from the production cluster:

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

### Top-level fields

| Field      | Type | Required | Description                                       |
| ---------- | ---- | -------- | ------------------------------------------------- |
| `clusters` | list | yes      | One or more cluster entries. kcp scans each one.  |

### Cluster fields

| Field                      | Type                 | Required | Description |
| -------------------------- | -------------------- | -------- | ----------- |
| `id`                       | string               | yes      | Unique identifier for the cluster. kcp uses it as the cluster key in `kcp-state.json` and as the `--cluster-id` value for later `kcp iac` commands. |
| `bootstrap_servers`        | list of strings      | yes      | Broker `host:port` addresses. |
| `auth_method`              | object               | yes      | Authentication method. Choose exactly one sub-block, as described in [`auth_method`](#auth_method). |
| `insecure_skip_tls_verify` | bool                 | no       | Disables server certificate verification for this cluster's Kafka connection. It doesn't affect `jolokia` or `prometheus`, which have their own `tls.insecure_skip_verify`. With `sasl_plain`, it applies only when TLS is on (`ca_cert` or `tls: true`). |
| `jolokia`                  | object               | no       | Jolokia HTTP endpoints for live JMX metrics. Required if you pass `--metrics jolokia` to `kcp scan clusters` or `kcp scan self-managed-connectors`. For Connect metrics, point the endpoints at Connect workers instead of brokers. |
| `prometheus`               | object               | no       | Prometheus HTTP API for historical metrics. Required if you pass `--metrics prometheus` to `kcp scan clusters` or `kcp scan self-managed-connectors`. |
| `metadata`                 | `map<string,string>` | no       | Free-form labels shown in reports and the UI, such as `environment` and `location`. |

### `auth_method`

Choose one of these sub-blocks:

| Sub-block                   | Use case                               | Required fields |
| --------------------------- | -------------------------------------- | --------------- |
| `sasl_scram`                | SASL/SCRAM-SHA-256 or SHA-512 over TLS | `use: true`, `username`, `password`, and `mechanism` (`SHA256` or `SHA512`). Optionally `ca_cert`. |
| `sasl_plain`                | SASL/PLAIN, in plaintext or over TLS   | `use: true`, `username`, and `password`. Optionally `ca_cert` or `tls: true`. |
| `tls`                       | TLS or mTLS with client certificates   | `use: true`, `ca_cert`, `client_cert`, and `client_key` |
| `unauthenticated_tls`       | TLS with no client authentication      | `use: true`. Optionally `ca_cert`. |
| `unauthenticated_plaintext` | No authentication (test environments only) | `use: true` |

**TLS for SASL blocks.** `sasl_scram` always connects over TLS. `ca_cert` names the CA to trust, and without it, kcp uses the system trust store. Set `ca_cert` when the brokers present a certificate from a private CA. `sasl_plain` connects in plaintext unless you select TLS: set `ca_cert` for a private CA, or `tls: true` for a certificate signed by a public CA, which kcp checks against the system trust store.

**SCRAM mechanism.** Apache Kafka supports both `SHA256` and `SHA512`. `SHA256` is the more common default, but kcp doesn't infer the mechanism, so set `mechanism` explicitly. MSK supports only `SHA512`, and `kcp discover` sets it automatically when it generates `msk-credentials.yaml`.

### `jolokia`

The optional `jolokia` block configures live metrics collection from Jolokia endpoints:

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
  connect_mbean_overrides:      # optional — for Connect worker scans; see connect-metrics-collection.md
    task-count: "acme.connect:type=connect-worker-metrics"
```

| Field                        | Required | Description |
| ---------------------------- | -------- | ----------- |
| `endpoints`                  | yes      | List of Jolokia HTTP endpoints, one for each broker. |
| `auth.username` / `password` | no       | HTTP basic authentication credentials. |
| `tls.ca_cert`                | no       | CA certificate for HTTPS Jolokia endpoints. |
| `tls.insecure_skip_verify`   | no       | Skips TLS verification (test environments only). |
| `mbean_overrides`            | no       | Maps a logical metric label to an MBean object name, for agents that expose non-standard MBean names. See [Metric-name overrides](#metric-name-overrides). |
| `connect_mbean_overrides`    | no       | Maps a logical Connect metric label to an MBean object name, for `kcp scan self-managed-connectors` scans of Kafka Connect workers. See [Connect metric-name overrides](connect-metrics-collection.md#metric-name-overrides). |

### `prometheus`

The optional `prometheus` block configures historical metrics collection from a Prometheus server:

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
  timeout: 60s                  # optional — HTTP client timeout per query (omit for 30s default; minimum 30s)
  metric_names:                 # optional — see "Metric-name overrides" below
    BytesInPerSec: acme_broker_bytesin_total
    MessagesInPerSec: acme_broker_messagesin_total
  connect_metric_names:         # optional — for Connect worker scans; see connect-metrics-collection.md
    task-count: acme_connect_task_count
```

| Field                        | Required | Description |
| ---------------------------- | -------- | ----------- |
| `url`                        | yes      | Prometheus server URL. |
| `auth.username` / `password` | no       | HTTP basic authentication credentials. |
| `tls.ca_cert`                | no       | CA certificate for HTTPS Prometheus endpoints. |
| `tls.insecure_skip_verify`   | no       | Skips TLS verification (test environments only). |
| `filter.labels`              | no       | Maps Prometheus label selectors that scope queries. When set, every PromQL query includes them as `{key="value"}` filters. Use it when one Prometheus server scrapes several clusters. |
| `timeout`                    | no       | HTTP client timeout for each Prometheus query, as a Go duration such as `60s` or `2m`. If you omit it, the default is `30s`. An explicit value must be at least `30s`, and kcp rejects shorter values. Raise it for large `--metrics-range` queries against high-cardinality clusters that respond slowly. |
| `metric_names`               | no       | Maps a logical metric label to a base Prometheus series name, for exporters that relabel the standard series. See [Metric-name overrides](#metric-name-overrides). |
| `connect_metric_names`       | no       | Maps a logical Connect metric label to a base Prometheus series name, for `kcp scan self-managed-connectors` scans of Kafka Connect workers. See [Connect metric-name overrides](connect-metrics-collection.md#metric-name-overrides). |

Each scan reads either `jolokia` or `prometheus`, not both, and `--metrics` selects which one. You can keep both blocks in the file and switch between them by changing the flag.

### Metric-name overrides

If your Prometheus exporter relabels the standard broker series, or your Jolokia agent exposes broker MBeans under non-standard object names, the built-in queries return empty results. Instead of patching those names in source, repoint them for each cluster:

- `prometheus.metric_names` maps a logical label to the base series name your exporter exposes.
- `jolokia.mbean_overrides` maps a logical label to the MBean object name your agent exposes.

Both use the same seven logical labels as keys:

`BytesInPerSec`, `BytesOutPerSec`, `MessagesInPerSec`, `PartitionCount`, `GlobalPartitionCount`, `ClientConnectionCount`, `TotalLocalStorageUsage`.

For example, if your Prometheus JMX Exporter publishes the byte-rate series under an `acme_` prefix and drops the `_total` suffix, map only the labels that changed. Every label you omit keeps its default:

```yaml
prometheus:
  url: http://prometheus.example.com:9090
  metric_names:
    BytesInPerSec: acme_kafka_bytesin     # default: kafka_server_brokertopicmetrics_bytesinpersec_total
    BytesOutPerSec: acme_kafka_bytesout   # default: kafka_server_brokertopicmetrics_bytesoutpersec_total
```

kcp puts the name into its own query wrapping, so it now runs `sum(rate(acme_kafka_bytesin[<window>]))` instead of the default. The Jolokia equivalent maps the same logical label to a full MBean object name, including the domain, instead of a series name:

```yaml
jolokia:
  endpoints:
    - http://broker1.example.com:8778/jolokia
  mbean_overrides:
    BytesInPerSec: "acme.kafka:type=BrokerTopicMetrics,name=BytesInPerSec"   # default domain: kafka.server
```

- **List only the labels you need to change.** Unlisted labels keep their defaults. kcp treats a key with an empty value, such as `BytesInPerSec: ""`, as no override and ignores it without a warning, so set a real name or omit the key.
- **Keys are validated when the file loads.** An unknown or misspelled label, including one with the wrong case, is an error that lists the valid labels. kcp validates both blocks whenever the file loads, whichever one `--metrics` selects, so a typo in the block you aren't scanning with still fails the scan.
- **Prometheus overrides replace only the base series name.** kcp keeps its own wrapping (`sum(rate(<name>[<window>]))`, `sum(<name>)`, and the GiB conversion) and its `filter.labels` injection, so an override renames the series instead of rewriting the query. `GlobalPartitionCount` is the exception. On the default series, `kafka_controller_kafkacontroller_value`, kcp appends a `{name="GlobalPartitionCount"}` discriminator, because several controller values share that series name. After you override `GlobalPartitionCount`, kcp drops the discriminator: your override already identifies the series, and most relabeled or flattened exporters expose it without a `name` label. If your override value carries its own selector, such as a job-scoped series, kcp uses it exactly as given.
- **An overridden metric that's still empty logs a warning.** If an overridden metric still returns no data, kcp logs it at WARN, while a missing default metric logs at DEBUG. You configured the override to fix an empty result, so a result that's still empty is worth surfacing.
- **An override renames a metric but doesn't reinterpret it.** The series or MBean you point at must have the same shape as the default: a byte counter for the `*PerSec` rates, a gauge for the counts, and raw bytes for storage. If you point at a rate gauge or a series in different units, you get a wrong value with no error, not an empty result. For details, see [What an override does not change](metrics-collection.md#what-an-override-does-not-change).

Kafka Connect worker metrics have their own pair of overrides, `prometheus.connect_metric_names` and `jolokia.connect_mbean_overrides`, keyed on the 10 Connect metric labels instead of the seven broker labels. For the label list, examples, and the per-connector caveats specific to Connect, see [Connect metric-name overrides](connect-metrics-collection.md#metric-name-overrides).

## Next steps

- [`kcp scan clusters`](../command-reference/scan/clusters.md): pass this file with `--credentials-file`.

## Related content

- [Collect broker metrics from Apache Kafka sources](metrics-collection.md)
- [Collect Kafka Connect worker metrics](connect-metrics-collection.md)
- [Get started with kcp](../getting-started.md)
- [Run kcp in Docker](../running-kcp-in-docker.md)
