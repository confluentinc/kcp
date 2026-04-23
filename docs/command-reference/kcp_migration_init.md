---
title: kcp migration init
---

## kcp migration init

Initialize a new migration

### Synopsis

Initialize a new migration by validating infrastructure and persisting migration state.

This command validates the cluster link and mirror topics on the destination cluster,
fetches the current gateway CR from Kubernetes, validates consistency across the initial,
fenced, and switchover gateway CRs, and writes the migration configuration to the state file.

The state file can then be used by 'kcp migration execute' to run the migration.

```
kcp migration init [flags]
```

### Examples

```
  # MSK source with IAM auth
  kcp migration init \
      --k8s-namespace my-namespace \
      --initial-cr-name my-gateway \
      --source-bootstrap b1.my-cluster.kafka.us-east-1.amazonaws.com:9098 \
      --cluster-bootstrap pkc-abc123.us-east-1.aws.confluent.cloud:9092 \
      --cluster-id lkc-abc123 \
      --cluster-rest-endpoint https://lkc-abc123.us-east-1.aws.confluent.cloud:443 \
      --cluster-link-name my-cluster-link \
      --cluster-api-key ABCDEFGHIJKLMNOP \
      --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
      --fenced-cr-yaml gateway-fenced.yaml \
      --switchover-cr-yaml gateway-switchover.yaml \
      --use-sasl-iam

  # SASL/SCRAM source
  kcp migration init \
      --k8s-namespace my-namespace --initial-cr-name my-gateway \
      --source-bootstrap broker1:9096 --cluster-bootstrap pkc-abc123.us-east-1.aws.confluent.cloud:9092 \
      --cluster-id lkc-abc123 --cluster-rest-endpoint https://lkc-abc123.us-east-1.aws.confluent.cloud:443 \
      --cluster-link-name my-cluster-link \
      --cluster-api-key ABCDEFGHIJKLMNOP --cluster-api-secret xxxx \
      --fenced-cr-yaml gateway-fenced.yaml --switchover-cr-yaml gateway-switchover.yaml \
      --use-sasl-scram --sasl-scram-username kafkauser --sasl-scram-password kafkapass

All flags can be provided via environment variables (uppercase, with underscores).
```

### Options

```
      --cluster-api-key string          API key for authenticating with the destination cluster.
      --cluster-api-secret string       API secret for authenticating with the destination cluster.
      --cluster-bootstrap string        Confluent Cloud Kafka bootstrap endpoint (e.g. pkc-abc123.us-east-1.aws.confluent.cloud:9092).
      --cluster-id string               Confluent Cloud destination cluster ID (e.g. lkc-abc123).
      --cluster-link-name string        Name of the cluster link on the destination cluster.
      --cluster-rest-endpoint string    REST endpoint of the destination Confluent Cloud cluster.
      --fenced-cr-yaml string           Path to the gateway CR YAML that blocks traffic during migration.
  -h, --help                            help for init
      --initial-cr-name string          Name of the initial gateway custom resource in Kubernetes.
      --insecure-skip-tls-verify        Skip TLS certificate verification for REST endpoint and Kafka connections.
      --k8s-namespace string            Kubernetes namespace where the gateway is deployed.
      --kube-path string                The path to the Kubernetes config file to use for the migration.
      --migration-state-file string     The path to the migration state file. If it doesn't exist, it will be created. If it exists, the new migration will be appended. (default "migration-state.json")
      --sasl-plain-password string      SASL/PLAIN password for the source cluster.
      --sasl-plain-username string      SASL/PLAIN username for the source cluster.
      --sasl-scram-password string      SASL/SCRAM password for the source MSK cluster.
      --sasl-scram-username string      SASL/SCRAM username for the source MSK cluster.
      --skip-validate                   Skip infrastructure validation. Creates migration metadata without validating gateway/Kubernetes resources. Useful for testing.
      --source-bootstrap string         Bootstrap server(s) of the source Kafka cluster (e.g. broker1:9092,broker2:9092).
      --switchover-cr-yaml string       Path to the gateway CR YAML that routes traffic to Confluent Cloud.
      --tls-ca-cert string              Path to the TLS CA certificate for the source MSK cluster.
      --tls-client-cert string          Path to the TLS client certificate for the source MSK cluster.
      --tls-client-key string           Path to the TLS client key for the source MSK cluster.
      --topics strings                  The topics to migrate (comma separated list or repeated flag).
      --use-sasl-iam                    Use IAM authentication for the source MSK cluster.
      --use-sasl-plain                  Use SASL/PLAIN authentication for the source cluster.
      --use-sasl-scram                  Use SASL/SCRAM authentication for the source MSK cluster.
      --use-tls                         Use TLS authentication for the source MSK cluster.
      --use-unauthenticated-plaintext   Use unauthenticated (plaintext) for the source MSK cluster.
      --use-unauthenticated-tls         Use unauthenticated (TLS encryption) for the source MSK cluster.
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp migration](kcp_migration.md)	 - Commands for migrating using CPC Gateway.

