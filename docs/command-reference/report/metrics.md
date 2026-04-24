---
title: kcp report metrics
---

## kcp report metrics

Generate a report of metrics for given cluster(s)

### Synopsis

Generate a report of metrics for the given cluster(s) based on the data collected by `kcp discover` or `kcp scan clusters`

```
kcp report metrics [flags]
```

### Examples

```
  # All clusters (MSK and OSK) in the state file
  kcp report metrics --state-file kcp-state.json

  # Specific clusters (supports both MSK ARNs and OSK cluster IDs)
  kcp report metrics --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc123 \
      --cluster-id osk-prod-cluster

  # All MSK clusters only
  kcp report metrics --state-file kcp-state.json --source-type msk

  # All OSK clusters, custom date range
  kcp report metrics --state-file kcp-state.json --source-type osk \
      --start 2024-01-01 --end 2024-01-31
```

### Options

```
      --cluster-id strings   The cluster identifier(s) to include in the report (comma separated list or repeated flag). Accepts both MSK ARNs and OSK cluster IDs.
      --end string           exclusive end date for metrics report (YYYY-MM-DD).  (Defaults to today).
  -h, --help                 help for metrics
      --source-type string   Source type filter: 'msk' (MSK only) or 'osk' (OSK only). Returns all clusters from the specified source.
      --start string         inclusive start date for metrics report (YYYY-MM-DD).  (Defaults to 31 days prior to today)
      --state-file string    The path to the kcp state file where the MSK cluster discovery reports have been written to.
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### AWS IAM Permissions

Required only for MSK clusters (CloudWatch metrics). OSK metrics come from the state file (Jolokia/Prometheus).

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "cloudwatch:GetMetricData",
        "cloudwatch:GetMetricStatistics",
        "cloudwatch:ListMetrics"
      ],
      "Resource": "*"
    }
  ]
}
```

### SEE ALSO

* [kcp report](index.md)	 - Generate a report of costs for given region(s)

