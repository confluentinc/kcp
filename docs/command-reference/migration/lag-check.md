---
title: kcp migration lag-check
---

## kcp migration lag-check

Show mirror topic lag for the cluster link

### Synopsis

Interactive TUI that displays mirror topic lag for the cluster link. Run in a terminal with cluster link credentials. Press q to quit, p to toggle partition details, r to refresh, +/- to adjust interval, arrow keys to scroll.

All flags can be provided via environment variables (uppercase, with underscores).

```
kcp migration lag-check [flags]
```

### Examples

```
  kcp migration lag-check --rest-endpoint https://... --cluster-id lkc-xxx --cluster-link-name my-link --cluster-api-key xxx --cluster-api-secret xxx
```

### Options

```
      --cluster-api-key string      Cluster link API key
      --cluster-api-secret string   Cluster link API secret
      --cluster-id string           Cluster link cluster ID
      --cluster-link-name string    Cluster link name
  -h, --help                        help for lag-check
      --poll-interval int           Poll interval in seconds (1-60) (default 1)
      --rest-endpoint string        Cluster link REST endpoint
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp migration](index.md)	 - Commands for migrating using CPC Gateway.

