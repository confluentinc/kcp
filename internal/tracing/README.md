# kcp tracing

kcp can record an OpenTelemetry **trace** of each command it runs: a tree of timed operations (spans) showing what the
command did and where the time went. It is built for measuring `kcp migration cutover execute` — every state-machine step, the
work inside each step, and any rollback — and is available to every command.

Tracing is **off** unless switched on. When off, nothing is created, written or sent.

These notes are for contributors; they are not published with the user docs.

## Switching it on

| Variable | Meaning |
|---|---|
| `KCP_TRACING_ENABLED` | Master switch. Tracing is on only when this parses as true (`true`, `1`, `TRUE`, …). |
| `KCP_TRACE_FILE` | Append spans to this file (OTLP/JSON lines). |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` / `OTEL_EXPORTER_OTLP_ENDPOINT` | Also send spans over OTLP/HTTP to this endpoint, and kcp's logs too (below). Headers, timeouts and TLS come from the standard `OTEL_EXPORTER_OTLP_*` variables. |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | Sends the logs somewhere other than the spans. Logs go only when spans go over OTLP. |
| `OTEL_EXPORTER_OTLP_(TRACES_)PROTOCOL` | Only `http/protobuf` is supported. |
| `OTEL_RESOURCE_ATTRIBUTES` | Added to every span's resource — use `run=<label>` to label runs (below). Leave `OTEL_SERVICE_NAME` unset: it replaces `service.name=kcp`, which the dashboard selects on. |

The file and OTLP are independent: set either or both. The rules, applied once at startup:

| Condition | Result |
|---|---|
| `KCP_TRACING_ENABLED` unset or false | Off. |
| `KCP_TRACING_ENABLED` not a boolean | Off, with a warning. |
| `KCP_TRACE_FILE` set and openable | Spans appended to it. |
| `KCP_TRACE_FILE` set but not openable | Spans appended to `./kcp-traces.jsonl` instead, with a warning. |
| OTLP endpoint set and a valid `http(s)` URL | Spans also sent over OTLP. |
| OTLP endpoint malformed, or another protocol | No OTLP, with a warning. |
| On, but no usable destination | Spans appended to `./kcp-traces.jsonl`. |
| …and that file can't be opened either | Off, with a warning. |

With spans going over OTLP, kcp's log records (every line `kcp.log` gets, Debug and above, including the migration
reporters' narrative) go to the same endpoint as OTLP logs, with the same resource as the spans:
`OTEL_RESOURCE_ATTRIBUTES` labels a run's logs and its trace alike. The trace file gets no logs. kcp logs without a
context, so each record carries the trace id of the command's root span: every line of a run opens that run's trace,
though not the span that was open when it was written. In `grafana/otel-lgtm`, Loki indexes only `service_name`; the
other resource attributes, `trace_id` and `span_id` are structured metadata, so `{service_name="kcp"} | run="perf-1"`
finds a run's lines.

Tracing never fails a command or changes its exit code. A collector that is unreachable mid-run produces warnings; the
spans it would have received are lost (the file, if configured, still has them).

A run that is killed (Ctrl-C, SIGKILL) loses the spans that were still open — the step it was in and the root span.
Spans already ended are in the file, because the file is written as each span ends.

## Viewing traces: the kcp tracing dashboard

`make otel-up` starts `grafana/otel-lgtm` (an OTel Collector, Tempo for traces, Prometheus and Grafana in one
container) with the **kcp tracing** dashboard already loaded. No login, no Explore, no queries to write.

```bash
make otel-up                       # prints the dashboard URL; Grafana is ready in ~30s
KCP_TRACING_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
  kcp migration cutover execute --migration-yaml migration.yaml
```

Open **http://localhost:3000/d/kcp-tracing**.

| Section | What it shows |
|---|---|
| **Runs** | *Traced commands*: every traced kcp command (any command, not only migrations), newest first, with its run label and duration. *Migration runs*: each migration with its strategy, migration name and topic count. **Click a command or a strategy** to load that run into the section below. |
| **Selected run** | The whole run's duration and how much of it was deliberate waiting (`wait.*`); the top-level phases (reconcile, service setup, the state machine); the full parent/child **timeline**; time per migration step in workflow order; each deliberate wait. |
| **Across all runs** | Mean time per step and per inner operation (gateway, offset, cluster-link, group offsets, reconcile, waits), and how often each ran, over every run since the backend started. |

The across-runs panels come from the span metrics Tempo derives in Prometheus, which carry span names but not the `run`
label. To compare labelled runs, read the duration and step bars of each run in turn.

Everything lives in the container: `make otel-down` stops it and discards the data.

### Running a batch of labelled runs

Label each run with `OTEL_RESOURCE_ATTRIBUTES` and run several; each appears as its own row under *Runs*. The smoke
suite does this for you: one pass runs five labelled migrations that vary topic count, detection window and promote
batch size (see `integration-tests/tracing/README.md`):

```bash
make otel-up
make test-tracing-setup      # once
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make test-tracing-run
```

For your own migrations, set `OTEL_RESOURCE_ATTRIBUTES=run=<label>` on each `kcp` command.

### Changing the dashboard

The dashboard is `internal/tracing/grafana/kcp-tracing.json`, loaded through `internal/tracing/grafana/dashboards.yaml`.
Edit it in Grafana, then *Export → Save to file* over the JSON in the repo (or edit the JSON directly) and
`make otel-down && make otel-up` to reload. Data sources are referenced by the UIDs `otel-lgtm` ships with: `tempo`
and `prometheus`.

## The trace file

One line per export, each a complete OTLP/JSON document (hex trace and span IDs, as the OTLP spec requires). The file is
appended to, never truncated; each run is its own trace ID.

Every span's name, start, end and parent:

```bash
jq -c '.resourceSpans[].scopeSpans[].spans[]
       | {name, start: .startTimeUnixNano, end: .endTimeUnixNano, parent: .parentSpanId}' kcp-traces.jsonl
