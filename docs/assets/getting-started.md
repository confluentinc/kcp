---
title: Getting started
---

# Getting started

This page is a runbook for the first thing you do with `kcp`: scan your Kafka clusters and look at the result. It takes you from an installed `kcp` to your clusters, topics, ACLs, consumer groups, connectors and schemas in `kcp ui`. For what comes after, see [Next steps](#next-steps).

Discovery (MSK) and `kcp scan clusters` are the required steps. The other scans in step 3 are optional: run the ones that apply to your estate and skip the rest.

Every step has an **MSK** tab and an **Apache Kafka** tab; follow the one that matches your source. Confluent Platform clusters are scanned as **Apache Kafka**.

Everything is written to a single state file, `kcp-state.json`, which later commands read and extend. Every command is safe to re-run: new results are merged into the state file, not duplicated.

| Step | MSK | Apache Kafka / Confluent Platform |
| ---- | --- | --------------------------------- |
| [1. Discover](#1-discover-msk-only) | `kcp discover` | not applicable |
| [2. Set up credentials](#2-set-up-credentials) | edit the generated `msk-credentials.yaml` | write `apache-kafka-credentials.yaml` |
| [3. Scan](#3-scan) | | |
| &nbsp;&nbsp;[3.1 Clusters](#31-clusters) | `kcp scan clusters` | `kcp scan clusters` (optionally with metrics) |
| &nbsp;&nbsp;[3.2 Client activity (optional)](#32-optional-client-activity-msk-only) | `kcp scan client-inventory` | not applicable |
| &nbsp;&nbsp;[3.3 Schema registry (optional)](#33-optional-schema-registry) | `kcp scan schema-registry` | `kcp scan schema-registry` |
| &nbsp;&nbsp;[3.4 Connectors (optional)](#34-optional-connectors) | `kcp scan msk-connectors`, `kcp scan self-managed-connectors` | `kcp scan self-managed-connectors` |
| [4. Review](#4-review-the-result) | `kcp ui` | `kcp ui` |

## Before you start

=== "MSK"

    `kcp` needs AWS credentials for discovery and for the AWS-side scans; see [Authentication](README.md#authentication). Your machine must also be able to reach the brokers, because `kcp scan clusters` connects with the Kafka Admin API. To see which IAM permissions each command needs, see its page in the [command reference](command-reference/index.md).

=== "Apache Kafka"

    Your machine must be able to reach the brokers (and, for the optional scans, the Schema Registry and Kafka Connect REST endpoints). No AWS credentials are needed.

Where a command needs a password, this page writes it as an environment variable (`$sr_password`, `$connect_password`). Export the variable first, or substitute the value directly in the command; either works.

## 1. Discover (MSK only)

=== "MSK"

    [`kcp discover`](command-reference/discover.md) finds your MSK clusters and records their configuration, costs and CloudWatch metrics.

    ```shell
    kcp discover --region us-east-1 --metrics-granularity 5m
    ```

    - `--region` takes one or more regions (repeat the flag or comma-separate them). To discover specific clusters instead, pass `--cluster-arn`.
    - `--metrics-granularity` sets the CloudWatch resolution: `60s`, `5m`, `1h` or `1d` (default `1d`). Finer granularity gives more detail on bursty workloads but grows the state file; see [`kcp discover`](command-reference/discover.md) for the maximum time range of each.

    This writes two files:

    - `kcp-state.json`: the clusters found and their discovered configuration.
    - `msk-credentials.yaml`: one entry per cluster, which you complete in the next step.

=== "Apache Kafka"

    There is no discovery step for Apache Kafka. Go straight to step 2.

## 2. Set up credentials

=== "MSK"

    Open the generated `msk-credentials.yaml`. For each cluster, `kcp discover` lists the authentication methods that cluster supports and enables one of them. The file looks like this (illustrative):

    ```yaml
    regions:
      - name: us-east-1
        clusters:
          - name: orders-prod
            arn: arn:aws:kafka:us-east-1:123456789012:cluster/orders-prod/abc-123
            auth_method:
              iam:
                use: false
              sasl_scram:
                use: true
                username: ""
                password: ""
    ```

    How `kcp scan clusters` reads this file:

    - **Each cluster with exactly one method enabled (`use: true`) is scanned.** Enable a method on several clusters and all of them are scanned in the same run.
    - **A cluster with no method enabled is skipped**, with a warning. To scan only some of your clusters, leave the others disabled.
    - **A cluster with more than one method enabled is an error.** Pick one per cluster.
    - Fill in the details the method needs. A cluster that only supports SASL/SCRAM is written with that block enabled but an empty `username` and `password`.

    Re-running `kcp discover` keeps the credentials you have already filled in for clusters it finds again.

=== "Apache Kafka"

    Create `apache-kafka-credentials.yaml` describing how `kcp` connects to each cluster. This example is a cluster using SASL/PLAIN with a Prometheus server for historical metrics:

    ```yaml
    clusters:
      - id: prod-kafka
        bootstrap_servers:
          - broker1.example.com:9092
          - broker2.example.com:9092
        auth_method:
          sasl_plain:
            use: true
            username: client
            password: <password>
        prometheus:
          url: http://prometheus.example.com:9090
        metadata:
          environment: production
          location: us-east-1
    ```

    - `id` is the name `kcp` uses for the cluster in the state file. You pass the same value as `--cluster-id` in later steps.
    - Every entry under `clusters` is scanned, so list as many clusters as you like in one file.
    - `auth_method` takes exactly one block: `sasl_scram`, `sasl_plain`, `tls`, `unauthenticated_tls` or `unauthenticated_plaintext`.
    - `prometheus` (or `jolokia`) is only needed if you want metrics in step 3.1.

    Every field, the TLS options and more worked examples are in [Apache Kafka credentials](apache-kafka-configuration/credentials.md).

## 3. Scan

`kcp scan` has one subcommand per kind of resource, and each adds its results to the same state file. Start with `clusters`; the rest are optional.

### 3.1 Clusters

[`kcp scan clusters`](command-reference/scan/clusters.md) connects to each cluster with the Kafka Admin API and records its topics, ACLs and consumer groups in `kcp-state.json`.

=== "MSK"

    ```shell
    kcp scan clusters --source-type msk \
        --state-file kcp-state.json \
        --credentials-file msk-credentials.yaml
    ```

    A cluster that could not be scanned is reported with a warning and skipped; the rest are still scanned.

=== "Apache Kafka"

    Topics, ACLs and consumer groups only:

    ```shell
    kcp scan clusters --source-type apache-kafka \
        --state-file kcp-state.json \
        --credentials-file apache-kafka-credentials.yaml
    ```

    To also collect broker metrics, add one of the following. Both need the matching block in the credentials file, and both feed the same reports.

    Historical metrics from Prometheus (needs a `prometheus` block):

    ```shell
    kcp scan clusters --source-type apache-kafka \
        --state-file kcp-state.json \
        --credentials-file apache-kafka-credentials.yaml \
        --metrics prometheus --metrics-range 7d
    ```

    Live metrics polled from each broker's Jolokia endpoint (needs a `jolokia` block):

    ```shell
    kcp scan clusters --source-type apache-kafka \
        --state-file kcp-state.json \
        --credentials-file apache-kafka-credentials.yaml \
        --metrics jolokia --metrics-duration 5m --metrics-interval 10s
    ```

    See [Metrics collection](apache-kafka-configuration/metrics-collection.md) for the metrics recorded and how rates are calculated.

Use `--skip-topics`, `--skip-acls` or `--skip-consumer-groups` to leave any of those out. Reading a consumer group's type needs brokers on Kafka 3.8 or newer; see the [command reference](command-reference/scan/clusters.md) for what is recorded for each group type.

### 3.2 Optional: client activity (MSK only)

=== "MSK"

    [`kcp scan client-inventory`](command-reference/scan/client-inventory.md) reads the broker logs in S3 to work out which clients (producers and consumers) are using each cluster. This tells you what has to move at switchover.

    It needs, on the source cluster:

    - Trace logging on every broker: `kafka.server.KafkaApis=TRACE`.
    - Broker logs delivered to S3.

    Point `--s3-uri` at the folder of logs for the period you want to analyse (logs are delivered into one folder per hour):

    ```shell
    kcp scan client-inventory \
        --state-file kcp-state.json \
        --s3-uri s3://<bucket>/AWSLogs/<account-id>/KafkaBrokerLogs/<region>/<cluster-name>-<cluster-id>/<yyyy-mm-dd-hh>/
    ```

    Each client is recorded once, keyed by client ID, topic, role, authentication and principal, so you can run this again on another time window and only new clients are added.

=== "Apache Kafka"

    Not available for Apache Kafka: the scan reads MSK broker logs from S3.

### 3.3 Optional: schema registry

[`kcp scan schema-registry`](command-reference/scan/schema-registry.md) records every schema and version in the registry. `--sr-type confluent` works with any registry that exposes a Confluent-compatible REST API, including Apicurio through its `/apis/ccompat/v7` endpoint.

Confluent Schema Registry with basic authentication:

```shell
kcp scan schema-registry --sr-type confluent \
    --state-file kcp-state.json \
    --url https://schema-registry.example.com:8081 \
    --use-basic-auth --username sr-admin --password "$sr_password"
```

Use `--use-unauthenticated` instead of the basic-auth flags for an open registry, or `--use-mtls` with `--tls-client-cert` and `--tls-client-key` for mutual TLS. If the registry is HTTPS behind a private CA, add `--tls-ca-cert`.

AWS Glue Schema Registry (uses your AWS credentials):

```shell
kcp scan schema-registry --sr-type glue \
    --state-file kcp-state.json \
    --region us-east-1 --registry-name my-glue-registry
```

Run it once per registry.

### 3.4 Optional: connectors

=== "MSK"

    MSK Connect connectors are matched to the MSK clusters already in your state file:

    ```shell
    kcp scan msk-connectors --state-file kcp-state.json --region us-east-1
    ```

    To limit the scan to one cluster, use `--cluster-arn` instead of `--region`. Add `--metrics-granularity` (`60s`, `5m`, `1h` or `1d`) to also collect the connectors' CloudWatch metrics.

    Self-managed Kafka Connect workers are scanned through their REST API. `--cluster-id` is the ARN of the MSK cluster the connectors belong to:

    ```shell
    kcp scan self-managed-connectors \
        --state-file kcp-state.json \
        --connect-rest-url http://connect.example.com:8083 \
        --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/orders-prod/abc-123 \
        --use-basic-auth --username connect-admin --password "$connect_password"
    ```

=== "Apache Kafka"

    Kafka Connect workers are scanned through their REST API. `--cluster-id` is the `id` of the cluster in your credentials file:

    ```shell
    kcp scan self-managed-connectors \
        --state-file kcp-state.json \
        --connect-rest-url http://connect.example.com:8083 \
        --cluster-id prod-kafka \
        --source-type osk \
        --use-basic-auth --username connect-admin --password "$connect_password"
    ```

For an open Connect REST API use `--use-unauthenticated`; for mutual TLS use `--use-tls` with `--tls-client-cert` and `--tls-client-key`. Sensitive connector configuration values are redacted before they are written to the state file. To also collect Connect worker metrics, see [Connect metrics collection](apache-kafka-configuration/connect-metrics-collection.md).

## 4. Review the result

Open the state file in the UI:

```shell
kcp ui --state-file kcp-state.json
```

The UI runs locally on `http://localhost:5556`; see [`kcp ui`](command-reference/ui.md). Open each cluster and check that what you expect is there: its topics, ACLs and consumer groups, plus the metrics, clients, schemas and connectors from any optional scans you ran. If something is missing, re-run the relevant scan with `--verbose` to see the detail.

## Next steps

- [`kcp report`](command-reference/report/index.md): cost and metrics reports from the state file.
- [`kcp create-asset`](command-reference/create-asset/index.md): generate Terraform for the target, migration infrastructure, topics, schemas, ACLs and connectors.
- [Getting Started with Zero-Cut Migrations](getting-started-with-zero-cut-migrations.md): initialize and execute the client switchover.
