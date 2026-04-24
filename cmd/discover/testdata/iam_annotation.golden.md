The following policy covers a full run. If you pass `--skip-topics`, `--skip-costs`, or `--skip-metrics`, the corresponding statements can be omitted.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MSKScanPermissions",
      "Effect": "Allow",
      "Action": [
        "kafka:DescribeClusterV2",
        "kafka:DescribeConfigurationRevision",
        "kafka:DescribeReplicator",
        "kafka:GetBootstrapBrokers",
        "kafka:GetClusterPolicy",
        "kafka:GetCompatibleKafkaVersions",
        "kafka:ListClientVpcConnections",
        "kafka:ListClusterOperationsV2",
        "kafka:ListClustersV2",
        "kafka:ListConfigurations",
        "kafka:ListKafkaVersions",
        "kafka:ListNodes",
        "kafka:ListReplicators",
        "kafka:ListScramSecrets",
        "kafka:ListVpcConnections",
        "kafkaconnect:DescribeConnector",
        "kafkaconnect:ListConnectors"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKClusterConnect",
      "Effect": "Allow",
      "Action": [
        "kafka-cluster:Connect",
        "kafka-cluster:DescribeCluster"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKTopicActions",
      "Effect": "Allow",
      "Action": [
        "kafka-cluster:DescribeTopic",
        "kafka-cluster:DescribeTopicDynamicConfiguration",
        "kafka:DescribeTopic",
        "kafka:ListTopics"
      ],
      "Resource": "*"
    },
    {
      "Sid": "CostMetricsScanPermissions",
      "Effect": "Allow",
      "Action": [
        "ce:GetCostAndUsage",
        "cloudwatch:GetMetricData",
        "cloudwatch:GetMetricStatistics",
        "cloudwatch:ListMetrics"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKNetworkingScanPermission",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeSubnets"
      ],
      "Resource": "*"
    }
  ]
}
```