```

## Replaying a file into a backend

A trace file can be loaded into any OTLP backend afterwards with the Collector's `otlpjsonfile` receiver. With
`make otel-up` running, put this in `replay.yaml` beside the trace file:

```yaml
receivers:
  otlpjsonfile:
    include: [/data/kcp-traces.jsonl]
    start_at: beginning
exporters:
  otlphttp:
    endpoint: http://host.docker.internal:4318
service:
  pipelines:
    traces:
      receivers: [otlpjsonfile]
      exporters: [otlphttp]
```

```bash
docker run --rm -v "$PWD/replay.yaml:/etc/otelcol-contrib/config.yaml" -v "$PWD:/data" \
  otel/opentelemetry-collector-contrib:0.161.0
```

Stop it with Ctrl-C once it logs `Started watching file` and a few seconds pass; the traces are then in Grafana.

## Labelling runs

```bash
OTEL_RESOURCE_ATTRIBUTES=run=600-topics-batch-50 KCP_TRACING_ENABLED=true kcp migration cutover execute ...
```

The label is on every span's resource, so runs can be told apart and compared in Tempo.

## Span reference

Names are `<area>.<operation>`; state-machine steps are `step.<event>`; waits kcp chooses to make are `wait.<reason>`, so
time spent waiting on purpose can be separated from time spent waiting on a cluster. kcp's own attributes are under
`kcp.*`. **Attributes never carry topic, group or pod names** — only counts, sizes, durations and modes. (A failed span
records kcp's error message, which may name topics, as the terminal output does.)

### Every `kcp migration cutover execute`

```
kcp migration cutover execute                       root span, one per command
├─ migplan.reconcile                        kcp.route.mode, kcp.refused, kcp.nothing_to_do, kcp.topics.migrate
│  └─ clusterlink.list_mirror_topics        (its link-status read)
├─ migration.build_services                 client setup
└─ migration.run                            kcp.strategy = aao | tbm | d2s, kcp.migration.id, the run's settings
   │                                        (lag threshold, batch size, detection window, workers), plan counts
   ├─ migration.started                     ends as it starts, with migration.run's opening attributes: reaches the
   │                                        backend at once, so a run can be listed while migration.run is still open
   ├─ step.<event> …                        kcp.step.from / kcp.step.to; kcp.step.skipped + kcp.step.skip_reason on pass-throughs
   └─ migration.rollback                    only when a failure rolls back; kcp.rollback.path
      └─ step.abort_fence                   when the rollback goes through the state machine
