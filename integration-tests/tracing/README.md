# Tracing — live smoke E2E

Proves that clean, uninterrupted `kcp migration execute` runs each produce **one complete OpenTelemetry trace** against a
live Confluent Gateway (dynamic route, hot reload), a real cluster link and Kafka in minikube (profile
`kcp-e2e-tracing-<mode>`). Unit tests cover the span trees of all three strategies; this suite checks the whole chain
in the real binary: environment variables read, spans written as each ends, flushed on exit, and the shared gateway,
offset and cluster-link spans present.

The topology, setup and runner pod are a deliberate copy of `integration-tests/idempotent-fsm` with its own minikube
profile, so that functional suite is untouched. Like it, setup needs a CP Enterprise licence and AWS credentials.

## Running

```bash
make test-tracing-setup        # once; GATEWAY_MODE=dynamic (default) → a TBM run
make test-tracing-run          # as often as you like (see "Topics" below)
make test-tracing-teardown
```

`make test-tracing` does setup → run → teardown in one go. `GATEWAY_MODE` picks the migration kind, each with its
own environment (minikube profile `kcp-e2e-tracing-<mode>`):

| `GATEWAY_MODE` | Migration kind | Suite |
|---|---|---|
| `dynamic` (default) | dynamic migration | this directory |
| `static` | static migration | this directory; the test expects the static steps |
| `convert` | dynamic-to-static migration | `convert/` (see its README) |

## The trace

Each run is saved under `.reports/<date>-<time>-<mode>/` (gitignored). The test's folder holds the manifest it ran,
before/after reports, kcp's raw output and **`kcp-traces.jsonl`**, the trace kcp wrote. `internal/tracing/README.md`
has a `jq` recipe for reading it and the span reference.

To watch the run in Grafana as well, start the local backend first and pass the endpoint through:

```bash
make otel-up
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make test-tracing-run
```

`run.sh` rewrites `localhost` to `host.minikube.internal` for the pod. The run then appears on the kcp tracing
dashboard at http://localhost:3000/d/kcp-tracing (see `internal/tracing/README.md`). A trace file from any run can also
be replayed into Grafana later.

## The runs

Each pass runs five migrations, each labelled (`OTEL_RESOURCE_ATTRIBUTES=run=<label>`) so it shows as its own row on the
dashboard, and varied so the runs differ:

| Run label (`<mode>` = `dynamic` or `static`) | Topics | Unrouted-producer detection | Promote batch size |
|---|---|---|---|
| `<mode>-3-topics` | 3 | off | all at once |
| `<mode>-5-topics-detect-10s` | 5 | 10s | all at once |
| `<mode>-8-topics-batch-2` | 8 | off | 2 |
| `<mode>-10-topics-detect-15s-batch-5` | 10 | 15s | 5 |
| `<mode>-12-topics-detect-15s` | 12 | 15s | all at once |

A pass takes about five minutes. The table is `tracedRuns` in `tracing_e2e_test.go`.

## Topics

Each run migrates topics whose mirrors are still ACTIVE, so re-runs on the same environment need no reset: a pass uses
38 of the 200-topic pool, so five passes fit in one setup. When too few are left the test says so; tear the
environment down and set it up again.

## What the test checks

For each run:

- one trace for the whole run, rooted at the `kcp migration execute` span (resource `service.name=kcp`, and the run's
  label set through `OTEL_RESOURCE_ATTRIBUTES`);
- `migplan.reconcile`, `migration.build_services` and `migration.run` directly under it, with `kcp.strategy` and the
  steps in order for the mode (TBM: initialize, wait_for_lags, fence, verify_fence, promote, switch);
- the shared spans present: offset sweeps and per-broker offset reads, gateway patch/accept/verify, cluster-link
  list/promote, reconcile's topic, cluster-ID and link-status reads;
- detection as configured: off, `step.verify_fence` is a skipped step (`detection_disabled`); on,
  `wait.unrouted_producers_window` records the manifest's window (`kcp.wait.configured_ms`) and actually lasted that
  long (no more than 1s over);
- one `promote_batch` event and one `clusterlink.promote_mirror_topics` call per promote batch;
- no error status on the command, the run or any step, and no attribute carrying a topic name.
