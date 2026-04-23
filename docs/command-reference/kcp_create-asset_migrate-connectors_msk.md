---
title: kcp create-asset migrate-connectors msk
---

## kcp create-asset migrate-connectors msk

Migrate MSK Connect connectors to Confluent Cloud

### Synopsis

Generate Terraform configuration that recreates MSK Connect connectors as Confluent Cloud fully-managed connectors. Uses the Confluent translate/config API to convert connector configs.

```
kcp create-asset migrate-connectors msk [flags]
```

### Examples

```
  kcp create-asset migrate-connectors msk \
      --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --cc-environment-id env-a1bcde \
      --cc-cluster-id lkc-xyz123 \
      --cc-api-key ABCDEFGHIJKLMNOP \
      --cc-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

### Options

```
      --cc-api-key string          The API key for the Confluent Cloud cluster to migrate connectors to.
      --cc-api-secret string       The API secret for the Confluent Cloud cluster to migrate connectors to.
      --cc-cluster-id string       The ID of the Confluent Cloud cluster to migrate connectors to.
      --cc-environment-id string   The ID of the Confluent Cloud environment to migrate connectors to.
      --cluster-id string          The ARN of the MSK cluster.
  -h, --help                       help for msk
      --output-dir string          The directory where the Confluent Cloud Terraform connector assets will be written to
      --state-file string          The path to the kcp state file where the cluster discovery reports have been written to.
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### AWS IAM Permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "kafkaconnect:ListConnectors",
        "kafkaconnect:DescribeConnector",
        "kafkaconnect:DescribeWorkerConfiguration",
        "kafkaconnect:DescribeCustomPlugin"
      ],
      "Resource": "*"
    }
  ]
}
```

### SEE ALSO

* [kcp create-asset migrate-connectors](kcp_create-asset_migrate-connectors.md)	 - Migrate connectors to Confluent Cloud

