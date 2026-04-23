---
title: kcp scan clusters
---

## kcp scan clusters

Scan Kafka clusters using the Kafka Admin API

### Synopsis

Scans MSK or OSK clusters to discover topics, ACLs, and other metadata via Kafka Admin API

```
kcp scan clusters [flags]
```

### Examples

```
  # Scan an MSK cluster (credentials from kcp discover)
  kcp scan clusters --source-type msk --state-file kcp-state.json --credentials-file msk-credentials.yaml

  # Scan an OSK cluster (hand-authored credentials)
  kcp scan clusters --source-type osk --state-file kcp-state.json --credentials-file osk-credentials.yaml

  # OSK with live Jolokia metric collection
  kcp scan clusters --source-type osk --state-file kcp-state.json \
      --credentials-file osk-credentials.yaml \
      --metrics jolokia --metrics-duration 5m --metrics-interval 10s

  # OSK with historical Prometheus metrics
  kcp scan clusters --source-type osk --state-file kcp-state.json \
      --credentials-file osk-credentials.yaml \
      --metrics prometheus --metrics-range 30d
```

### Options

```
      --credentials-file string   Path to credentials file (msk-credentials.yaml or osk-credentials.yaml)
  -h, --help                      help for clusters
      --metrics string            Metrics collection source: 'jolokia' or 'prometheus' (OSK only)
      --metrics-duration string   Duration to poll Jolokia (e.g. 10m, 1h). Required with --metrics jolokia.
      --metrics-interval string   Polling interval for Jolokia (e.g. 10s, 30s). Default: 10s. (default "10s")
      --metrics-range string      Time range to query from Prometheus (e.g. 7d, 30d). Required with --metrics prometheus.
      --skip-acls                 Skip ACL discovery
      --skip-topics               Skip topic discovery
      --source-type string        Source type: 'msk' or 'osk' (required)
      --state-file string         Path to the KCP state file (default "kcp-state.json")
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### AWS IAM Permissions

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
      "Action": ["kafka-cluster:ReadData"],
      "Resource": [
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-configs",
        "arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/connect-status"
      ]
    }
  ]
}
```

### SEE ALSO

* [kcp scan](kcp_scan.md)	 - Scan AWS resources for migration planning

