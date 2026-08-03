The following policy covers scanning MSK-managed (MSK Connect) connectors.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MSKConnectScanPermissions",
      "Effect": "Allow",
      "Action": [
        "kafkaconnect:DescribeConnector",
        "kafkaconnect:ListConnectors"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKConnectMetricsPermissions",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:GetMetricData",
        "cloudwatch:ListMetrics"
      ],
      "Resource": "*"
    }
  ]
}
```
