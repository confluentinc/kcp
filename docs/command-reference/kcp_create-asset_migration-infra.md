---
title: kcp create-asset migration-infra
---

## kcp create-asset migration-infra

Create migration infrastructure Terraform for a source cluster

### Synopsis

Generate the Terraform needed to provision the migration path between the source Kafka cluster and Confluent Cloud. The --type flag selects the migration topology and authentication method.

Type options:
  1  Public MSK endpoints — Cluster Link (SASL/SCRAM)
  2  Private MSK endpoints — External Outbound Cluster Link (SASL/SCRAM, Enterprise only)
  3  Private MSK endpoints — External Outbound Cluster Link (Unauthenticated Plaintext, Enterprise only)
  4  Private MSK endpoints — Jump Cluster (SASL/SCRAM)
  5  Private MSK endpoints — Jump Cluster (IAM, MSK only)

```
kcp create-asset migration-infra [flags]
```

### Examples

```
  # Type 4 — Jump Cluster with SASL/SCRAM, against a private MSK
  kcp create-asset migration-infra \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --type 4 \
      --existing-internet-gateway \
      --output-dir type-4 \
      --existing-private-link-vpce-id vpce-0abc123def456789 \
      --jump-cluster-broker-subnet-cidr 10.0.101.0/24,10.0.102.0/24,10.0.103.0/24 \
      --jump-cluster-setup-host-subnet-cidr 10.0.104.0/24 \
      --cluster-link-name type-4-link \
      --target-environment-id env-a1bcde \
      --target-cluster-id lkc-w89xyz \
      --target-rest-endpoint https://lkc-w89xyz.XXX.aws.private.confluent.cloud:443 \
      --target-bootstrap-endpoint lkc-w89xyz.XXX.aws.private.confluent.cloud:9092

  # Type 1 — Public MSK, simple cluster link
  kcp create-asset migration-infra \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --type 1 \
      --cluster-link-name simple-link \
      --target-cluster-id lkc-w89xyz \
      --target-rest-endpoint https://lkc-w89xyz.us-east-1.aws.confluent.cloud:443
```

### Options

```
      --cluster-id string                            The cluster identifier (ARN for MSK, cluster ID from credentials file for OSK).
      --cluster-link-name string                     The name of the cluster link that will be created as part of the migration.
      --existing-internet-gateway                    Whether to use an existing internet gateway. (default: false)
      --existing-private-link-vpce-id string         The ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud.
  -h, --help                                         help for migration-infra
      --jump-cluster-broker-storage int              [Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).
      --jump-cluster-broker-subnet-cidr ipNetSlice   The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes. (default [])
      --jump-cluster-iam-auth-role-name string        The IAM role name to authenticate the cluster link between MSK and the jump cluster.
      --jump-cluster-instance-type string            [Optional] The instance type to use for the jump cluster. (default: MSK broker type).
      --jump-cluster-setup-host-subnet-cidr ipNet    The CIDR block to use for the jump cluster setup host subnet.
      --output-dir string                            The directory to output the migration infrastructure assets to. (default: 'migration-infra')
      --region string                                The AWS region where the OSK cluster's VPC resides. (required for OSK)
      --security-group-id string                     [Optional] Security group ID for the EC2 instance that provisions the cluster link. (default: MSK cluster security group).
      --source-sasl-scram-mechanism string           [Optional] The SASL/SCRAM mechanism for the source cluster (SCRAM-SHA-256 or SCRAM-SHA-512). Overrides the value from the state file.
      --source-type string                           Source type: 'msk' or 'osk' (required)
      --state-file string                            The path to the kcp state file where the cluster discovery reports have been written to.
      --subnet-id string                             [Optional] Subnet ID for the EC2 instance that provisions the cluster link. (default:  MSK broker #1 subnet).
      --target-bootstrap-endpoint string             The bootstrap endpoint to use for the Confluent Cloud cluster.
      --target-cluster-id string                     The Confluent Cloud cluster ID.
      --target-cluster-type string                   The Confluent Cloud target cluster type ('dedicated' or 'enterprise').
      --target-environment-id string                 The Confluent Cloud environment ID.
      --target-rest-endpoint string                  The Confluent Cloud cluster REST endpoint.
      --type string                                  The migration-infra type. See README for available options.
      --vpc-id string                                The VPC ID where the OSK cluster resides. (required for OSK)
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp create-asset](kcp_create-asset.md)	 - Generate infrastructure and migration assets

