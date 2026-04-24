---
title: kcp report costs
---

## kcp report costs

Generate a report of costs for given region(s)

### Synopsis

Generate a report of costs for the given region(s) based on the data collected by `kcp discover`

```
kcp report costs [flags]
```

### Examples

```
  # Default: all regions in the state file for the last 31 days
  kcp report costs --state-file kcp-state.json

  # Specific regions
  kcp report costs --state-file kcp-state.json --region us-east-1 --region eu-west-3

  # Specific regions and date range (all three must be supplied together)
  kcp report costs --state-file kcp-state.json \
      --region us-east-1,eu-west-3 --start 2024-01-01 --end 2024-01-31
```

### Options

```
      --end string          exclusive end date for cost report (YYYY-MM-DD).  (Defaults to today).
  -h, --help                help for costs
      --region strings      The AWS region(s) to include in the report (comma separated list or repeated flag).  If not provided, all regions in the state file will be included.
      --start string        inclusive start date for cost report (YYYY-MM-DD).  (Defaults to 31 days prior to today)
      --state-file string   The path to the kcp state file where the MSK cluster discovery reports have been written to.
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
      "Action": ["ce:GetCostAndUsage"],
      "Resource": "*"
    }
  ]
}
```

### SEE ALSO

* [kcp report](index.md)	 - Generate a report of costs for given region(s)

