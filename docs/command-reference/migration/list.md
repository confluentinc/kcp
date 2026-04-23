---
title: kcp migration list
---

## kcp migration list

List all migrations from the migration state file

### Synopsis

Display all migrations from the migration state file in a human-readable format, showing migration IDs, status, gateway configuration, and topics.

```
kcp migration list [flags]
```

### Examples

```
  # Default state file
  kcp migration list

  # Specific state file
  kcp migration list --migration-state-file /path/to/migration-state.json
```

### Options

```
  -h, --help                          help for list
      --migration-state-file string   The path to the migration state file to read. (default "migration-state.json")
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp migration](index.md)	 - Commands for migrating using CPC Gateway.

