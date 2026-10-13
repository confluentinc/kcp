# Tracing — live smoke E2E

Proves that a clean, uninterrupted `kcp migration execute` produces **one complete OpenTelemetry trace** against a
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

`make test-tracing` does setup → run → teardown in one go. `GATEWAY_MODE=static` gives an AAO run instead.

## The trace

Each run is saved under `.reports/<date>-<time>-<mode>/` (gitignored). The test's folder holds the manifest it ran,
before/after reports, kcp's raw output and **`kcp-traces.jsonl`**, the trace kcp wrote. `internal/tracing/README.md`
has a `jq` recipe for reading it and the span reference.

To watch the run in Grafana as well, start the local backend first and pass the endpoint through:

```bash
make otel-up
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make test-tracing-run
```

`run.sh` rewrites `localhost` to `host.minikube.internal` for the pod. Open http://localhost:3000 → Explore → Tempo,
Service Name `kcp`. A trace file from any run can also be replayed into Grafana later (see `internal/tracing/README.md`).

## Topics

Each run migrates five topics whose mirrors are still ACTIVE, so re-runs on the same environment need no reset. When
fewer than five are left the test says so; tear the environment down and set it up again.

## What the test checks

- one trace for the whole run, rooted at the `kcp migration execute` span (resource `service.name=kcp`);
- `migplan.reconcile`, `migration.build_services` and `migration.run` (`kcp.strategy=tbm`) directly under it;
- the TBM steps in order: initialize, wait_for_lags, fence, verify_fence, promote, switch;
- the shared spans present: offset sweeps and per-broker offset reads, gateway patch/accept/verify, cluster-link
  list/promote, reconcile's topic, cluster-ID and link-status reads;
- no span with an error status, and no attribute carrying a topic name.
