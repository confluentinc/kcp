# KCP CLI

- [Getting Started](#getting-started)
  - [Installation](#installation)
  - [Authentication](#authentication)
  - [kcp Commands](#kcp-commands)
    - [`kcp discover`](#kcp-discover)
    - [`kcp scan`](#kcp-scan)
      - [`kcp scan clusters`](#kcp-scan-clusters)
      - [`kcp scan client-inventory`](#kcp-scan-client-inventory)
      - [`kcp scan schema-registry`](#kcp-scan-schema-registry)
    - [`kcp report`](#kcp-report)
      - [`kcp report costs`](#kcp-report-costs)
      - [`kcp report metrics`](#kcp-report-metrics)
    - [`kcp create-asset`](#kcp-create-asset)
      - [`kcp create-asset bastion-host`](#kcp-create-asset-bastion-host)
      - [`kcp create-asset migrate-acls`](#kcp-create-asset-migrate-acls)
        - [`kcp create-asset migrate-acls iam`](#kcp-create-asset-migrate-acls-iam)
        - [`kcp create-asset migrate-acls kafka`](#kcp-create-asset-migrate-acls-kafka)
      - [`kcp create-asset migrate-connectors`](#kcp-create-asset-migrate-connectors)
        - [`kcp create-asset migrate-connectors msk`](#kcp-create-asset-migrate-connectors-msk)
        - [`kcp create-asset migrate-connectors self-managed`](#kcp-create-asset-migrate-connectors-self-managed)
      - [`kcp create-asset migration-infra`](#kcp-create-asset-migration-infra)
      - [`kcp create-asset migrate-schemas`](#kcp-create-asset-migrate-schemas)
      - [`kcp create-asset migrate-topics`](#kcp-create-asset-migrate-topics)
      - [`kcp create-asset reverse-proxy`](#kcp-create-asset-reverse-proxy)
      - [`kcp create-asset target-infra`](#kcp-create-asset-target-infra)
    - [`kcp migration`](#kcp-migration)
      - [`kcp migration init`](#kcp-migration-init)
      - [`kcp migration execute`](#kcp-migration-execute)
      - [`kcp migration lag-check`](#kcp-migration-lag-check)
      - [`kcp migration list`](#kcp-migration-list)
    - [`kcp ui`](#kcp-ui)
    - [`kcp update`](#kcp-update)

# Getting Started

> [!NOTE]
> Currently, only migrations from AWS MSK are supported. Therefore, until later Apache Kafka migrations are supported, AWS MSK will be the reference point for the source of a migration.

## Installation

You can download kcp from GitHub under the [releases tab](https://github.com/confluentinc/kcp/releases/latest). We provide support for Linux, Darwin, and Windows amd64 systems.

### Linux/macOS

Set a variable for the latest release:

```
LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
```

Set a variable for your platform (comment and uncomment as appropriate):

```
PLATFORM=darwin_amd64
# PLATFORM=darwin_arm64
# PLATFORM=linux_amd64
# PLATFORM=linux_arm64
```

Download the binary:

```
curl -L -o kcp_${LATEST_TAG}.tar.gz "https://github.com/confluentinc/kcp/releases/download/${LATEST_TAG}/kcp_${PLATFORM}.tar.gz"
```

Untar the binary:

```
tar -xzf kcp_${LATEST_TAG}.tar.gz
```

Ensure binary is executable:

```
chmod +x ./kcp/kcp
```

Test the binary:

```
./kcp/kcp version
```

You should see output similar to :

```
Executing kcp with build version=0.4.5 commit=a8ef9fd2b4f1d000a00717b2f5f46fa30ad74e08 date=2025-11-13T12:56:00Z
Version: 0.4.5
Commit:  a8ef9fd2b4f1d000a00717b2f5f46fa30ad74e08
Date:    2025-11-13T12:56:00Z
```

If you wish to run kcp from anywhere on your system, move the binary to somewhere on your PATH, e.g.:

```shell
sudo mv ./kcp/kcp /usr/local/bin/kcp
```

Test the installation to your PATH:

```
kcp version
```

You should see output similar to :

```
Executing kcp with build version=0.4.5 commit=a8ef9fd2b4f1d000a00717b2f5f46fa30ad74e08 date=2025-11-13T12:56:00Z
Version: 0.4.5
Commit:  a8ef9fd2b4f1d000a00717b2f5f46fa30ad74e08
Date:    2025-11-13T12:56:00Z
```

### Windows

Download the latest Windows artifact from the [releases page](https://github.com/confluentinc/kcp/releases/latest), using either:

- `kcp_windows_amd64.exe` for a single executable
- `kcp_windows_amd64.zip` for a packaged archive

If you download the zip, extract it and run `kcp.exe`.

Optionally, move `kcp.exe` to a folder on your `PATH` so you can run `kcp` from any terminal.

Verify the installation by running `kcp version`.

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

## Workflow steps

The migration process follows these general steps:

1. **Initialize the environment**: Set up the CLI and configure your environment.
2. **Scan clusters**: Discover and analyze your Kafka deployment.
3. **Generate reports**: Produce cost and metrics reports for your MSK clusters using `kcp report costs` and `kcp report metrics`.
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

---

### `kcp discover`

The kcp discover command performs a full discovery of all MSK clusters in an AWS account across multiple regions, together with their associated resources including topics as well as costs and metrics.

**Optional Flags**

You can skip certain discovery operations using the following flags:

- `--skip-topics`: Skips the topic discovery through the AWS MSK API. Clusters with a large topic count may take a considerable amount of time to complete the discovery due to limits on the MSK API. If you wish to skip the topic discovery, you can use this flag and later use the `kcp scan clusters` command to perform topic discovery through the Kafka Admin API that does not have the same request limits. However, this will require the cluster to be either publicly accessible or within the same network as kcp to complete.
- `--skip-costs`: Skips the cost discovery through the AWS Cost Explorer API. Useful when you don't have Cost Explorer permissions or want faster discovery runs.
- `--skip-metrics`: Skips the metrics discovery through the AWS CloudWatch API. Useful when you don't have CloudWatch permissions or want faster discovery runs.

**Example Usage**

`kcp discover --region us-east-1 --region eu-west-3`

or

`kcp discover --region us-east-1,eu-west-3`

or with skip flags:

`kcp discover --region us-east-1 --skip-topics --skip-costs --skip-metrics`

The command will produce a cluster-credentials.yaml and a kcp-state.json file. An overview will be output to the terminal and a more in-depth breakdown can be seen in the [UI](#kcp-ui).

This command requires the following permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MSKScanPermissions",
      "Effect": "Allow",
      "Action": [
        "kafka:ListClustersV2",
        "kafka:ListReplicators",
        "kafka:ListVpcConnections",
        "kafka:GetCompatibleKafkaVersions",
        "kafka:GetBootstrapBrokers",
        "kafka:ListConfigurations",
        "kafka:DescribeClusterV2",
        "kafka:ListKafkaVersions",
        "kafka:ListNodes",
        "kafka:ListClusterOperationsV2",
        "kafka:ListScramSecrets",
        "kafka:ListClientVpcConnections",
        "kafka:GetClusterPolicy",
        "kafka:DescribeConfigurationRevision",
        "kafka:DescribeReplicator",
        "kafkaconnect:ListConnectors",
        "kafkaconnect:DescribeConnector"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKClusterConnect",
      "Effect": "Allow",
      "Action": ["kafka-cluster:Connect", "kafka-cluster:DescribeCluster"],
      "Resource": ["*"]
    },
    {
      "Sid": "MSKTopicActions",
      "Effect": "Allow",
      "Action": [
        "kafka:ListTopics",
        "kafka:DescribeTopic",
        "kafka-cluster:DescribeTopic",
        "kafka-cluster:DescribeTopicDynamicConfiguration"
      ],
      "Resource": ["*"]
    },
    {
      "Sid": "CostMetricsScanPermissions",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:GetMetricData",
        "ce:GetCostAndUsage",
        "cloudwatch:GetMetricStatistics",
        "cloudwatch:ListMetrics"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKNetworkingScanPermission",
      "Effect": "Allow",
      "Action": ["ec2:DescribeSubnets"],
      "Resource": "*"
    }
  ]
}
```

> [!NOTE]
> Some permissions are optional depending on which skip flags you use:
>
> - If using `--skip-topics`, the `MSKTopicActions` permissions are not required
> - If using `--skip-costs`, the `ce:GetCostAndUsage` permission is not required
> - If using `--skip-metrics`, the CloudWatch permissions (`cloudwatch:GetMetricData`, `cloudwatch:GetMetricStatistics`, `cloudwatch:ListMetrics`) are not required

### `kcp scan`

The `kcp scan` commands perform various different scans on MSK clusters, scanning clusters to get additional broker level information or scanning Kafka broker logs to identify potential clients.

The `kcp scan` command includes the following sub-commands:

- `clusters`
- `client-inventory`
- `schema-registry`

The sub-commands require the following minimum AWS IAM permissions:

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
    },
    {
      "Sid": "MSKConnectTopicAccess",
      "Effect": "Allow",
      "Action": ["kafka-cluster:ReadData"],
      "Resource": [
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-configs",
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-status"
      ]
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

#### `kcp scan clusters`

Scan multiple MSK clusters at the Kafka level using the generated assets of the `kcp discover` command to drive it.

> [!NOTE]
> Optionally, you can provide the kcp user/principal connecting to the MSK cluster with permissions to read the `connect-configs` and `connect-status` topics. This allows kcp to aggregate discovered self-managed connectors, their connect URL, state and config.

**Example Usage**

```shell
kcp scan clusters \
  --state-file kcp-state.json \
  --credentials-file cluster-credentials.yaml
```

**Output:**
The command appends the gathered list of ACLs, topics and the Kafka cluster ID to each cluster's entries in the kcp-state.json file. If provided with sufficient permissions, kcp will also consume from the `connect-status` and `connect-configs` topics, if they exist, and gather self-managed connectors and their running state/configs. An overview will be output to the terminal and a more in-depth breakdown can be seen in the [UI](#kcp-ui).

---

#### `kcp scan client-inventory`

This command scans a hour window folder in s3 to identify as many clients as possible in the cluster.

**Prerequisites**

- Enable trace logging for `kafka.server.KafkaApis=TRACE` for each broker
- Enable s3 broker log delivery for the cluster

**Example Usage**

```shell
kcp scan client-inventory \
--s3-uri  s3://my-cluster-logs-bucket/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-1a2345b6-bf9f-4670-b13b-710985f5645d-5/2025-08-13-14/
--state-file kcp-state.json
```

**Output:**
The command writes to `discovered_clients` in the state file for the corresponding cluster:

- All the unique clients it could identify based on a combination of values:
  - i.e. clientID + topic + role + auth + principal

example output

```json
 "discovered_clients": [
    {
        "composite_key": "stock-levels-producer|stock-levels|Producer|IAM|arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/gsmith@confluent.io",
        "client_id": "stock-levels-producer",
        "role": "Producer",
        "topic": "stock-levels",
        "auth": "IAM",
        "principal": "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/gsmith@confluent.io",
        "timestamp": "2025-08-21T13:01:28Z"
    }
  ]
```

Alternatively, the following environment variables need to be set:

```shell
export S3_URI=<folder-in-s3>
```

---

#### `kcp scan schema-registry`

This command scans a schema registry to capture all subjects, their versions, schema metadata, and compatibility settings. The information is appended to the kcp-state.json file for migration planning.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the schema registry information will be written to
- `--url`: The URL of the schema registry to scan

**Authentication Options** (choose one):

- `--use-unauthenticated`: Use unauthenticated access
- `--use-basic-auth`: Use basic authentication (requires `--username` and `--password`)

**Example Usage**

```shell
# With unauthenticated access
kcp scan schema-registry \
  --state-file kcp-state.json \
  --url https://my-schema-registry:8081 \
  --use-unauthenticated

# With basic authentication
kcp scan schema-registry \
  --state-file kcp-state.json \
  --url https://my-schema-registry:8081 \
  --use-basic-auth \
  --username my-username \
  --password my-password
```

**Output:**
The command appends the following schema registry information to the kcp-state.json file:

- Schema registry type and URL
- Default compatibility level
- All subjects and their versions
- Latest schema metadata for each subject

Alternatively, the following environment variables can be set:

```shell
export STATE_FILE=<path-to-state-file>
export URL=<schema-registry-url>
export USE_UNAUTHENTICATED=<true|false>
export USE_BASIC_AUTH=<true|false>
export USERNAME=<username>
export PASSWORD=<password>
```

---

### `kcp report`

The `kcp report` command generates reports based on the data collected by `kcp discover`. It includes the following sub-commands:

- `costs`
- `metrics`

---

#### `kcp report costs`

Generate a cost report for given region(s) based on the data collected by `kcp discover`.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the MSK cluster discovery reports have been written to

**Optional Arguments**:

- `--region`: The AWS region(s) to include in the report (comma separated list or repeated flag). (If not provided, all regions in the state file will be included.)
- `--start`: Inclusive start date for cost report (YYYY-MM-DD) (Defaults to 31 days prior to today)
- `--end`: Exclusive end date for cost report (YYYY-MM-DD) (Defaults to today)

The above optional arguments are all required if one is supplied. If none are supplied, a report generating costs for all regions present in the `state-file.json` for the last thirty full days will be generated.

**Example Usage**

```shell
kcp report costs \
  --state-file kcp-state.json \
  --region us-east-1 \
  --region eu-west-3
```

or

```shell
kcp report costs \
  --state-file kcp-state.json \
  --region us-east-1,eu-west-3 \
  --start 2024-01-01 \
  --end 2024-01-31
```

or

```shell
kcp report costs \
  --state-file kcp-state.json \
```

**Output:**
The command generates a `cost_report_YYYY-MM-DD_HH-MM-SS.md` file containing cost analysis for the specified regions and time period.

---

#### `kcp report metrics`

Generate a metrics report for given cluster(s) based on the data collected by `kcp discover`.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the MSK cluster discovery reports have been written to

**Optional Arguments**:

- `--cluster-arn`: The AWS cluster ARN(s) to include in the report (comma separated list or repeated flag). If not provided, all clusters in the state file will be included.
- `--start`: Inclusive start date for metrics report (YYYY-MM-DD). (Defaults to 31 days prior to today)
- `--end`: Exclusive end date for cost report (YYYY-MM-DD). (Defaults to today)

The above optional arguments are all required if one is supplied. If none are supplied, a report generating metrics for all clusters present in the `state-file.json` for the last thirty full days will be generated.

**Example Usage**

```shell
kcp report metrics \
  --state-file kcp-state.json \
  --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc123 \
  --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/def456
```

or

```shell
kcp report metrics \
  --state-file kcp-state.json \
  --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc123 \
  --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/def456 \
  --start 2024-01-01 \
  --end 2024-01-31
```

**Output:**
The command generates a `metric_report_YYYY-MM-DD_HH-MM-SS.md` file containing metrics analysis for the specified clusters and time period.

or

```shell
kcp report metrics \
  --state-file kcp-state.json \
```

---

### `kcp create-asset`

The `kcp create-asset` command includes the following sub-commands:

- `bastion-host`
- `migrate-acls`
- `migrate-connectors`
- `migration-infra`
- `migrate-schemas`
- `migrate-topics`
- `reverse-proxy`
- `target-infra`

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

# 2a. (Optional) Re-run `kcp discover` to regenerate
#     the kcp-state file or copy it across from the previous run.

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

#### `kcp create-asset migrate-acls`

The `kcp create-asset migrate-acls` command generates Terraform based on the ACLs discovered by `kcp scan clusters` as well as the possibility of passing a IAM user/role ARN. It includes the following sub-commands:

- `iam`
- `kafka`

#### `kcp create-asset migrate-acls iam`

This command generates the Terraform to convert and provision ACLs from provided IAM users/roles ARNs or by passing a client inventory file generated from the `kcp scan client-inventory` command.

**Required Arguments**:

- `--role-arn`: IAM Role ARN to convert ACLs from.
- `--user-arn`: IAM User ARN to convert ACLs from.
- `--client-file`: The client discovery file generated by the 'kcp scan client-inventory' command.

**Optional Arguments**:

- `--output-dir`: The directory where the Confluent Cloud Terraform ACL assets will be written to.
- `--skip-audit-report`: Skip generating an audit report of the converted ACLs.

**Output:**

This will generate either a directory based on the cluster name or into a user-defined output directory with a Terraform resource per principal/user for creating an equivalent Confluent Cloud service account with individual ACL resources.

#### `kcp create-asset migrate-acls kafka`

This command generates the Terraform to convert and provision ACLs discovered from the `kcp scan clusters` command.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the MSK cluster discovery reports have been written to.
- `--cluster-arn`: The ARN of the MSK cluster to convert ACLs from.

**Optional Arguments**:

- `--output-dir`: The directory where the Confluent Cloud Terraform ACL assets will be written to.
- `--skip-audit-report`: Skip generating an audit report of the converted ACLs.

**Output:**

This will generate either a directory based on the cluster name or into a user-defined output directory with a Terraform resource per principal/user for creating an equivalent Confluent Cloud service account with individual ACL resources.

---

#### `kcp create-asset migrate-connectors`

The `kcp create-asset migrate-connectors` command generates Terraform based on MSK Connect connectors discovered by the `kcp discover` command and self-managed connectors discovered by `kcp scan clusters`. It includes the following sub-commands:

- `connector-utility`
- `msk`
- `self-managed`

> [!NOTE]
> This requires having provisioned a Confluent Cloud environment and cluster as well as having API keys with the `Cloud Resource Management` scope. This is due to using the `.../translate/config` Confluent API endpoint to convert self-managed connector configs to fully-managed configs.

#### `kcp create-asset migrate-connectors connector-utility`

This command generates the JSON per MSK cluster that can be used by the [Connect Migration Utility](https://github.com/confluentinc/connect-migration-utility) to migrate connectors.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the cluster discovery reports have been written to.

**Optional Arguments**:

- `--cluster-arn`: The ARN of the MSK cluster to generate the connector configs JSON from.
- `--output-dir`: The directory where the Confluent Cloud Terraform connector assets will be written to.

**Output:**

This will generate a JSON file per MSK cluster containing the connector configs discovered from `kcp discover` and `kcp scan clusters` in a directory named `discovered-connector-configs` or by the `--output-dir` flag.

#### `kcp create-asset migrate-connectors msk`

This command generates the Terraform from the running MSK Connect connector(s) to a valid Confluent fully-managed config.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the cluster discovery reports have been written to.
- `--cluster-arn`: The ARN of the cluster to migrate connectors from.
- `--cc-environment-id`: The ID of the Confluent Cloud environment to migrate connectors to.
- `--cc-cluster-id`: The ID of the Confluent Cloud cluster to migrate connectors to.
- `--cc-api-key`: The API key for the Confluent Cloud cluster to migrate connectors to.
- `--cc-api-secret`: The API secret for the Confluent Cloud cluster to migrate connectors to.

**Optional Arguments**:

- `--output-dir`: The directory where the Confluent Cloud Terraform connector assets will be written to.

**Output:**

This will generate a Terraform file per Confluent Cloud connector based on the MSK cluster name or directory name defined by the `--output-dir` flag.

#### `kcp create-asset migrate-connectors self-managed`

This command generates the Terraform from the running self-managed connector(s) to a valid Confluent fully-managed config.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the cluster discovery reports have been written to.
- `--cluster-arn`: The ARN of the cluster to migrate connectors from.
- `--cc-environment-id`: The ID of the Confluent Cloud environment to migrate connectors to.
- `--cc-cluster-id`: The ID of the Confluent Cloud cluster to migrate connectors to.
- `--cc-api-key`: The API key for the Confluent Cloud cluster to migrate connectors to.
- `--cc-api-secret`: The API secret for the Confluent Cloud cluster to migrate connectors to.

**Optional Arguments**:

- `--output-dir`: The directory where the Confluent Cloud Terraform connector assets will be written to.

**Output:**

This will generate a Terraform file per Confluent Cloud connector based on the MSK cluster name or directory name defined by the `--output-dir` flag.

---

#### `kcp create-asset target-infra`

This command generates Terraform assets for Confluent Cloud target infrastructure including environment, cluster, and private link setup. The infrastructure to be provisioned can be managed using the `--needs-environment`, `--needs-cluster` and `--needs-private-link` flags.

**Required**:

One of the following combinations is required - `--state-file` + `--cluster-arn` OR `--aws-region` + `--vpc-id`.

- `--state-file`: Path to kcp state file.
- `--cluster-arn`: MSK cluster ARN.

- `--aws-region`: AWS region for the infrastructure to be provisioned.
- `--vpc-id`: The AWS VPC ID used by Private Link.

**Target Environment**:

- `--needs-environment`: Whether to create a new environment (true) or use existing (false)
- `--env-name`: Name for new environment (required when `--needs-environment=true`)
- `--env-id`: ID of existing environment (required when `--needs-environment=false`)

**Target Cluster**:

- `--needs-cluster`: Whether to create a new cluster (true) or use existing (false)
- `--cluster-name`: Name for new cluster (required when `--needs-cluster=true`)
- `--cluster-id`: ID of existing cluster (required when `--needs-cluster=false`)
- `--cluster-type`: Cluster type ('dedicated' or 'enterprise')
- `--cluster-availability`: Cluster availability zone type ('SINGLE_ZONE' or 'MULTI_ZONE'). MULTI_ZONE requires `--cluster-cku >= 2`. (default: 'SINGLE_ZONE')
- `--cluster-cku`: Number of CKUs for dedicated clusters (must be >= 1, MULTI_ZONE requires >= 2). (default: 1)
- `--prevent-destroy`: Whether to set `lifecycle { prevent_destroy = true }` on generated Terraform resources. (default: true)

**Private Link**:

- `--needs-private-link`: Whether the infrastructure needs private link setup
- `--subnet-cidrs`: Subnet CIDRs for private link (required when `--needs-private-link=true`)

**Optional Arguments**:

- `--output-dir`: Output directory for generated Terraform files (default: "target_infra")

**Example Usage**

```bash
kcp create-asset target-infra \
--state-file kcp-state.json \
--cluster-arn arn:aws:kafka:us-east-1:XXX:cluster/XXX/1a2345b6-bf9f-4670-b13b-710985f5645d-5 \
--needs-environment true \
--env-name example-environment \
--needs-cluster true \
--cluster-name example-cluster \
--cluster-type enterprise \
--needs-private-link true \
--subnet-cidrs 10.0.0.0/16,10.0.1.0/16,10.0.2.0/16 \
--output-dir confluent-cloud-infrastructure
```

**Output:**
The command creates a directory (default: `target-infra`) containing Terraform configurations that will provision a Confluent Cloud setup based on the provided flags.

---

#### `kcp create-asset migration-infra`

This command generates the required Terraform to provision your migration environment. The `--type` flag will determine how the Confluent Platform jump cluster will authenticate with MSK - using either IAM or SASL/SCRAM - as well some aspects of the networking to connect to an existing Private Link setup between the AWS VPC and Confluent Cloud.

**Required Arguments**:

- `--state-file`: Path to kcp-state.json file
- `--cluster-arn`: The cluster-arn to target
- `--type`: The type of authentication to use to establish the cluster link between AWS MSK and Confluent Platform jump cluster

**Base Migration Flags**:

- `--cluster-link-name`: The name of the cluster link that will be created as part of the migration.
- `--target-cluster-id`: The Confluent Cloud cluster ID.
- `--target-rest-endpoint`: The Confluent Cloud cluster REST endpoint.

**Base Optional Arguments**:

- `--existing-internet-gateway`: Whether to use an existing internet gateway. (default: false)
- `--output-dir`: The directory to output the migration infrastructure assets to. (default: 'migration-infra')

**Type Two Flags**:

- `--target-environment-id`: The Confluent Cloud environment ID.
- `--subnet-id`: [Optional] Subnet ID for the EC2 instance that provisions the cluster link. (default: MSK broker #1 subnet).
- `--security-group-id`: [Optional] Security group ID for the EC2 instance that provisions the cluster link. (default: MSK cluster security group).

**Type Three Flags**:

- `--target-environment-id`: The Confluent Cloud environment ID.
- `--target-bootstrap-endpoint`: The bootstrap endpoint to use for the Confluent Cloud cluster.
- `--existing-private-link-vpce-id`: The ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud. (Required)
- `--jump-cluster-broker-subnet-cidr`: The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.
- `--jump-cluster-setup-host-subnet-cidr`: The CIDR block to use for the jump cluster setup host subnet.
- `--jump-cluster-instance-type`: [Optional] The instance type to use for the jump cluster. (default: MSK broker type).
- `--jump-cluster-broker-storage`: [Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).

**Type Four Flags**:

- `--target-environment-id`: The Confluent Cloud environment ID.
- `--target-bootstrap-endpoint`: The bootstrap endpoint to use for the Confluent Cloud cluster.
- `--existing-private-link-vpce-id`: The ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud. (Required)
- `--jump-cluster-broker-subnet-cidr`: The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.
- `--jump-cluster-setup-host-subnet-cidr`: The CIDR block to use for the jump cluster setup host subnet.
- `--jump-cluster-iam-auth-role-name`: The IAM role name to authenticate the cluster link between MSK and the jump cluster.
- `--jump-cluster-instance-type`: [Optional] The instance type to use for the jump cluster. (default: MSK broker type).
- `--jump-cluster-broker-storage`: [Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).

**Type Options** (choose one):

_Public MSK Endpoints:_

- Type 1: Cluster Link [SASL/SCRAM]

_Private MSK Endpoints:_

- Type 2: External Outbound Cluster Link [SASL/SCRAM]
- Type 3: Jump Cluster [SASL/SCRAM]
- Type 4: Jump Cluster [IAM]

**Example Usage**

> [!NOTE]
> The example below uses `--type 3` which indicates that SASL/SCRAM will be used in conjunction with a jump cluster to migrate data. Type 3 requires an existing VPC endpoint (`--existing-private-link-vpce-id`) for the Private Link connection between the jump cluster and Confluent Cloud.

```bash
kcp create-asset migration-infra /
--state-file kcp-state.json /
--cluster-arn arn:aws:kafka:us-east-1:XXX:cluster/XXX/1a2345b6-bf9f-4670-b13b-710985f5645d-5 /
--existing-internet-gateway /
--output-dir type-3 /
--type 3 /
--existing-private-link-vpce-id vpce-0abc123def456789 /
--jump-cluster-broker-subnet-cidr 10.0.101.0/24,10.0.102.0/24,10.0.103.0/24 /
--jump-cluster-setup-host-subnet-cidr 10.0.104.0/24 /
--cluster-link-name type-3-link /
--target-environment-id env-a1bcde /
--target-cluster-id lkc-w89xyz /
--target-rest-endpoint https://lkc-w89xyz.XXX.aws.private.confluent.cloud:443 /
--target-bootstrap-endpoint lkc-w89xyz.XXX.aws.private.confluent.cloud:9092
```

**Output:**
The command creates a directory (default: `migration-infra`) containing Terraform configurations that provision:

- **Jump Cluster Setup Host** - EC2 instance running that will provision the Confluent Platform jump cluster.
- **N Jump Cluster Nodes** - EC2 instances that will host the Confluent Platform jump clusters made up of N brokers.
- **Networking** - NAT gateway, Elastic IPs, subnets, security groups, route tables & associations.
- **Private Link** - Establish VPC connectivity between the MSK VPC and Confluent Cloud cluster.

---

#### `kcp create-asset migrate-schemas`

This command generates Terraform assets for migrating Schema Registry schemas to Confluent Cloud using Schema Exporters.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the schema registry information has been written to
- `--url`: The URL of the schema registry to migrate schemas from

**Example Usage**:

```shell
kcp create-asset migrate-schemas \
  --state-file kcp-state.json \
  --url https://my-schema-registry.example.com
```

**Output:**
The command creates a `migrate_schemas` directory containing Terraform files:

- `main.tf` - Terraform configuration defining `confluent_schema_exporter` resources for schema migration
- `variables.tf` - Input variable definitions for source and destination Schema Registry details
- `inputs.auto.tfvars` - Auto-populated variable values from the kcp state file

**What it does:**
The generated Terraform configuration creates Schema Exporter resources that continuously sync schemas from your source Schema Registry to Confluent Cloud's Schema Registry. By default, it exports all subjects (`:*:`) with context type `NONE`.

**Next Steps:**

1. Navigate to the generated `migrate_schemas` directory
2. Review and customize the Terraform configuration if needed
3. Run `terraform init` to initialize the Terraform workspace
4. Run `terraform plan` to preview the changes
5. Run `terraform apply` to create the Schema Exporters

---

#### `kcp create-asset migrate-topics`

This command generates migration scripts that mirror topics from MSK to Confluent Platform jump clusters and then finally to Confluent Cloud.

**Required Arguments**:

- `--state-file`: The path to the kcp state file where the MSK cluster discovery reports have been written to.
- `--cluster-arn`: The ARN of the MSK cluster to create migration scripts for.
- `--target-cluster-id`: The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).
- `--target-cluster-rest-endpoint`: The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).
- `--target-cluster-link-name`: The name of the cluster link that was created as part of the migration (e.g., msk-to-cc-migration-link).

**Optional Arguments**:

- `--output-dir`: The directory to output the Terraform files to. (default: 'migrate_topics')

**Example Usage**:

```shell
kcp create-asset migrate-topics \
    --state-file kcp-state.json \
    --cluster-arn arn:aws:kafka:us-east-1:XXX:cluster/XXX/1a2345b6-bf9f-4670-b13b-710985f5645d-5 \
    --target-cluster-id lkc-xyz123 \
    --target-cluster-rest-endpoint https://lkc-xyz123.eu-west-3.aws.private.confluent.cloud:443 \
    --target-cluster-link-name example-cluster-link-name
```

**Output:**
The command creates a `migrate_topics` directory containing Terraform to migrate the topics through the established Confluent Cloud cluster link.

> [!NOTE]
> The command generates a `confluent_kafka_mirror_topic` Terraform resource per topic. If you prefer to finegrain this and only migrate some topics, you may manually edit the Terraform or use the [kcp UI](#kcp-ui) and following the 'Migration Scripts > Mirror Topics' wizard.

---

#### `kcp create-asset reverse-proxy`

> [!NOTE]
> This command is currently hidden while the reverse proxy is being reworked.

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

### `kcp migration`

The `kcp migration` command provides tools for executing end-to-end Kafka migrations from MSK to Confluent Cloud using CPC Gateway. It includes the following sub-commands:

- `init`
- `execute`
- `status`
- `list`

The migration workflow follows a defined lifecycle managed by a finite state machine:

1. **Initialize** — validate cluster link and gateway CRs, persist migration config
2. **Check Lags** — compare source and destination offsets until lag is below threshold
3. **Fence Gateway** — apply fenced gateway CR to block traffic during cutover
4. **Promote Topics** — promote mirror topics at zero lag
5. **Switch Gateway** — apply switchover gateway CR to route traffic to Confluent Cloud

If execution is interrupted at any step, re-running `kcp migration execute` resumes from the last completed step.

---

#### `kcp migration init`

Initialize a new migration by validating infrastructure and persisting migration state. This command validates the cluster link and mirror topics on the destination cluster, fetches the current gateway CR from Kubernetes, validates consistency across the gateway CRs, and writes the migration configuration to the state file.

**Required Arguments**:

- `--k8s-namespace`: Kubernetes namespace where the gateway is deployed.
- `--initial-cr-name`: Name of the initial gateway custom resource in Kubernetes.
- `--source-cluster-arn`: ARN of the source MSK cluster.
- `--cluster-id`: Confluent Cloud destination cluster ID (e.g. `lkc-abc123`).
- `--cluster-rest-endpoint`: REST endpoint of the destination Confluent Cloud cluster.
- `--cluster-link-name`: Name of the cluster link on the destination cluster.
- `--cluster-api-key`: API key for authenticating with the destination cluster.
- `--cluster-api-secret`: API secret for authenticating with the destination cluster.
- `--fenced-cr-yaml`: Path to the gateway CR YAML that blocks traffic during migration.
- `--switchover-cr-yaml`: Path to the gateway CR YAML that routes traffic to Confluent Cloud.

**Source Cluster Authentication Flags** (mutually exclusive):

- `--use-sasl-iam`: Use IAM authentication for the source MSK cluster.
- `--use-sasl-scram`: Use SASL/SCRAM authentication for the source MSK cluster.
- `--use-tls`: Use TLS authentication for the source MSK cluster.
- `--use-unauthenticated-tls`: Use unauthenticated (TLS encryption) for the source MSK cluster.
- `--use-unauthenticated-plaintext`: Use unauthenticated (plaintext) for the source MSK cluster.

**SASL/SCRAM Flags** (required when `--use-sasl-scram`):

- `--sasl-scram-username`: SASL/SCRAM username for the source MSK cluster.
- `--sasl-scram-password`: SASL/SCRAM password for the source MSK cluster.

**TLS Flags** (required when `--use-tls`):

- `--tls-ca-cert`: Path to the TLS CA certificate for the source MSK cluster.
- `--tls-client-cert`: Path to the TLS client certificate for the source MSK cluster.
- `--tls-client-key`: Path to the TLS client key for the source MSK cluster.

**Optional Arguments**:

- `--migration-state-file`: Path to the migration state file (default: `migration-state.json`).
- `--skip-validate`: Skip infrastructure validation. Creates migration metadata without validating gateway/Kubernetes resources.
- `--kube-path`: Path to the Kubernetes config file (default: `~/.kube/config`).
- `--topics`: Topics to migrate (comma separated list or repeated flag).

**Example Usage**

```shell
kcp migration init \
  --k8s-namespace my-namespace \
  --initial-cr-name my-gateway \
  --source-cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc123 \
  --cluster-id lkc-abc123 \
  --cluster-rest-endpoint https://lkc-abc123.us-east-1.aws.confluent.cloud:443 \
  --cluster-link-name my-cluster-link \
  --cluster-api-key ABCDEFGHIJKLMNOP \
  --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  --fenced-cr-yaml gateway-fenced.yaml \
  --switchover-cr-yaml gateway-switchover.yaml \
  --use-sasl-iam
```

> [!NOTE]
> All flags can be provided via environment variables using uppercase names with underscores (e.g. `CLUSTER_API_KEY`).

---

#### `kcp migration execute`

Execute an initialized migration through its remaining workflow steps. This command resumes a migration from its current state, progressing through lag checking, gateway fencing, topic promotion, and gateway switchover.

**Required Arguments**:

- `--migration-id`: ID of the migration to execute (from `kcp migration list`).
- `--lag-threshold`: Total topic replication lag threshold (sum of all partition lags) before proceeding with migration.
- `--cluster-api-key`: API key for authenticating with the destination cluster.
- `--cluster-api-secret`: API secret for authenticating with the destination cluster.
- `--source-cluster-arn`: ARN of the source MSK cluster.
- `--cc-bootstrap`: Confluent Cloud Kafka bootstrap endpoint.

**Source Cluster Authentication Flags** (mutually exclusive):

- `--use-sasl-iam`: Use IAM authentication for the source MSK cluster.
- `--use-sasl-scram`: Use SASL/SCRAM authentication for the source MSK cluster.
- `--use-tls`: Use TLS authentication for the source MSK cluster.
- `--use-unauthenticated-tls`: Use unauthenticated (TLS encryption) for the source MSK cluster.
- `--use-unauthenticated-plaintext`: Use unauthenticated (plaintext) for the source MSK cluster.

**SASL/SCRAM Flags** (required when `--use-sasl-scram`):

- `--sasl-scram-username`: SASL/SCRAM username for the source MSK cluster.
- `--sasl-scram-password`: SASL/SCRAM password for the source MSK cluster.

**TLS Flags** (required when `--use-tls`):

- `--tls-ca-cert`: Path to the TLS CA certificate for the source MSK cluster.
- `--tls-client-cert`: Path to the TLS client certificate for the source MSK cluster.
- `--tls-client-key`: Path to the TLS client key for the source MSK cluster.

**Optional Arguments**:

- `--migration-state-file`: Path to the migration state file (default: `migration-state.json`).

**Example Usage**

```shell
kcp migration execute \
  --migration-id migration-a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
  --lag-threshold 0 \
  --cluster-api-key ABCDEFGHIJKLMNOP \
  --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  --source-cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc123 \
  --cc-bootstrap pkc-abc123.us-east-1.aws.confluent.cloud:9092 \
  --use-sasl-iam
```

> [!NOTE]
> Credentials (`--cluster-api-key`, `--cluster-api-secret`) are intentionally not stored in the state file and must be provided each time for security.

---

#### `kcp migration lag-check`

Interactive TUI that displays mirror topic lag for the cluster link. This is a standalone monitoring tool that does not require a prior `kcp migration init`. It uses the Cluster Link REST API to query mirror topic lag.

> [!NOTE]
> This command uses the **Cluster Link REST API** to retrieve mirror topic lag reported by the cluster link itself. In contrast, `kcp migration execute` uses a **direct offset comparison** between the source (MSK) and destination (Confluent Cloud) Kafka clusters to determine lag during the migration workflow.

**Required Arguments**:

- `--rest-endpoint`: Cluster link REST endpoint.
- `--cluster-id`: Cluster link cluster ID.
- `--cluster-link-name`: Cluster link name.
- `--cluster-api-key`: Cluster link API key.
- `--cluster-api-secret`: Cluster link API secret.

**Optional Arguments**:

- `--poll-interval`: Poll interval in seconds, between 1 and 60 (default: `1`).

**Example Usage**

```shell
kcp migration lag-check \
  --rest-endpoint https://lkc-abc123.us-east-1.aws.confluent.cloud:443 \
  --cluster-id lkc-abc123 \
  --cluster-link-name my-cluster-link \
  --cluster-api-key ABCDEFGHIJKLMNOP \
  --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

**Features**:

- **Mirror Topic Lag**: Displays per-topic lag reported by the cluster link with drill-down to partition level
- **Lag Trend Sparkline**: Visual sparkline showing lag history over time per topic
- **Auto-refresh**: Polls at configurable intervals with live updates
- **Keyboard Navigation**: Scroll through topics, toggle partition detail view (`p`), refresh (`r`), adjust interval (`+`/`-`)

---

#### `kcp migration list`

Display all migrations from the migration state file, showing migration IDs, current state, gateway configuration, and topics.

**Optional Arguments**:

- `--migration-state-file`: Path to the migration state file (default: `migration-state.json`).

**Example Usage**

```shell
# List all migrations from the default state file
kcp migration list

# List from a specific state file
kcp migration list --migration-state-file /path/to/state.json
```

---

### `kcp ui`

This command starts a web-based user interface for visualizing and analyzing your MSK cluster data. The UI provides an interactive dashboard for exploring costs, metrics, and cluster information from your `kcp-state.json` file.

**Optional Arguments**:

- `--port`, `-p`: Port to run the UI server on (default: 5556)

**Example Usage**

```shell
# Start UI on default port (5556)
kcp ui

# Start UI on custom port
kcp ui --port 8080
kcp ui -p 3000
```

**Features**:

- **Interactive Dashboard**: Web-based interface for exploring cluster data
- **State File Upload**: Upload and analyze your `kcp-state.json` file through the browser
- **Cost Analysis**: Visual cost reports and breakdowns by region
- **Metrics Visualization**: Interactive charts and graphs for cluster metrics
- **Cluster Reports**: Detailed cluster information and configuration analysis
- **TCO Calculator**: Total Cost of Ownership analysis and projections
- **Dark/Light Mode**: Modern UI with theme support

**Access**:
Once started, the UI will be available at `http://localhost:<port>` (default: `http://localhost:5556`). The command will display the exact URL when the server starts.

**Workflow**:

1. Run `kcp discover` to generate your `kcp-state.json` file
2. Start the UI with `kcp ui`
3. Upload your state file through the web interface
4. Explore costs, metrics, and cluster data visually

> [!NOTE]
> The UI runs locally and does not send your data to external servers. All analysis is performed on your local machine.

---

### `kcp update`

This command updates the kcp binary to the latest version by downloading the latest release from GitHub and installing it. The command automatically creates a backup of the current binary and can rollback on failure.

**Optional Arguments**:

- `--force`: Force update without user confirmation
- `--check-only`: Only check for updates, don't install

**Example Usage**

```shell
# Update to latest version (with confirmation prompt)
kcp update

# Force update without confirmation
kcp update --force

# Check for updates without installing
kcp update --check-only
```

**Behavior**:

- Automatically detects the current version and compares with the latest GitHub release
- With `--check-only`, reports available updates without installing
- Prompts for confirmation unless `--force` is used
- Skips update check for development versions unless `--force` is specified

> [!NOTE]
> **Permission Requirements**: If kcp is installed in a system directory (e.g., `/usr/local/bin`), the update command will check permissions early and exit with an error if sudo is required. In this case, re-run the command with sudo:
>
> ```shell
> sudo kcp update
> ```
>
> The error message will include the exact command to run, preserving any flags you used (e.g., `sudo kcp update --force`).

---
