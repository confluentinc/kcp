---
title: kcp migration execute
---

## kcp migration execute

Execute an initialized migration

### Synopsis

Execute an initialized migration through its remaining workflow steps.

This command resumes a migration from its current state, progressing through:
lag checking, gateway fencing, topic promotion, and gateway switchover.

The migration must first be created with 'kcp migration init'. If execution is
interrupted, re-running this command will resume from the last completed step.

```
kcp migration execute [flags]
```

### Examples

```
  # MSK source with IAM auth
  kcp migration execute \
      --migration-id migration-a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
      --lag-threshold 0 \
      --cluster-api-key ABCDEFGHIJKLMNOP \
      --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
      --use-sasl-iam --aws-region us-east-1

  # OSK source with TLS
  kcp migration execute \
      --migration-id migration-a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
      --lag-threshold 0 \
      --cluster-api-key ABCDEFGHIJKLMNOP \
      --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
      --use-tls --tls-ca-cert ca.pem --tls-client-cert client.pem --tls-client-key client.key

Credentials (cluster-api-key, cluster-api-secret) are intentionally not stored in the migration state file and must be provided each time.
```

### Options

```
      --aws-region string               AWS region of the source MSK cluster (e.g. us-east-1).
      --cluster-api-key string          API key for authenticating with the destination cluster.
      --cluster-api-secret string       API secret for authenticating with the destination cluster.
  -h, --help                            help for execute
      --insecure-skip-tls-verify        Skip TLS certificate verification for REST endpoint and Kafka connections.
      --lag-threshold int               Total topic replication lag threshold (sum of all partition lags) before proceeding with migration.
      --migration-id string             ID of the migration to execute (from 'kcp migration list').
      --migration-state-file string     Path to the migration state file. (default "migration-state.json")
      --sasl-plain-password string      SASL/PLAIN password for the source cluster.
      --sasl-plain-username string      SASL/PLAIN username for the source cluster.
      --sasl-scram-password string      SASL/SCRAM password for the source MSK cluster.
      --sasl-scram-username string      SASL/SCRAM username for the source MSK cluster.
      --tls-ca-cert string              Path to the TLS CA certificate for the source MSK cluster.
      --tls-client-cert string          Path to the TLS client certificate for the source MSK cluster.
      --tls-client-key string           Path to the TLS client key for the source MSK cluster.
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

* [kcp migration](index.md)	 - Commands for migrating using CPC Gateway.

