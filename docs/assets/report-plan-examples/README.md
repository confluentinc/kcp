# `kcp report plan` — example output

`kcp report plan` turns a scan into a migration plan. For each scanned cluster it
recommends a target cluster type, sizing, networking, authentication, and
data-migration approach, and surfaces the questions a scan can't answer. Every run
writes three artifacts: `plan.md` (the human-readable plan), `plan.json` (the same
plan as structured data), and `plan-inputs.yaml` (the answers file you edit in place
and re-run to refine the plan). The examples below show all three across the main
ways the command is run.

- **[`filled/`](filled/plan.md)** — scan **plus** a fully answered `plan-inputs.yaml`. The
  plan is ready: 2 clusters ready to migrate, 1 routed to a specialist.
- **[`first-run/`](first-run/plan.md)** — a scan with **no answers yet** (the seed
  `plan-inputs.yaml` from a first run). All 3 clusters need answers; the plan lists
  what to fill in.
- **[`no-scan/`](no-scan/plan.md)** — **no `--state-file`**, run as a pure questionnaire.
  The planner starts a single placeholder cluster, so every fact becomes a question.

## How these stay current

These examples are **generated from committed fixtures, not hand-edited**, so they
can't silently drift from what `kcp report plan` actually produces:

- **[`demo-scan.json`](demo-scan.json)** — the synthetic scan fixture they're built
  from (fake account `123456789012`, made-up cluster names — no real customer data).
- **[`filled-inputs.yaml`](filled-inputs.yaml)** — the answered `plan-inputs.yaml`
  seed used for the `filled/` example. `first-run/` and `no-scan/` start with no
  answers.

`TestReportPlanExamples_UpToDate` (in `internal/services/plan/`) regenerates all
three artifacts for each mode through the same library entrypoints the CLI uses,
normalizes the only nondeterministic bits (the `plan.md` `Generated …` timestamp and
the `plan.json` `generated_at`, both fixed to `<example>`), and fails if the result
differs from the committed files. It runs as part of `make test-go`.

**After any change that alters plan output, regenerate with one command:**

```
make examples
```

Paths in the samples are shown in relative form (`demo-scan.json`,
`plan-inputs.yaml`) so they're portable. The `Generated …` timestamp and
`generated_at` field carry the fixed `<example>` placeholder (not a real time) so the
committed samples are byte-stable — expect a real timestamp on your own runs.