```

### Steps by strategy

| Strategy (`kcp.strategy`) | Steps |
|---|---|
| `aao` (static route) | initialize, wait_for_lags, fence, pause_offset_sync, verify_fence, promote, switch, restore_offset_sync |
| `tbm` (dynamic route) | initialize, wait_for_lags, fence, verify_fence, promote, switch |
| `d2s` (route conversion) | initialize, fence, verify_fence, sync_offsets, switch |

### Inside the steps

| Span | Where / attributes |
|---|---|
| `offset.sweep` | One per lag poll and promote iteration; children `offset.get_many` ×2 |
| `offset.get_many` | `kcp.cluster`, `kcp.topics`, `kcp.partitions`, `kcp.brokers`, `kcp.retried_partitions` |
| `offset.detect_unrouted_producers` | `kcp.topics`, `kcp.violations`; contains `wait.unrouted_producers_window` |
| `gateway.resolve_capability` | `kcp.gateway.mode`, `kcp.gateway.hot_reload`, `kcp.gateway.crd_supports_config_id` |
| `gateway.verify_hot_reload` | Only when the gateway hot-reloads; contains `gateway.wait_accepted`, `gateway.wait_config_applied` |
| `gateway.patch_cr` | `kcp.gateway.config_id_injected` |
| `gateway.wait_accepted`, `gateway.wait_config_applied` | — |
| `gateway.verify_transition` | `kcp.gateway.mechanism` = `config_id` \| `rollout`; contains `gateway.wait_config_applied` or `gateway.wait_ready` (→ `gateway.observe_rollout`, `kcp.gateway.rolled`) |
| `gateway.wait_pods` | Static fence with producer detection |
| every `gateway.*` verifier span | `kcp.gateway.step` = `fence`, `switchover`, `unfence` or `hot-reload check` |
| `clusterlink.*` | `promote_mirror_topics` (`kcp.batch.size`), `list_mirror_topics` (`kcp.mirrors`) |
| `step.wait_for_lags` | `kcp.polls`, `kcp.topics_behind` |
| `step.promote` | `kcp.polls`, `kcp.batches`, `kcp.promote_errors`; one `clusterlink.promote_mirror_topics` per batch; `wait.promote_poll` between polls |
| `wait.offset_sync_drain` | Static pause_offset_sync drain |
| `wait.retry_backoff` | Between two attempts of a call that failed on a connection error (see Retries) |
| `d2s.gather_facts` | Dynamic-to-static verify_fence: re-gathering the facts and re-checking the route and credentials |
| `groupoffsets.list_groups` | A consumer-group listing in verify_fence; `kcp.cluster` (`source` or `destination`), `kcp.groups` |
| `groupoffsets.snapshot` | `kcp.groups`, `kcp.groups.with_commits`, `kcp.workers`; one `groupoffsets.open_client` per worker |
| `groupoffsets.open_client` | One worker's client build, including its full metadata fetch and any retries; `kcp.worker`. A phase builds its workers one after another before handing out any group, so its connection cost is the sum of its `open_client` spans |
| `d2s.check_link_scope` | The link and group checks; `kcp.groups.in_scope` once the group rule passes (also on a later split-brain refusal) |
| `groupoffsets.detect_rogue_commits` | `kcp.changes`; contains `wait.rogue_commit_window`, `groupoffsets.list_groups`, `groupoffsets.snapshot` |
| `d2s.check_split_brain` | The second split-brain check, after the destination's `groupoffsets.list_groups` |
| `groupoffsets.destination_hwm`, `groupoffsets.apply` | Dynamic-to-static sync_offsets; `kcp.topics` / `kcp.groups`, `kcp.partitions`, `kcp.workers`; `groupoffsets.apply` has one `groupoffsets.open_client` per worker |

Every `wait.*` span carries `kcp.wait.configured_ms`.

### Retries

A call that fails on a connection error (reset, dropped, refused, timed out, or HTTP 429, 502, 503, 504) is tried
again with backoff: 500 ms, doubling, at most 10 s per wait, giving up rather than start a wait that would end more
than 2 minutes after the first failure (`internal/retry`). A call that succeeds first time records nothing.

| What | Where |
|---|---|
| `retry.attempt` event | On the span current when the call failed, once per failed attempt that is retried: `kcp.retry.operation`, `kcp.retry.attempt`, `kcp.retry.next_wait_ms`, `kcp.retry.error`; plus `kcp.retry.endpoint` on `rest.get` and `rest.put` (names replaced by `{cluster}`, `{link}`, `{topic}`, `{mirror}`) and `kcp.cluster` on `kafka.list_offsets` |
| `wait.retry_backoff` span | Each wait between attempts |
| `kcp.retry.attempts` | On the same span, when the call needed more than one attempt |
| `kcp.retry.gave_up` | `true` on the same span when the call ran out of budget |

Operations: `kafka.list_topics`, `kafka.cluster_id`, `kafka.list_groups`, `kafka.committed_offsets`,
`kafka.commit_offsets`, `kafka.probe_describe_group`, `kafka.probe_describe_topic`, `kafka.probe_commit`,
`kafka.partition_counts`, `kafka.open_client`, `kafka.list_offsets`, `rest.get`, `rest.put`, `rest.promote`.

`kcp.retry.error` is kcp's error message and may name a topic or group, as a failed span's error does. Each retry
also logs a warning (`⚠️ connection error, retrying`), and giving up logs `❌ giving up after connection errors`.

## Adding spans

A span earns its place when it is a state-machine step, a deliberate wait, or I/O that is (or could plausibly be)
slow. Cheap one-off reads stay inside their parent: the parent's duration still covers them, so no time goes
missing — only attribution inside it gets coarser. Add a span where a parent's unexplained time turns out to matter.

- Open and close spans with `tracing.Start(ctx, tracerName, "area.operation", attrs...)` and `tracing.End(span, err)`;
  annotate the current span with `tracing.SetAttributes`, mark a pass-through step with `tracing.MarkSkipped`, and make
  a deliberate wait with `tracing.Wait`.
- Each package declares `const tracerName = "kcp/<path below internal/>"`. Never cache a tracer in a package-level
  variable: tests install a fresh provider per test, and a cached tracer stays bound to the first one.
- Pass the `ctx` the span returned to the work inside it, so child spans nest.
- In tests, `tracingtest.Install(t)` records spans for one test (not with `t.Parallel()`), and
  `tracingtest.AssertNoValues(t, spans, names...)` fails if any attribute carries a topic or group name.

## The smoke suite

`integration-tests/tracing/` runs labelled static, dynamic and dynamic-to-static migrations (`GATEWAY_MODE=static`,
`dynamic` or `convert`) against a live gateway, cluster link and Kafka in minikube and checks each trace end to end.
See its README.
