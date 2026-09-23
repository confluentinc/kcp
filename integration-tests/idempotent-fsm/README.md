# Idempotent migration FSM — live resume E2E

Proves the migration execution FSMs are **idempotent from any interruption
point**, against a **live dynamic-mode Confluent Gateway with hot reload** and a
real cluster link (Minikube profile `kcp-e2e-idempotent`).

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

`setup.sh` seeds a pool (`tbm-topic-001..055`, `001..048` mirrored). Promotion is
irreversible, so each test **reserves a disjoint slice** (via `topicRange`) and
the suite is **one-shot per env standup** — re-running the whole suite means
`teardown.sh` + `setup.sh` to re-seed. Current reservations: baseline `001..011`,
interrupt-after-fence `023..033`.

## Running

Needs AWS creds (ECR images + the CP Enterprise licence in AWS Secrets Manager
`kcp/e2e/gateway-license`) — locally, an ambient `sso`/nonprod-admin profile
covers both.

```bash
# one-time (minutes): stand up the profile, gateway, link, topic pool
AWS_PROFILE=<profile> PROFILE=kcp-e2e-idempotent bash integration-tests/idempotent-fsm/setup.sh

# run one test (or all); output is the report
bash integration-tests/idempotent-fsm/run.sh 'TestBaseline_FullMigrationCompletes'

# when done
PROFILE=kcp-e2e-idempotent bash integration-tests/idempotent-fsm/teardown.sh
```

## Status

New suite (2026-09-23), local on `feat/idempotent-migration-fsm`. Currently
covers the dynamic/TBM route; the static/AAO route and the intra-promote
(PENDING_STOPPED) checkpoint are follow-ups. Env-var names are still `KCP_TBM_*`
(inherited from the copied `migration-tbm` spine) pending a cosmetic rename.
