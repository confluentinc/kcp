Only required for `--source-type msk`. OSK scans use credentials from the credentials file, not AWS IAM.

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
      "Action": [
        "kafka-cluster:ReadData"
      ],
      "Resource": [
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-configs",
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-status"
      ]
    }
  ]
}
```
