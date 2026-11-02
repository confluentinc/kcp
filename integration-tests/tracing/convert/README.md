# Tracing — dynamic-to-static runs

The tracing smoke suite's third migration kind: **dynamic to static** (a route conversion,
`spec.dynamicToStaticConversion`). Proves each conversion produces one complete OpenTelemetry trace against a live
Confluent Gateway, cluster link and Kafka in minikube (profile `kcp-e2e-tracing-convert`).

The topology, setup and runner pod are a deliberate copy of `integration-tests/route-conversion`, trimmed to what a
clean, traced conversion needs, so that functional suite is untouched. Setup needs a CP Enterprise licence and AWS
credentials, like the other e2e suites.

## Running

```bash
GATEWAY_MODE=convert make test-tracing-setup     # once
GATEWAY_MODE=convert make test-tracing-run       # as often as you like
GATEWAY_MODE=convert make test-tracing-teardown
```

To watch the runs on the kcp tracing dashboard, `make otel-up` first and pass
`OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` to the run (see `internal/tracing/README.md`).

## The runs

Before each run the environment is reset to the state a finished dynamic migration leaves — every link mirror
promoted, the route sending every link topic to the destination, no consumer groups — and consumer groups are seeded
on the source for the conversion to copy. So conversions repeat without limit; nothing is used up.

| Run label | Consumer groups | Commit detection | Offset-sync workers |
|---|---|---|---|
| `dynamic-to-static-2-groups-detect-10s` | 2 | 10s | 8 (default) |
| `dynamic-to-static-6-groups-detect-15s` | 6 | 15s | 8 |
| `dynamic-to-static-12-groups-1-worker` | 12 | 10s | 1 |

The table is `conversionRuns` in `tracing_e2e_test.go`.

## What the test checks

For each run: the route is static on the destination afterwards and every seeded group's offsets are on the
destination; one trace for the whole run with the run's label; `migration.run` (`kcp.strategy=d2s`, the configured
workers and window) with the five steps in order — initialize, fence, verify_fence, sync_offsets, switch — and no
error on the command, the run or any step; the gateway patch and accept spans; the groupoffsets spans present
(snapshot, rogue-commit detection, destination high-water marks, apply); one `groupoffsets.open_client` per worker
under each snapshot and the apply; `groupoffsets.list_groups` twice on the source and once on the destination;
`d2s.gather_facts`, `d2s.check_link_scope` (with every seeded group in scope) and `d2s.check_split_brain` under
verify_fence; `wait.rogue_commit_window` configured to and lasting the manifest's window (no more than 1s over); one
`groupoffsets.apply` syncing every seeded group with the configured number of workers; and no attribute carrying a
group name.
