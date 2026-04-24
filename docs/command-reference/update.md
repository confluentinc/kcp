---
title: kcp update
---

## kcp update

Update the kcp binary to the latest version

### Synopsis

Updates the kcp binary to the latest version by downloading latest release from github and installing

```
kcp update [flags]
```

### Examples

```
  # Check for updates (no install)
  kcp update --check-only

  # Update with confirmation prompt
  kcp update

  # Update without prompt
  kcp update --force

  # Update when kcp is installed in /usr/local/bin
  sudo kcp update
```

### Options

```
      --check-only   Only check for updates, don't install
      --force        Force update without user confirmation
  -h, --help         help for update
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp](index.md)	 - A CLI tool for kafka cluster planning and migration

