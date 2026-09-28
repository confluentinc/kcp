# Idempotent migration FSM — live resume E2E

Proves the migration execution FSMs are **idempotent from any interruption
point**, against a **live Confluent Gateway** — dynamic-mode (hot reload) or
static-mode (rollout), per `GATEWAY_MODE` — and a real cluster link (Minikube
profile `kcp-e2e-idempotent`).

Each test drives the real `kcp migration execute` command, interrupts it at a
chosen checkpoint via the **killpoint seam** (`KCP_TEST_CANCEL_AFTER=<state>`, a
real context cancellation — the Ctrl-C path), inspects the genuine partial world
it leaves (live gateway CR + live mirror status), then re-runs the **same
manifest** and asserts it drives to completion. The uninterrupted baseline is
the control.

The binary runs **inside the cluster** (see `testdata/manifests/kcp-runner.yaml`):
reconcile reads the live gateway CR with in-cluster config, and each test execs
`/workspace/kcp` as a local subprocess. `run.sh` compiles the suite + the
current-branch `kcp` for the node arch, ships them plus the rendered manifests
into the runner pod, and runs the suite there.

## Tangible evidence

Every test emits, via the harness, into `go test -v` output: the live **world
state before**, the **full raw kcp output** of each run (never truncated; an
interrupted run shows its non-zero exit), and the **world state after** — the
gateway route's fence/routing rules, each topic's mirror status, and the cluster
link's `consumer.offset.sync.enable`. Nothing is sent to `/dev/null`.

`run.sh` saves every run to `.reports/<date>-<time>-<mode>/` (gitignored):

```
.reports/2026-09-28-101500-static/
├── run.log                          ← the whole run, including the helper checks
└── TestResume_InterruptAfterFence/  ← one folder per migration test
    ├── manifest.yaml                ← the manifest kcp ran with
    ├── before.md                    ← checkpoint reports: gateway route (full CR
    ├── after-interrupt.md           ←   collapsed), the test's mirror statuses, and
    ├── after-resume.md              ←   consumer.offset.sync.enable
    ├── kcp-run-1-interrupted.log    ← kcp's raw output, per run
    ├── kcp-run-2-resume.log
    └── test.log                     ← this test's slice of run.log
```

The baseline test's folder has `before.md`, `after.md`, `kcp-run-1-execute.log`,
`kcp-run-2-dry-run.log` and `kcp-run-3-rerun.log` (a re-run of the completed
migration, which finds nothing to do) instead. The folders are copied out of the runner pod
whether the tests pass or fail, so a failed test keeps everything it captured.

## Topic pool

`setup.sh` seeds `tbm-topic-001..085`, all mirrored. Promotion is irreversible,
so each kill-point test **reserves a disjoint 5-topic slice** (via `topicRange`)
in `036..085`: rollback on a mid-batch resume `036..040`, rollback on a resume
`041..045`, after-switch with the offset-sync pause on `046..050` (static
only), baseline `051..055`, after-fence `056..060`, after-promote
`061..065`, after-switch `066..070`, during-promote `071..075`,
after-offset-sync-pause `076..080` (static only), mid-batch `081..085`. On the dynamic
route these coexist in one env; on the static (whole-route) route each test first
resets the route to source (`resetStaticRoute`).

## Running

Needs AWS creds (ECR images + the CP Enterprise licence in AWS Secrets Manager
`kcp/e2e/gateway-license`) — locally, an ambient `sso`/nonprod-admin profile
covers both.

`GATEWAY_MODE` (default `dynamic`, or `static` for AAO) selects the route mode.
Make targets wrap the scripts:

```bash
# one-time (minutes): stand up the profile, gateway, link, topic pool
GATEWAY_MODE=dynamic make test-idempotent-fsm-setup

# run one test (or all)
make test-idempotent-fsm-run RUN=TestBaseline_FullMigrationCompletes

# when done
make test-idempotent-fsm-teardown
```

Every run prints its evidence and `run.sh` saves it to
`.reports/<date>-<time>-<mode>/` (see [Tangible evidence](#tangible-evidence)),
printing the path at the end.

## Status

New suite (2026-09-23), local on `feat/idempotent-migration-fsm`. Covers **both
routes** — dynamic/TBM and static/AAO (`GATEWAY_MODE=dynamic|static`) — across all
five kill-points: baseline, after-fence, after-promote, during-promote
(intra-promote / PENDING_STOPPED), after-switch — plus, on static,
after-offset-sync-pause and after-switch with the pause on, which leaves the
restore owed to the resume (`pauseConsumerOffsetSync` on; the static suite's
link starts with consumer offset sync enabled so the pause and restore are
observable). Both modes also cover a mid-batch kill: with
`promoteBatchSize: 2`, the run is killed after the first batch of 2 is
accepted, leaving 2 mirrors promoting and 3 ACTIVE. Two rollback tests
(`rollback_e2e_test.go`) fail the resume's `verify_fence` step with the test-only
failure hook (`KCP_TEST_FAIL_AT`): after a kill at the fence the rollback must
lift the interrupted run's fence; after a mid-batch kill it must keep the fence,
and a plain re-run then completes. Env-var names are still
`KCP_TBM_*` (inherited from the copied `migration-tbm` spine) pending a cosmetic
rename.
