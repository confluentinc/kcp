# KCP CLI

[![FOSSA Status](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp.svg?type=shield&issueType=license)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp?ref=badge_shield&issueType=license) [![FOSSA Status](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp.svg?type=shield&issueType=security)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp?ref=badge_shield&issueType=security)

This repository is part of the Confluent organization on GitHub.
It is public and open to contributions from the community.

Please see the LICENSE file for contribution terms.
Please see the CHANGELOG.md for details of recent updates.

---

<div align="center">

**A comprehensive command-line tool for planning and executing Kafka migrations to Confluent Cloud.**

</div>

---

## Table of Contents

- [Overview](#overview)
  - [Installation](#installation)
  - [Authentication](#authentication)
- [Getting Started](#getting-started)
  - [kcp Commands](#kcp-commands)
    - [`kcp init`](#kcp-init)
    - [`kcp discover`](#kcp-discover)
    - [`kcp scan`](#kcp-scan)
    - [`kcp report`](#kcp-report)
    - [`kcp create-asset`](#kcp-create-asset)
- [Development](#development)

## Overview

**Mission**: Simplify and streamline your Kafka migration journey to Confluent Cloud!

kcp helps you migrate your Kafka setups to Confluent Cloud by providing tools to:

- **Scan** scan and identify resources in existing Kafka deployments.
- **Create** reports for migration planning and cost analysis.
- **Generate** migration assets and infrastructure configurations.

### Key Features

| Feature                     | Description                                                                             |
| --------------------------- | --------------------------------------------------------------------------------------- |
| **Multiple Auth Methods**   | Support for SASL-IAM, SASL-SCRAM, TLS, and unauthenticated.                             |
| **Comprehensive Reporting** | Detailed migration planning and cost analysis.                                          |
| **Infrastructure as Code**  | Generate Terraform and Ansible configurations to seamlessly migrate to Confluent Cloud. |
| **Private VPC Deployments** | Migrate to Confluent Cloud from private networks and isolated environments.             |

## Installation

You can download kcp from GitHub under the [releases tab](https://github.com/confluentinc/kcp/releases/latest). We provide support for Linux and Darwin arm64/amd64 systems respectively.

Once downloaded, make sure to set the binary permissions to executable by running `chmod +x <binary name>`.

If you wish to run the downloaded kcp binary from anywhere on your system, you may run the following (requires sudo permissions):

```shell
# Update the binary suffix to your respective architecture.
sudo mv ./kcp_<ARCH> /usr/local/bin/kcp
```

## Authentication

Ensure that your terminal session is authenticated with AWS. The kcp CLI uses the standard AWS credential chain and supports multiple authentication methods:

**Authentication options:**

- **Environment variables**: Export `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and optionally `AWS_SESSION_TOKEN`
- **AWS credentials file**: Configure with `aws configure` (requires AWS CLI)
- **AWS SSO/Identity Center**: Use `aws sso login` (requires AWS CLI)
- **IAM Roles**: Assume roles or use instance profiles
- **Other tools**: Any tool that sets AWS credentials in the standard locations such as `granted`.

**Verify your authentication:**
The easiest way to test authentication is to run a kcp command that requires AWS access such as `kcp scan region`, or if you have AWS CLI installed:

```bash
aws sts get-caller-identity
```

# Getting Started

> [!NOTE]
> Currently, only migrations from AWS MSK are supported. Therefore, until later Apache Kafka migrations are supported, AWS MSK will be the reference point for the source of a migration.

## Workflow steps

The migration process follows these general steps:

1. **Initialize the environment**: Set up the CLI and configure your environment.
2. **Scan clusters**: Discover and analyze your Kafka deployment.
3. **Generate reports**: Produce reports on the cost and metrics of the MSK cluster.
4. **Generate migration assets**: Create the necessary infrastructure and scripts.
5. **Execute migration**: Perform the actual migration process.

## Make Key Infrastructure Decisions

Before starting the migration process, you need to make some key decisions about your infrastructure:

1. Is your MSK cluster accessible from the internet or within a private network?
2. If your MSK cluster is within a private network, do you require a bastion host or do you already have a way to access the cluster?
3. What authentication methods are enabled on the MSK cluster at the moment and what method will you use for establishing the cluster link.
   - Depending on the accessibility and authentication methods, only certain cluster link configurations may be possible.

### Bastion Host Requirements

**For MSK clusters with public endpoints:** You can run the CLI commands directly from your local machine without a bastion host server.

**For MSK clusters with private endpoints:** The CLI commands must be run from within the same VPC as the MSK cluster. In this case, you must use a a bastion host or jump server that resides in the same VPC as your existing MSK cluster.

**Important**: This ensures proper network connectivity for scanning and migration operations. When a bastion host is required, you can either:

1. **Create a new bastion host**: If you don't have a bastion host, you can create one using the `kcp create-asset bastion-host` command, [this step is outlined during the CLI deployment steps](#deploying-the-cli-to-a-bastion-host-only-required-for-msk-with-private-endpoints).

2. **Use an existing bastion host**: If you already have a bastion host, you need to deploy the CLI onto that server to scan your clusters.

> [!NOTE]
> If your MSK cluster is in a private network (not accessible from the internet), you'll need to transfer the kcp CLI to a bastion host within the same VPC before continuing.

## kcp Commands

### `kcp init`

Initializes an optional environment setup script requiring the configuration migration variables once instead of using CLI flags.

The `kcp init` command creates a `set_migration_env_vars.sh` shell script that can be configured to export environment variables for common CLI options used across kcp commands. Setting environment variables is optional but may be preferred especially when passing secrets to a kcp command.

To set the environment variables from the script, run `source set_migration_env_vars.sh`.

You can also set environment variables individually if you opt not to use the script. All environment variables map to their respective flags but in uppercase and with underscores replacing dashes. For example, `--vpc-id vpc-xxxxxxxxx` becomes `VPC_ID=vpc-xxxxxxxxx`, and `--cluster-arn arn:aws:...` becomes `CLUSTER_ARN=arn:aws:...`.

> [!NOTE]
> The environment setup script is completely optional if you wish to instead run each command with flags. Command flags will **always** take precedence over environment variables.
>
> However, they can be mixed and matched, for example if you are only planning to run commands within the region `us-east-1`, setting the environment variable `REGION` will avoid having to set the flag on all commands that need it.

---

### `kcp discover`

The kcp discover command performs a full discovery of all MSK clusters in an AWS account across multiple regions, together with their associated resources, costs and metrics.

**Example Usage**

`kcp discover --region us-east-1 --region eu-west-3`

or

`kcp discover --region us-east-1,eu-west-3`

The command will produce a creds.yaml, region msk, cost, metrics and cluster output files in a nested structure as follows:

```
.
└── kcp-scan
    ├── creds.yaml
    ├── eu-west-3
    │   ├── eu-west-3-cost-report.json
    │   ├── eu-west-3-cost-report.md
    │   ├── eu-west-3-region-scan.json
    │   ├── eu-west-3-region-scan.md
    │   └── cluster-1
    │       ├── cluster-1-metrics.json
    │       ├── cluster-1-metrics.md
    │       ├── cluster-1.json
    │       └── cluster-1.md
    └── us-east-1
        ├── cluster-2
        │   ├── cluster-2-metrics.json
        │   ├── cluster-2-metrics.md
        │   ├── cluster-2.json
        │   └── cluster-2.md
        ├── us-east-1-cost-report.json
        ├── us-east-1-cost-report.md
        ├── us-east-1-region-scan.json
        └── us-east-1-region-scan.md

```

This command requires the following permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ScanAllPermissions",
      "Effect": "Allow",
      "Action": [
        "kafka:ListClustersV2",
        "kafka:ListReplicators",
        "kafka:ListVpcConnections",
        "kafka:GetCompatibleKafkaVersions",
        "cloudwatch:GetMetricData",
        "kafka:ListKafkaVersions",
        "ce:GetCostAndUsage",
        "kafka:GetBootstrapBrokers",
        "kafka:ListConfigurations",
        "cloudwatch:GetMetricStatistics",
        "cloudwatch:ListMetrics",
        "kafka:DescribeClusterV2",
        "kafka:ListNodes",
        "kafka:ListClusterOperationsV2",
        "kafka:ListScramSecrets",
        "kafka:ListClientVpcConnections",
        "kafka:GetClusterPolicy",
        "kafka:DescribeConfigurationRevision",
        "kafka:DescribeReplicator"
      ],
      "Resource": "*"
    }
  ]
}
```

### `kcp scan`

The `kcp scan` commands perform various different scans on MSK clusters, from individual and multi-cluster scans, entire region scans and scanning Kafka broker logs to identify potential clients.

The `kcp scan` command includes the following sub-commands:

- `cluster`
- `clusters`
- `region`
- `client-inventory`

The sub-commands require the following minimum AWS IAM permissions:

`cluster`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MSKClusterManagementAccess",
      "Effect": "Allow",
      "Action": [
        "kafka:DescribeClusterV2",
        "kafka:GetClusterPolicy",
        "kafka:ListClientVpcConnections",
        "kafka:ListClusterOperationsV2",
        "kafka:ListNodes",
        "kafka:ListScramSecrets"
      ],
      "Resource": "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>"
    },
    {
      "Sid": "MSKClusterKafkaAccess",
      "Effect": "Allow",
      "Action": [
        "kafka-cluster:Connect",
        "kafka-cluster:DescribeCluster",
        "kafka-cluster:DescribeClusterDynamicConfiguration",
        "kafka-cluster:DescribeTopic"
      ],
      "Resource": [
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/*",
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>"
      ]
    },
    {
      "Sid": "RegionLevelMSKAccess",
      "Effect": "Allow",
      "Action": [
        "kafka:GetBootstrapBrokers",
        "kafka:GetCompatibleKafkaVersions",
        "kafka:ListKafkaVersions",
        "kafka:ListClustersV2",
        "kafka:ListConfigurations",
        "kafka:ListReplicators",
        "kafka:ListVpcConnections"
      ],
      "Resource": ["arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:*"]
    },
    {
      "Sid": "MSKClusterNetworkingAccess",
      "Effect": "Allow",
      "Action": ["ec2:DescribeSubnets"],
      "Resource": ["*"]
    }
  ]
}
```

`clusters`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MSKClusterKafkaAccess",
      "Effect": "Allow",
      "Action": [
        "kafka-cluster:Connect",
        "kafka-cluster:DescribeCluster",
        "kafka-cluster:DescribeClusterDynamicConfiguration",
        "kafka-cluster:DescribeTopic"
      ],
      "Resource": [
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/*",
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>"
      ]
    }
  ]
}
```

`region`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "RegionLevelMSKAccess",
      "Effect": "Allow",
      "Action": [
        "kafka:DescribeConfiguration",
        "kafka:DescribeConfigurationRevision",
        "kafka:GetBootstrapBrokers",
        "kafka:GetCompatibleKafkaVersions",
        "kafka:ListClustersV2",
        "kafka:ListConfigurations",
        "kafka:ListKafkaVersions",
        "kafka:ListReplicators",
        "kafka:ListVpcConnections"
      ],
      "Resource": ["arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:*"]
    }
  ]
}
```

`client-inventory`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:ListBucket"],
      "Resource": [
        "arn:aws:s3:::<BROKER_LOGS_BUCKET>",
        "arn:aws:s3:::<BROKER_LOGS_BUCKET>/*"
      ]
    }
  ]
}
```

---

#### `kcp scan cluster`

Scan a specific MSK cluster for detailed information including both at the AWS and Kafka level.

**Example Usage**

```shell
kcp scan cluster \
  --cluster-arn arn:aws:kafka:us-east-1:XXX:cluster/XXX/1a2345b6-bf9f-4670-b13b-710985f5645d-5 \
  --use-sasl-scram \
  --sasl-scram-username username \
  --sasl-scram-password pa55word
```

**Output:**
The command generates two files - `cluster_scan_<cluster-name>.md` and `cluster_scan_<cluster-name>.json` file containing:

- Detailed cluster configuration
- Broker information
- Topic metadata
- Consumer group details
- Cluster metrics

---

#### `kcp scan clusters`

Scan multiple MSK clusters at the Kafka level using the generated assets of the `kcp discover` command to drive it.

**Example Usage**

```shell
kcp scan clusters \
  --discover-dir kcp-scan \
  --credentials-yaml kcp-scan/creds.yaml
```

**Output:**
The command appends the gathered list of ACLs, topics and the Kafka cluster ID to each cluster's respective scan JSON and markdown files.

---

#### `kcp scan region`

This command discovers all MSK clusters in a specified AWS region and generates a comprehensive report.

**Example Usage**

```shell
kcp scan region --region us-east-1
```

**Output:**
The command generates two files - `region_scan_<region>.md` and `region_scan_<region>.json` file containing:

- List of all MSK clusters in the region
- MSK cluster status & type
- Cluster authentication methods
- Public access configuration
- VPC connections
- MSK Kafka cluster configurations
- Available Kafka versions
- Replicators

Alternatively, the following environment variables need to be set:

```shell
export REGION=<aws-region>
```

---

#### `kcp scan client-inventory`

This command scans a hour window folder in s3 to identify as many clients as possible in the cluster.

**Prerequisites**

- Enable trace logging for `kafka.server.KafkaApis=TRACE` for each broker
- Enable s3 broker log delivery for the cluster

**Example Usage**

```shell
kcp scan client-inventory \
--region us-east-1 \
--s3-uri  s3://my-cluster-logs-bucket/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-1a2345b6-bf9f-4670-b13b-710985f5645d-5/2025-08-13-14/
```

**Output:**
The command generates a csv file - `broker_logs_scan_results.csv` containing:

- All the unique clients it could identify based on a combination of values:
  - i.e. clientID + topic + role + auth + principal

example output

```csv
Client ID,Role,Topic,Auth,Principal,Timestamp
consumer1,Consumer,test-topic-1,SASL_SCRAM,User:kafka-user-2,2025-08-18 10:15:16
default-producer-id,Producer,test-topic-1,SASL_SCRAM,User:kafka-user-2,2025-08-18 10:15:18
consumer2,Consumer,test-topic-1,UNAUTHENTICATED,User:ANONYMOUS,2025-08-18 10:18:22
default-producer-id,Producer,test-topic-1,UNAUTHENTICATED,User:ANONYMOUS,2025-08-18 10:18:24
```

Alternatively, the following environment variables need to be set:

```shell
export REGION=<aws-region>
export S3_URI=<folder-in-s3>
```

---

### `kcp report`

The `kcp report` command includes the following sub-commands:

- `region`
- `cluster`

The `kcp report region` command includes the following sub-command:

- `costs`

The sub-command requires the following minimum AWS IAM permissions:

`costs`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["ce:GetCostAndUsage"],
      "Resource": ["*"]
    }
  ]
}
```

The `kcp report cluster` command includes the following sub-command:

- `metrics`

The sub-command requires the following minimum AWS IAM permissions:

`metrics`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["cloudwatch:GetMetricStatistics", "cloudwatch:GetMetricData"],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": ["kafka:DescribeClusterV2", "kafka:GetBootstrapBrokers"],
      "Resource": [
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>"
      ]
    },
    {
      "Effect": "Allow",
      "Action": ["kafka:DescribeConfigurationRevision"],
      "Resource": [
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:configuration/<MSK CLUSTER CONFIG NAME>/<MSK CLUSTER CONFIG ID>"
      ]
    }
  ]
}
```

#### `kcp report region costs`

This command discovers all MSK clusters in a specified AWS region and generates a comprehensive report.

**Example Usage**

```shell
kcp report region costs \
--monthly \
--start 2025-07-01 \
--end 2025-08-01 \
--region us-east-1 \
--tag Environment=Staging \
--tag Owner=kcp-team
```

**Output:**
The command generates a `cost_report` directory, splitting reports by region which contain three files - `cost_report-<aws-region>.csv`, `cost_report-<aws-region>.md` and `cost_report-<aws-region>.json` file containing:

- Total cost of MSK based on the time granularity specified.
- Itemised cost of each usage type.

---

#### `kcp report cluster metrics`

This command collates important MSK Kafka metrics for a cluster and generates a comprehensive report using AWS CloudWatch.

**Example Usage**

```shell
kcp report cluster metrics \
--start 2025-07-01 \
--end 2025-08-01 \
--cluster-arn arn:aws:kafka:us-east-1:000123456789:cluster/msk-cluster/1a2345b6-bf9f-4670-b13b-710985f5645d-5 \
```

**Output:**
The command generates two files - `<aws-cluster>-metrics.md` and `<aws-cluster>-metrics.json` file containing:

- Broker details
- Metrics summary - average ingress/egress throughput, total partitions
- Easy-to-copy metrics values for a TCO calculator

---

### `kcp create-asset`

The `kcp create-asset` command includes the following sub-commands:

- `bastion-host`
- `migrate-acls`
- `migration-infra`
- `migration-scripts`
- `reverse-proxy`

The sub-commands require the following minimum AWS IAM permissions:

`bastion-host`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "EC2ReadOnlyAccess",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeImages",
        "ec2:DescribeAvailabilityZones",
        "ec2:DescribeKeyPairs",
        "ec2:DescribeInternetGateways",
        "ec2:DescribeSubnets",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribeRouteTables",
        "ec2:DescribeInstances",
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeTags",
        "ec2:DescribeVolumes",
        "ec2:DescribeInstanceCreditSpecifications"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MigrationKeyPairManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:ImportKeyPair",
        "ec2:DescribeKeyPairs",
        "ec2:DeleteKeyPair",
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:key-pair/migration-ssh-key"
    },
    {
      "Sid": "InternetGatewayManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateInternetGateway",
        "ec2:CreateTags",
        "ec2:AttachInternetGateway",
        "ec2:DeleteInternetGateway"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:internet-gateway/*"
    },
    {
      "Sid": "VPCResourceCreation",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSubnet",
        "ec2:CreateSecurityGroup",
        "ec2:AttachInternetGateway",
        "ec2:CreateRouteTable"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:vpc/*"
    },
    {
      "Sid": "SubnetManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSubnet",
        "ec2:CreateTags",
        "ec2:DeleteSubnet",
        "ec2:ModifySubnetAttribute",
        "ec2:AssociateRouteTable",
        "ec2:RunInstances",
        "ec2:DisassociateRouteTable"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:subnet/*"
    },
    {
      "Sid": "SecurityGroupManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSecurityGroup",
        "ec2:CreateTags",
        "ec2:DeleteSecurityGroup",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:AuthorizeSecurityGroupEgress",
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:security-group/*"
    },
    {
      "Sid": "RouteTableManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateRouteTable",
        "ec2:CreateTags",
        "ec2:DeleteRouteTable",
        "ec2:CreateRoute",
        "ec2:AssociateRouteTable",
        "ec2:DisassociateRouteTable"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:route-table/*"
    },
    {
      "Sid": "InstanceLifecycleManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances",
        "ec2:CreateTags",
        "ec2:DescribeInstanceAttribute",
        "ec2:TerminateInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:instance/*"
    },
    {
      "Sid": "InstanceLaunchNetworkInterface",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:network-interface/*"
    },
    {
      "Sid": "InstanceLaunchVolume",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:volume/*"
    },
    {
      "Sid": "InstanceLaunchAMI",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "arn:aws:ec2:<AWS REGION>::image/*"
    }
  ]
}
```

`migrate-acls`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ParseRolesForACLs",
      "Effect": "Allow",
      "Action": [
        "iam:GetRolePolicy",
        "iam:ListAttachedRolePolicies",
        "iam:ListRolePolicies"
      ],
      "Resource": ["*"]
    },
    {
      "Sid": "ParseUsersForACLs",
      "Effect": "Allow",
      "Action": [
        "iam:GetUserPolicy",
        "iam:ListAttachedUserPolicies",
        "iam:ListUserPolicies"
      ],
      "Resource": ["*"]
    }
  ]
}
```

`migration-infra`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "EC2ReadOnlyAccess",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeImages",
        "ec2:DescribeInternetGateways",
        "ec2:DescribeAvailabilityZones",
        "ec2:DescribeKeyPairs",
        "ec2:DescribeAddresses",
        "ec2:DescribeRouteTables",
        "ec2:DescribeVpcs",
        "ec2:DescribeAddressesAttribute",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribeSubnets",
        "ec2:DescribeNatGateways",
        "ec2:DescribeVpcEndpoints",
        "ec2:DescribePrefixLists",
        "ec2:DescribeInstances",
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeTags",
        "ec2:DescribeVolumes",
        "ec2:DescribeInstanceCreditSpecifications",
        "ec2:DescribeInstanceAttribute"
      ],
      "Resource": "*"
    },
    {
      "Sid": "Route53Management",
      "Effect": "Allow",
      "Action": [
        "route53:CreateHostedZone",
        "route53:GetChange",
        "route53:GetHostedZone",
        "route53:ListResourceRecordSets",
        "route53:ListTagsForResource",
        "route53:DeleteHostedZone",
        "route53:ChangeTagsForResource",
        "route53:ChangeResourceRecordSets",
        "ec2:CreateRoute",
        "ec2:DisassociateAddress"
      ],
      "Resource": "*"
    },
    {
      "Sid": "VPCResourceCreation",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSecurityGroup",
        "ec2:CreateRouteTable",
        "ec2:CreateSubnet",
        "ec2:CreateVpcEndpoint"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:vpc/*"
    },
    {
      "Sid": "SecurityGroupManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSecurityGroup",
        "ec2:CreateTags",
        "ec2:DeleteSecurityGroup",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:AuthorizeSecurityGroupEgress",
        "ec2:RunInstances",
        "ec2:CreateVpcEndpoint"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:security-group/*"
    },
    {
      "Sid": "SubnetManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSubnet",
        "ec2:CreateTags",
        "ec2:DeleteSubnet",
        "ec2:ModifySubnetAttribute",
        "ec2:AssociateRouteTable",
        "ec2:DisassociateRouteTable",
        "ec2:CreateNatGateway",
        "ec2:CreateVpcEndpoint",
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:subnet/*"
    },
    {
      "Sid": "RouteTableManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateRouteTable",
        "ec2:DeleteRouteTable",
        "ec2:CreateRoute",
        "ec2:AssociateRouteTable",
        "ec2:CreateTags",
        "ec2:DisassociateRouteTable"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:route-table/*"
    },
    {
      "Sid": "ElasticIPManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:AllocateAddress",
        "ec2:CreateTags",
        "ec2:DeleteTags",
        "ec2:ReleaseAddress",
        "ec2:DisassociateAddress",
        "ec2:CreateNatGateway",
        "ec2:DeleteNatGateway"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:elastic-ip/*"
    },
    {
      "Sid": "NATGatewayManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateNatGateway",
        "ec2:CreateTags",
        "ec2:DeleteNatGateway"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:natgateway/*"
    },
    {
      "Sid": "VPCEndpointManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateVpcEndpoint",
        "ec2:CreateTags",
        "ec2:DeleteVpcEndpoints"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:vpc-endpoint/*"
    },
    {
      "Sid": "MigrationKeyPairManagement",
      "Effect": "Allow",
      "Action": ["ec2:ImportKeyPair", "ec2:DeleteKeyPair", "ec2:RunInstances"],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:key-pair/*"
    },
    {
      "Sid": "InstanceLifecycleManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances",
        "ec2:CreateTags",
        "ec2:TerminateInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:instance/*"
    },
    {
      "Sid": "InstanceLaunchNetworkInterface",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:network-interface/*"
    },
    {
      "Sid": "InstanceLaunchVolume",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:volume/*"
    },
    {
      "Sid": "InstanceLaunchAMI",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "arn:aws:ec2:<AWS REGION>::image/*"
    }
  ]
}
```

> [!Note]
> The below IAM permissions are only required if you are using IAM to establish a cluster link between MSK and the jump cluster (Type 1). These will need to be applied to the EC2 Instance Profile role `jump-cluster-broker-iam-role-name`.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "kafka-cluster:Connect",
        "kafka-cluster:DescribeClusterDynamicConfiguration"
      ],
      "Resource": "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>"
    },
    {
      "Effect": "Allow",
      "Action": [
        "kafka-cluster:DescribeTopic",
        "kafka-cluster:ReadData",
        "kafka-cluster:AlterTopicDynamicConfiguration",
        "kafka-cluster:DescribeTopicDynamicConfiguration",
        "kafka-cluster:AlterTopic"
      ],
      "Resource": "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/*"
    },
    {
      "Effect": "Allow",
      "Action": ["kafka-cluster:DescribeGroup"],
      "Resource": "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:group/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/*"
    }
  ]
}
```

> [!TIP]
> The MSK cluster ID is the final UUID in the MSK cluster's ARN. If your MSK cluster ARN is `arn:aws:kafka:us-east-1:XXX:cluster/XXX/1a2345b6-bf9f-4670-b13b-710985f5645d-5`, the cluster ID would be `1a2345b6-bf9f-4670-b13b-710985f5645d-5`.

`reverse-proxy`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "EC2ReadOnlyAccess",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeKeyPairs",
        "ec2:DescribeInternetGateways",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeAvailabilityZones",
        "ec2:DescribeRouteTables",
        "ec2:DescribeSubnets",
        "ec2:DescribeImages",
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribeInstances",
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeTags",
        "ec2:DescribeVolumes",
        "ec2:DescribeInstanceCreditSpecifications"
      ],
      "Resource": "*"
    },
    {
      "Sid": "ReverseProxyKeyPairManagement",
      "Effect": "Allow",
      "Action": ["ec2:ImportKeyPair", "ec2:DeleteKeyPair"],
      "Resource": [
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:key-pair/reverse-proxy-ssh-key",
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:key-pair/reverse-proxy-ssh-key-*"
      ]
    },
    {
      "Sid": "VPCResourceCreation",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSecurityGroup",
        "ec2:CreateRouteTable",
        "ec2:CreateSubnet"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:vpc/<MSK VPC ID>"
    },
    {
      "Sid": "SecurityGroupManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSecurityGroup",
        "ec2:CreateTags",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:DeleteSecurityGroup",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:AuthorizeSecurityGroupEgress"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:security-group/*"
    },
    {
      "Sid": "RouteTableManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateRouteTable",
        "ec2:CreateTags",
        "ec2:CreateRoute",
        "ec2:DeleteRouteTable",
        "ec2:AssociateRouteTable",
        "ec2:DisassociateRouteTable"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:route-table/*"
    },
    {
      "Sid": "SubnetManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateSubnet",
        "ec2:CreateTags",
        "ec2:ModifySubnetAttribute",
        "ec2:DeleteSubnet",
        "ec2:AssociateRouteTable",
        "ec2:DisassociateRouteTable"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:subnet/*"
    },
    {
      "Sid": "InstanceLaunchAndManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances",
        "ec2:CreateTags",
        "ec2:DescribeInstanceAttribute",
        "ec2:TerminateInstances"
      ],
      "Resource": [
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:instance/*",
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:network-interface/*",
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:volume/*",
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:subnet/*",
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:security-group/*",
        "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:key-pair/reverse-proxy-ssh-key*",
        "arn:aws:ec2:<AWS REGION>::image/*"
      ]
    }
  ]
}
```

---

#### `kcp create-asset bastion-host`

This command generates Terraform configurations to provision a new bastion host in your specified VPC.

> [!NOTE]
> If your MSK cluster is reachable from your local machine or already have a bastion host/jump server provisioned, you may skip this command.

**Required Arguments**:

- `--region`: The region where the bastion host will be provisioned in
- `--bastion-host-cidr`: The CIDR of the public subnet associated with the bastion host
- `--vpc-id`: The VPC ID of the VPC that the **MSK cluster is deployed in**

**Optional Arguments**:

- `--create-igw`: When set, Terraform will create a new internet gateway in the VPC. If an Internet Gateway is not required, do not set this flag.
- `--security-group-ids`: When set, Terraform will use this security group for the bastion host.

**Example Usage**

```shell
kcp create-asset bastion-host \
  --region us-east-1 \
  --bastion-host-cidr 10.0.XXX.0/24 \
  --vpc-id vpc-xxxxxxxxx \
  --security-group-ids sg-xxxxxxxxxx
```

**Output:**
The command creates a `bastion_host` directory containing Terraform configurations that provision:

- **EC2 instance** (t2.medium running Amazon Linux 2023)
  - Public IP for remote access
  - SSH access on port 22
  - Pre-configured with migration tools
- **Public subnet** in the specified VPC
- **Security group** allowing SSH access. Created when `--security-group-ids` parameter is not provided.
- **SSH key pair** for secure access
- **Route table** for internet connectivity

#### New Bastion Host Architecture

This diagram illustrates how the kcp generated bastion host in AWS connects to an MSK cluster for the migration operations.

```
┌──────────────────────────────────────────────────────────────────┐
│                     User's Local Machine                         │
│                                                                  │
│  ┌─────────────────┐          ┌────────────────────┐             │
│  │  migration CLI  │ ───────► │ Bastion Host Asset │             │
│  └─────────────────┘          └─────────┬──────────┘             │
└─────────────────────────────────────────┼────────────────────────┘
                                          |
                                          | Internet
                                          |
┌─────────────────────────────────────────┼────────────────────────┐
│                           AWS VPC       |                        │
│                                         ▼                        │
│  ┌─────────────────┐    ┌──────────────────┐    ┌─────────────┐  │
│  │   MSK Cluster   │    │  New Jump        │    │   Internet  │  │
│  │                 │    │  Server          │    │   Gateway   │  │
│  │  ┌───────────┐  │    │                  │    │             │  │
│  │  │  Broker 1 │  │    │  ┌─────────────┐ │    │             │  │
│  │  └───────────┘  │    │  │  Deployed   │ │    │             │  │
│  │  ┌───────────┐  │    │  │migration CLI│ │    │             │  │
│  │  │  Broker 2 │  │◄──►│  └─────────────┘ │    │             │  │
│  │  └───────────┘  │    │                  │    │             │  │
│  │  ┌───────────┐  │    │                  │    │             │  │
│  │  │  Broker 3 │  │    │                  │    │             │  │
│  │  └───────────┘  │    │                  │    │             │  │
│  └─────────────────┘    └──────────────────┘    └─────────────┘  │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

#### Existing Bastion Host Architecture

When using an existing bastion host, simply move the CLI to that server:

```bash
# 1. SSH into your existing bastion host.

# 2. Deploy the CLI on the bastion host.

# 2a. (Optional) Re-run `kcp init` and re-set all
#      environment variables.

# 2b. (Optional) Re-run `kcp scan cluster` to regenerate
#     the cluster file or copy it across from the previous run.

# 4. Run CLI commands from your bastion host.
```

This diagram illustrates how kcp expects the bastion host setup to successfully connect to MSK and begin the migration operations. The bastion host serves as a secure jump point within the MSK VPC to access the MSK Kafka cluster.

```
┌─────────────────────────────────────────────────────────────────────┐
│                           AWS VPC                                   │
│                                                                     │
│  ┌─────────────────┐    ┌──────────────────────┐    ┌─────────────┐ │
│  │   MSK Cluster   │    │ Existing Bastion     │    │   Internet  │ │
│  │                 │    │ Host                 │    │   Gateway   │ │
│  │  ┌───────────┐  │    │                      │    │             │ │
│  │  │  Broker 1 │  │    │  ┌───────────────┐   │    │             │ │
│  │  └───────────┘  │    │  │    Deployed   │   │    │             │ │
│  │  ┌───────────┐  │    │  │ migration CLI │   │    │             │ │
│  │  │  Broker 2 │  │◄──►│  └───────────────┘   │    │             │ │
│  │  └───────────┘  │    │                      │    │             │ │
│  │  ┌───────────┐  │    │                      │    │             │ │
│  │  │  Broker 3 │  │    │                      │    │             │ │
│  │  └───────────┘  │    │                      │    │             │ │
│  └─────────────────┘    └──────────────────────┘    └─────────────┘ │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

#### `kcp create-asset migration-infra`

This command generates the required Terraform to provision your migration environment. The `--type` flag will determine how the Confluent Platform jump cluster with authenticate with MSK - using either IAM or SASL/SCRAM.

**Required Arguments**:

- `--cluster-file`: Path to cluster configuration file
- `--region`: The region in which the ansible control node & jump clusters will be hosted in
- `--vpc-id`: The VPC ID of the VPC that the **MSK cluster is deployed in**
- `--type`: The type of authentication to use to establish the cluster link between AWS MSK and Confluent Platform jump cluster
- `--cc-env-name`: The Confluent Cloud environment name where data will be migrated to
- `--cc-cluster-name`: The Confluent Cloud cluster name where data will be migrated to
- `--cc-cluster-type`: The Confluent Cloud cluster type - Dedicated or Enterprise

**Optional Arguments**:

- `--security-group-ids`: When set, Terraform will use this security group for the ansible host and CP jump cluster.

**Type Options** (choose one):

- 1: MSK private cluster w/ SASL_IAM authentication to Confluent Cloud private cluster.
- 2: MSK private cluster w/ SASL_SCRAM authentication to Confluent Cloud private cluster.
- 3: MSK public cluster w/ SASL_SCRAM authentication to Confluent Cloud public cluster.

**Example Usage**

> [!NOTE]
> The example below uses `--type 2` which indicates that SASL/SCRAM will be used to establish a connection between AWS MSK and the Confluent Platform jump clusters. Moreover, some values like the VPC ID and Confluent Cloud environment/cluster name have been inferred from `--cluster-file`, though can be overwritten by their respective flags.

```bash
kcp create-asset migration-infra \
  --region us-east-1 \
  --cluster-file path/to/clusterfile.json \
  --type 2 \
  --cluster-link-sasl-scram-username my-cluster-link-user \
  --cluster-link-sasl-scram-password pa55word \
  --cc-cluster-type enterprise \
  --ansible-control-node-subnet-cidr 10.0.XXX.0/24 \
  --jump-cluster-broker-subnet-config us-east-1a:10.0.XXX.0/24,us-east-1b:10.0.XXX.0/24,us-east-1c:10.0.XXX.0/24 \
  --security-group-ids sg-xxxxxxxxxx
```

**Output:**
The command creates a `migration-infra` directory containing Terraform configurations that provision:

- **EC2 Instance** - Ansible Control Node that will provision the Confluent Platform jump cluster.
- **3x EC2 Instances** - Confluent Platform jump clusters made up of 3 brokers.
- **Networking** - NAT gateway, Elastic IPs, subnets, security groups, route tables & associations. Security Groups are created when `--security-group-ids` parameter is not provided.
- **Confluent Cloud** - Environment, Cluster, Schema Registry, Service Accounts, API keys.
- **Private Link** - Establish VPC connectivity between the MSK VPC and Confluent Cloud cluster.

---

#### `kcp create-asset migration-scripts`

This command generates migration scripts that mirror topics from MSK to Confluent Platform jump clusters and then finally to Confluent Cloud.

**Example Usage**:

```shell
kcp create-asset migration-scripts \
  --cluster-file cluster_scan_kcp-msk-cluster.json \
  --migration-infra-folder migration_infra
```

> [!NOTE]
> This command does not require AWS IAM permissions as it generates local scripts and configuration files. The mirror topics piggyback off the authentication link established in the cluster link.

**Output:**
The command creates a `migration_scripts` directory containing shell scripts:

- `msk-to-cp-mirror-topics.sh` - Individual `kafka-mirror` commands per topic to move data from MSK to the Confluent Platform jump cluster.
- `destination-cluster-properties` - Kafka client configuration file.
- `cp-to-cc-mirror-topics.sh` - Individual cURL requests to the Confluent Cloud API per topic move data from the Confluent Platform jump cluster to Confluent Cloud.

> [!NOTE]
> A `README.md` is generated in the `migration_scripts` directory to further assist in migrating the data from MSK to Confluent Cloud.

---

#### `kcp create-asset reverse-proxy`

Create reverse proxy infrastructure assets to allow observability into migrated data in Confluent Cloud.

**Required Arguments**:

- `--region`: The region where the reverse proxy EC2 instance will be provisioned in
- `--vpc-id`: The VPC ID of the VPC that the **MSK cluster is deployed in**
- `--migration-infra-folder`: Path to migration infrastructure folder that was previously generated
- `--reverse-proxy-cidr`: The CIDR of the subnet associated with the reverse proxy

**Optional Arguments**:

- `--security-group-ids`: When set, Terraform will use this security group for the bastion host.

**Example Usage**

```shell
kcp create-asset reverse-proxy \
  --region us-east-1 \
  --vpc-id vpc-xxxxxxxxx \
  --migration-infra-folder migration-infra \
  --reverse-proxy-cidr 10.0.XXX.0/24 \
  --security-group-ids sg-xxxxxxxxxx
```

**Output**
The command creates a `reverse-proxy` directory containing Terraform configurations that provision:

- **EC2 Instance** - The reverse-proxy bridge between the local machine and the VPC that MSK and Confluent Cloud are connected to.
- **Networking** - Security groups, subnet, route tables & associations. Security Groups are created when `--security-group-ids` parameter is not provided.
- **Confluent Cloud** - Environment, Cluster, Schema Registry, Service Accounts, API keys.
- **`generate_dns_entries.sh`** - Script that creates DNS entries mapping the reverse proxy's IP to Confluent Cloud broker hostnames for local /etc/hosts configuration.

> [!NOTE]
> A `README.md` is generated in the `reverse-proxy` directory to further assist in setting up the reverse proxy on your local machine to view the private networked Confluent Cloud cluster.

---

### `kcp update`

This command will update the kcp binary to the latest version by downloading latest release from GitHub and installing it.

**Example Usage**

```shell
kcp update
```

**Optional Arguments**:

- `--force`: Force update without user confirmation
- `--check-only`: Only check for updates, don't install

>[!NOTE]
> This will require sudo to update the binary.

---

## Development

### Prerequisites

- Go 1.24+
- Make

```bash
# Clone the repository
git clone https://github.com/confluentinc/kcp.git
cd kcp

# Install to system path (requires sudo)
make install
```

### Build Commands

```bash
# Build for current platform
make build

# Build for Linux
make build-linux

# Build for all platforms
make build-all

# Clean build artifacts
make clean
```

### Testing & Quality

```bash
# Format go code
make fmt

# Run tests
make test

# Run tests with coverage
make test-cov

# Run tests with coverage and open UI coverage browser
make test-cov-ui
```
