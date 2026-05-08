# Change Log

## Unreleased

### Breaking changes

- `kcp scan clusters` no longer auto-discovers self-managed connectors by reading the `connect-configs` and `connect-status` topics. Run `kcp scan self-managed-connectors` (which queries the Kafka Connect REST API) to populate connector data in the state file. CI/automation that previously called only `kcp scan clusters` and consumed `self_managed_connectors` from state must add the explicit second command. Existing connectors written to state by the old path are preserved across `kcp scan clusters` re-scans (the merge layer is unchanged); only the *new-cluster* discovery path requires the explicit command.
- Removed `kafka-cluster:ReadData` on `connect-configs` / `connect-status` from the IAM policy printed by `kcp scan clusters --print-iam-policy`. If you used that policy to derive a least-privilege role, you can drop those statements.
- Removed `GetAllMessagesWithKeyFilter` and `GetConnectorStatusMessages` from the internal `KafkaAdmin` interface (`internal/client/kafka_admin.go`). Internal-only — no public API impact.

### Features and Fixes

- `kcp scan self-managed-connectors` now populates `connect_host` on each connector (from the Connect REST API status payload's `worker_id`), restoring the UI's per-Connect-host grouping.
- `kcp scan clusters` emits an info-level log line when it detects `connect-configs` / `connect-status` topics in the scanned cluster, pointing operators at the explicit `kcp scan self-managed-connectors` command.

## v0.0.0 - YYYY-MM-DD

### Features and Fixes

- Initial release
