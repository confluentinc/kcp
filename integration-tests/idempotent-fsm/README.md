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

Every test emits, via the harness, into `go test -v` output (captured to
`.reports/<test>.log`): the live **world state before**, the **full raw kcp
output** of each run (never truncated; an interrupted run shows its non-zero
exit), and the **world state after** — the gateway route's fence/routing rules
and each topic's mirror status. Nothing is sent to `/dev/null`.

## Topic pool

`setup.sh` seeds `tbm-topic-001..075`, all mirrored. Promotion is irreversible,
so each kill-point test **reserves a disjoint 5-topic slice** (via `topicRange`)
in `051..075`: baseline `051..055`, after-fence `056..060`, after-promote
`061..065`, after-switch `066..070`, during-promote `071..075`. On the dynamic
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

# run one test (or all); output is the report
make test-idempotent-fsm-run RUN=TestBaseline_FullMigrationCompletes

# when done
make test-idempotent-fsm-teardown
```

## Status

New suite (2026-09-23), local on `feat/idempotent-migration-fsm`. Covers **both
routes** — dynamic/TBM and static/AAO (`GATEWAY_MODE=dynamic|static`) — across all
five kill-points: baseline, after-fence, after-promote, during-promote
(intra-promote / PENDING_STOPPED), after-switch. Env-var names are still
`KCP_TBM_*` (inherited from the copied `migration-tbm` spine) pending a cosmetic
rename.
