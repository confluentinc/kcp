---
title: kcp scan client-inventory
---

## kcp scan client-inventory

Scan the broker logs for client activity

### Synopsis

Scan the broker logs in s3 to help identify clients that are using the cluster based on activity.

Prerequisites:
  - The source MSK cluster must be configured with trace logging (kafka.server.KafkaApis=TRACE) on each broker.
  - Broker logs must be delivered to S3.

```
kcp scan client-inventory [flags]
```

### Examples

```
  kcp scan client-inventory \
      --s3-uri s3://my-cluster-logs/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-xxxx-5/2025-08-13-14/ \
      --state-file kcp-state.json
```

### Options

```
  -h, --help                help for client-inventory
      --s3-uri string       The S3 URI to the broker logs folder (e.g., s3://my-bucket/kafka-logs/2025-08-04-06/)
      --state-file string   The path to the kcp state file where the client inventory reports will be written to.
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
      "Action": ["s3:GetObject", "s3:ListBucket"],
      "Resource": [
        "arn:aws:s3:::<BROKER_LOGS_BUCKET>",
        "arn:aws:s3:::<BROKER_LOGS_BUCKET>/*"
      ]
    }
  ]
}
```

### SEE ALSO

* [kcp scan](kcp_scan.md)	 - Scan AWS resources for migration planning

