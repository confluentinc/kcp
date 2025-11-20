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
    - [`kcp ui`](#kcp-ui)
    - [`kcp update`](#kcp-update)

# Getting Started

> [!NOTE]
> Currently, only migrations from AWS MSK are supported. Therefore, until later Apache Kafka migrations are supported, AWS MSK will be the reference point for the source of a migration.

## Installation

You can download kcp from GitHub under the [releases tab](https://github.com/confluentinc/kcp/releases/latest). We provide support for Linux and Darwin arm64/amd64 systems respectively.

The following reference workflow should work on most linux and darwin systems:


Set a variable for the latest release:
```
LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
```

Set a variable for your platform (comment and uncomment as appropriate):
```
PLATFORM=$(echo darwin_amd64)
# PLATFORM=$(echo darwin_arm64)
# PLATFORM=$(echo linux_amd64)
# PLATFORM=$(echo linux_arm64)
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

The kcp discover command performs a full discovery of all MSK clusters in an AWS account across multiple regions, together with their associated resources, costs and metrics.

**Example Usage**

`kcp discover --region us-east-1 --region eu-west-3`

or

`kcp discover --region us-east-1,eu-west-3`

The command will produce a cluster-credentials.yaml and a kcp-state.json file

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
        "kafkaconnect:DescribeConnector",
        "ec2:DescribeSubnets"
      ],
      "Resource": "*"
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
The command appends the gathered list of ACLs, topics and the Kafka cluster ID to each cluster's entries in the kcp-state.json file. If provided with sufficient permissions, kcp will also consume from the `connect-status` and `connect-configs` topics, if they exist, and gather self-managed connectors and their running state/configs.

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
- `--end`: Exclusive end date for cost report (YYYY-MM-DD)  (Defaults to today)

The above optional arguments are all required if one is supplied.  If none are supplied, a report generating costs for all regions present in the `state-file.json` for the last thirty full days will be generated.

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

- `--cluster-arn`: The AWS cluster ARN(s) to include in the report (comma separated list or repeated flag).  If not provided, all clusters in the state file will be included.
- `--start`: Inclusive start date for metrics report (YYYY-MM-DD).  (Defaults to 31 days prior to today)
- `--end`: Exclusive end date for cost report (YYYY-MM-DD).  (Defaults to today)

The above optional arguments are all required if one is supplied.  If none are supplied, a report generating metrics for all clusters present in the `state-file.json` for the last thirty full days will be generated.

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

- `msk`
- `self-managed`

> [!NOTE]
> This requires having provisioned a Confluent Cloud environment and cluster as well as having API keys with the `Cloud Resource Management` scope. This is due to using the `.../translate/config` Confluent API endpoint to convert self-managed connector configs to fully-managed configs.

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

#### `kcp create-asset migration-infra`

This command generates the required Terraform to provision your migration environment. The `--type` flag will determine how the Confluent Platform jump cluster with authenticate with MSK - using either IAM or SASL/SCRAM.

**Required Arguments**:

- `--state-file`: Path to kcp-state.json file
- `--cluster-arn`: The cluster-arn to target
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
  --state-file path/to/kcp-state.json \
  -- cluster-arn arn:aws:kafka:us-east-3:635910096382:cluster/my-cluster/7340266e-2cff-4480-b9b2-f60572a4c94c-2 \
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

**Example Usage**:

```shell
kcp create-asset migrate-topics \
  --state-file kcp-state.json \
  --cluster-arn arn:aws:kafka:us-east-3:635910096382:cluster/my-cluster/7340266e-2cff-4480-b9b2-f60572a4c94c-2 \
  --migration-infra-folder migration_infra
```

> [!NOTE]
> This command does not require AWS IAM permissions as it generates local scripts and configuration files. The mirror topics piggyback off the authentication link established in the cluster link.

**Output:**
The command creates a `migrate_topics` directory containing shell scripts:

- `msk-to-cp-mirror-topics.sh` - Individual `kafka-mirror` commands per topic to move data from MSK to the Confluent Platform jump cluster.
- `destination-cluster-properties` - Kafka client configuration file.
- `cp-to-cc-mirror-topics.sh` - Individual cURL requests to the Confluent Cloud API per topic move data from the Confluent Platform jump cluster to Confluent Cloud.

> [!NOTE]
> A `README.md` is generated in the `migrate_topics` directory to further assist in migrating the data from MSK to Confluent Cloud.

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
- Creates a backup of the current binary before updating
- Prompts for confirmation unless `--force` is used
- Automatically rolls back on update failure
- Skips update check for development versions unless `--force` is specified

> [!NOTE]
> This command may require sudo permissions to update the binary, depending on the installation location.

---
