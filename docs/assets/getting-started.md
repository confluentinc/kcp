---
title: Get Started With kcp
---

# Get Started With kcp

This runbook covers the first thing you do with kcp: scan your Kafka clusters, whether Amazon MSK or Apache Kafka®, and review the result. It takes you from an installed kcp to your clusters, topics, access control lists (ACLs), consumer groups, connectors, and schemas in `kcp ui`.

Discovery (MSK only) and `kcp scan clusters` are required. The other scans in step 3 are optional, so run the ones that apply to your environment and skip the rest.

Where a step differs by source, it has an MSK tab and an Apache Kafka tab. Follow the tab that matches your source. kcp scans Confluent Platform clusters as Apache Kafka.

kcp writes everything to a single state file, `kcp-state.json`, which later commands read and extend. You can safely re-run every command: kcp merges new results into the state file instead of duplicating them.

| Step | MSK | Apache Kafka and Confluent Platform |
| ---- | --- | ----------------------------------- |
| [1. Discover](#1-discover-msk-clusters) | `kcp discover` | Not applicable |
| [2. Set up credentials](#2-set-up-credentials) | Edit the generated `msk-credentials.yaml` | Write `apache-kafka-credentials.yaml` |
| [3. Scan](#3-run-the-scans) | | |
| &nbsp;&nbsp;[3.1 Clusters](#31-scan-clusters) | `kcp scan clusters` | `kcp scan clusters`, optionally with metrics |
| &nbsp;&nbsp;[3.2 Client activity (optional)](#32-scan-client-activity-optional) | `kcp scan client-inventory` | Not applicable |
| &nbsp;&nbsp;[3.3 Schema registry (optional)](#33-scan-a-schema-registry-optional) | `kcp scan schema-registry` | `kcp scan schema-registry` |
| &nbsp;&nbsp;[3.4 Connectors (optional)](#34-scan-connectors-optional) | `kcp scan msk-connectors`, `kcp scan self-managed-connectors` | `kcp scan self-managed-connectors` |
| [4. Review](#4-review-the-result) | `kcp ui` | `kcp ui` |

## Before you start

=== "MSK"

    kcp needs AWS credentials for discovery and for the scans that call AWS. To set them up, see [Authenticate to AWS](README.md#authenticate-to-aws). Your machine must also reach the brokers, because `kcp scan clusters` connects through the Kafka Admin API. To see which IAM permissions each command needs, see its page in the [command reference](command-reference/index.md).

=== "Apache Kafka"

    Your machine must reach the brokers and, for the optional scans, the Schema Registry and Kafka Connect REST endpoints. You don't need AWS credentials.

To run these commands from a container instead of installing the binary, see [Run kcp in Docker](running-kcp-in-docker.md).

Where a command needs a password, this page writes it as an environment variable, such as `$sr_password` or `$connect_password`. Export the variable first, or substitute the value in the command.

## 1. Discover MSK clusters

=== "MSK"

    [`kcp discover`](command-reference/discover.md) finds your MSK clusters and records their configuration, costs, and CloudWatch metrics.

    ```shell
    kcp discover --region us-east-1 --metrics-granularity 5m
    ```

    - `--region` takes one or more regions. Repeat the flag, or separate the regions with commas. To discover specific clusters instead, pass `--cluster-arn`.
    - `--metrics-granularity` sets the CloudWatch resolution: `60s`, `5m`, `1h`, or `1d`. The default is `1d`. Finer granularity gives more detail on bursty workloads but makes the state file larger. For the maximum time range of each resolution, see [`kcp discover`](command-reference/discover.md).

    The command writes two files:

    - `kcp-state.json`: the clusters found and their discovered configuration.
    - `msk-credentials.yaml`: one entry for each cluster, which you complete in the next step.

=== "Apache Kafka"

    Apache Kafka has no discovery step. Go straight to step 2.

## 2. Set up credentials

=== "MSK"

    Open the generated `msk-credentials.yaml`. For each cluster, `kcp discover` lists the authentication methods the cluster supports and enables one of them. The file looks similar to this example:

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

    `kcp scan clusters` reads the file as follows:

    - **Each cluster with exactly one method enabled (`use: true`) is scanned.** If you enable a method on several clusters, kcp scans all of them in the same run.
    - **A cluster with no method enabled is skipped,** with a warning. To scan only some of your clusters, leave the others disabled.
    - **A cluster with more than one method enabled is an error.** Pick one method for each cluster.
    - **Fill in the details the method needs.** For a cluster that supports only SASL/SCRAM, kcp writes that block enabled, with an empty `username` and `password`.

    If you re-run `kcp discover`, it keeps the credentials you've already filled in for the clusters it finds again.

=== "Apache Kafka"

    Create `apache-kafka-credentials.yaml` to describe how kcp connects to each cluster. This example describes a cluster that uses SASL/PLAIN, with a Prometheus server for historical metrics:

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

    - `id` is the name kcp uses for the cluster in the state file. You pass the same value as `--cluster-id` in later steps.
    - kcp scans every entry under `clusters`, so you can list as many clusters as you need in one file.
    - `auth_method` takes exactly one block: `sasl_scram`, `sasl_plain`, `tls`, `unauthenticated_tls`, or `unauthenticated_plaintext`.
    - You need `prometheus` or `jolokia` only if you want metrics in step 3.1.

    For every field, the TLS options, and more examples, see [Apache Kafka credentials](apache-kafka-configuration/credentials.md).

## 3. Run the scans

`kcp scan` has one subcommand for each kind of resource, and each subcommand adds its results to the same state file. Start with `clusters`. The rest are optional.

### 3.1 Scan clusters

[`kcp scan clusters`](command-reference/scan/clusters.md) connects to each cluster through the Kafka Admin API and records its topics, ACLs, and consumer groups in `kcp-state.json`.

=== "MSK"

    ```shell
    kcp scan clusters --source-type msk \
        --state-file kcp-state.json \
        --credentials-file msk-credentials.yaml
    ```

    If kcp can't scan a cluster, it warns, skips that cluster, and still scans the rest.

=== "Apache Kafka"

    To scan topics, ACLs, and consumer groups only:

    ```shell
    kcp scan clusters --source-type apache-kafka \
        --state-file kcp-state.json \
        --credentials-file apache-kafka-credentials.yaml
    ```

    To also collect broker metrics, add one of the following options. Both need the matching block in the credentials file, and both feed the same reports.

    For historical metrics from Prometheus, which needs a `prometheus` block:

    ```shell
    kcp scan clusters --source-type apache-kafka \
        --state-file kcp-state.json \
        --credentials-file apache-kafka-credentials.yaml \
        --metrics prometheus --metrics-range 7d
    ```

    For live metrics polled from each broker's Jolokia endpoint, which needs a `jolokia` block:

    ```shell
    kcp scan clusters --source-type apache-kafka \
        --state-file kcp-state.json \
        --credentials-file apache-kafka-credentials.yaml \
        --metrics jolokia --metrics-duration 5m --metrics-interval 10s
    ```

    For the metrics kcp records and how it calculates rates, see [Apache Kafka metrics collection](apache-kafka-configuration/metrics-collection.md).

To leave out topics, ACLs, or consumer groups, pass `--skip-topics`, `--skip-acls`, or `--skip-consumer-groups`. Reading a consumer group's type needs brokers running Kafka 3.8 and later. For what kcp records for each group type, see [`kcp scan clusters`](command-reference/scan/clusters.md).

### 3.2 Scan client activity (optional)

=== "MSK"

    [`kcp scan client-inventory`](command-reference/scan/client-inventory.md) reads the broker logs in Amazon S3 to work out which clients (producers and consumers) use each cluster. This tells you what has to move at switchover.

    The scan needs the following on the source cluster:

    - Trace logging on every broker: `kafka.server.KafkaApis=TRACE`.
    - Broker logs delivered to S3.

    Point `--s3-uri` at the folder of logs for the period you want to analyze. Logs are delivered into one folder for each hour.

    ```shell
    kcp scan client-inventory \
        --state-file kcp-state.json \
        --s3-uri s3://<bucket>/AWSLogs/<account_id>/KafkaBrokerLogs/<region>/<cluster_name>-<cluster_id>/<yyyy-mm-dd-hh>/
    ```

    kcp records each client once, keyed by client ID, topic, role, authentication, and principal. If you run the scan again on another time window, kcp adds only the new clients.

=== "Apache Kafka"

    This scan isn't available for Apache Kafka, because it reads MSK broker logs from S3.

### 3.3 Scan a schema registry (optional)

[`kcp scan schema-registry`](command-reference/scan/schema-registry.md) records every schema and version in the registry. `--sr-type confluent` works with any registry that exposes a Confluent-compatible REST API, including Apicurio through its `/apis/ccompat/v7` endpoint.

For Confluent Schema Registry with basic authentication:

```shell
kcp scan schema-registry --sr-type confluent \
    --state-file kcp-state.json \
    --url https://schema-registry.example.com:8081 \
    --use-basic-auth --username sr-admin --password "$sr_password"
```

For an open registry, use `--use-unauthenticated` instead of the basic authentication flags. For mutual TLS, use `--use-mtls` with `--tls-client-cert` and `--tls-client-key`. If the registry serves HTTPS with a certificate from a private certificate authority (CA), add `--tls-ca-cert`.

For AWS Glue Schema Registry, which uses your AWS credentials:

```shell
kcp scan schema-registry --sr-type glue \
    --state-file kcp-state.json \
    --region us-east-1 --registry-name my-glue-registry
```

Run the scan once for each registry.

### 3.4 Scan connectors (optional)

=== "MSK"

    kcp matches MSK Connect connectors to the MSK clusters already in your state file:

    ```shell
    kcp scan msk-connectors --state-file kcp-state.json --region us-east-1
    ```

    To limit the scan to one cluster, use `--cluster-arn` instead of `--region`. To also collect the connectors' CloudWatch metrics, add `--metrics-granularity` with `60s`, `5m`, `1h`, or `1d`.

    kcp scans self-managed Kafka Connect workers through their REST API. Set `--cluster-id` to the ARN of the MSK cluster the connectors belong to:

    ```shell
    kcp scan self-managed-connectors \
        --state-file kcp-state.json \
        --connect-rest-url http://connect.example.com:8083 \
        --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/orders-prod/abc-123 \
        --use-basic-auth --username connect-admin --password "$connect_password"
    ```

=== "Apache Kafka"

    kcp scans Kafka Connect workers through their REST API. Set `--cluster-id` to the `id` of the cluster in your credentials file:

    ```shell
    kcp scan self-managed-connectors \
        --state-file kcp-state.json \
        --connect-rest-url http://connect.example.com:8083 \
        --cluster-id prod-kafka \
        --source-type osk \
        --use-basic-auth --username connect-admin --password "$connect_password"
    ```

For an open Connect REST API, use `--use-unauthenticated`. For mutual TLS, use `--use-tls` with `--tls-client-cert` and `--tls-client-key`. kcp redacts sensitive connector configuration values before it writes them to the state file. To also collect Connect worker metrics, see [Connect metrics collection](apache-kafka-configuration/connect-metrics-collection.md).

## 4. Review the result

Open the state file in the UI:

```shell
kcp ui --state-file kcp-state.json
```

The UI runs locally at `http://localhost:5556`. For its options, see [`kcp ui`](command-reference/ui.md). Open each cluster and check that what you expect is there: its topics, ACLs, and consumer groups, plus the metrics, clients, schemas, and connectors from any optional scans you ran. If something is missing, re-run the relevant scan with `--verbose` to see the details.

## Next steps

- [Migration assets](migration-assets/README.md): create the cluster link, topics, and ACLs on the destination with `kcp migration resources`.
- [Run a migration cutover](migration-assets/cutover/running-a-cutover.md): cut clients over to Confluent Cloud through Confluent Gateway with `kcp migration cutover execute`.

## Related content

- [Migrate to Confluent Cloud with kcp](README.md)
- [Source compatibility for kcp commands](source-compatibility.md)
- [Run kcp in Docker](running-kcp-in-docker.md)
- [Apache Kafka credentials](apache-kafka-configuration/credentials.md)
- [`kcp report` command reference](command-reference/report/index.md)
- [`kcp iac` command reference](command-reference/iac/index.md)
