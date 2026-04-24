---
title: kcp discover
---

## kcp discover

Multi-region, multi cluster discovery scan of AWS MSK

### Synopsis

Performs a full Discovery of all MSK clusters across multiple regions, and their associated resources, costs and metrics

```
kcp discover [flags]
```

### Examples

```
  # Scan a single region
  kcp discover --region us-east-1

  # Scan multiple regions (repeated flag or comma-separated)
  kcp discover --region us-east-1 --region eu-west-3
  kcp discover --region us-east-1,eu-west-3

  # Skip topic/cost/metric discovery for faster runs or reduced IAM scope
  kcp discover --region us-east-1 --skip-topics --skip-costs --skip-metrics
```

### Options

```
  -h, --help             help for discover
      --region strings   The AWS region(s) to scan (comma separated list or repeated flag)
      --skip-costs       Skips the cost discovery through the AWS Cost Explorer API
      --skip-metrics     Skips the metrics discovery through the AWS CloudWatch API
      --skip-topics      Skips the topic discovery through the AWS MSK API
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### AWS IAM Permissions

The following policy covers a full run. If you pass `--skip-topics`, `--skip-costs`, or `--skip-metrics`, the corresponding statements can be omitted.

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
      "Resource": "*"
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

### SEE ALSO

* [kcp](index.md)	 - A CLI tool for kafka cluster planning and migration

