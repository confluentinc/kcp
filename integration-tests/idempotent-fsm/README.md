# Idempotent migration FSM — live resume E2E

Proves the migration execution FSMs are **idempotent from any interruption
point**, against a **live Confluent Gateway** — dynamic-mode (hot reload) or
static-mode (rollout), per `GATEWAY_MODE` — and a real cluster link (Minikube
profile `kcp-e2e-idempotent-dynamic` or `kcp-e2e-idempotent-static`).

Each test drives the real `kcp migration cutover execute` command, interrupts it at a
chosen checkpoint via the **killpoint seam** (`KCP_TEST_CANCEL_AFTER=<state>`,
which stops the run right after that checkpoint, as an abrupt exit such as a
Ctrl-C would), inspects the genuine partial world
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

`setup.sh` seeds `tbm-topic-001..100`, all mirrored. Promotion is irreversible,
so each kill-point test **reserves a disjoint 5-topic slice** (via `topicRange`)
in `001..005`, `011..085` and `091..100`: drift refused (operator stopped
mid-switch) `001..005` (static only), a terminating gateway pod refused until
the gateway settles `091..095` (static only, never promotes), a resume after
every gateway pod restarts `096..100` (dynamic only), rollback when a resume fails before its own fence step
`011..015`, a static route fenced by an operator is refused `016..020`
(static only), an operator's fence kept through a resumed migration `021..025`
and through a rollback `026..030` (dynamic only), unrouted-producer detection
on a mid-batch resume `031..035`,
rollback on a mid-batch resume `036..040`, rollback on a resume
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

`GATEWAY_MODE` (default `dynamic`, or `static` for AAO) selects the route mode,
for setup, run and teardown alike. Each mode has its own Minikube profile
(`kcp-e2e-idempotent-<mode>`) and its own generated files (`.state/<mode>/`: the
`.env` run.sh reads and the rendered manifests), so a dynamic and a static
environment can be up, and run tests, at the same time. Make targets wrap the
scripts:

```bash
# one-time (minutes): stand up the profile, gateway, link, topic pool
GATEWAY_MODE=dynamic make test-idempotent-fsm-setup
GATEWAY_MODE=static  make test-idempotent-fsm-setup

# run one test (or all), in each mode — at the same time from two terminals if you like
GATEWAY_MODE=dynamic make test-idempotent-fsm-run RUN=TestBaseline_FullMigrationCompletes
GATEWAY_MODE=static  make test-idempotent-fsm-run RUN=TestBaseline_FullMigrationCompletes

# when done: each mode separately
GATEWAY_MODE=dynamic make test-idempotent-fsm-teardown
GATEWAY_MODE=static  make test-idempotent-fsm-teardown
```

Each environment uses about 4.5 GiB of Docker memory at rest (its Minikube
profile is capped at 12 GiB, `MINIKUBE_MEMORY`). Two at once need roughly 9 GiB
free in Docker Desktop's memory allowance on top of whatever else is running.

Every run prints its evidence and `run.sh` saves it to
`.reports/<date>-<time>-<mode>/` (see [Tangible evidence](#tangible-evidence)),
printing the path at the end.

## Coverage

CI runs the whole suite in both modes, as the two jobs of the Semaphore block
`integration: idempotent-fsm e2e`. It covers **both routes** — dynamic/TBM and
static/AAO (`GATEWAY_MODE=dynamic|static`) — across all
five kill-points: baseline, after-fence, after-promote, during-promote
(intra-promote / PENDING_STOPPED), after-switch — plus, on static,
after-offset-sync-pause and after-switch with the pause on, which leaves the
restore owed to the resume (`staticMigration.consumerOffsetSync.pause` on; the static suite's
link starts with consumer offset sync enabled so the pause and restore are
observable). Both modes also cover a mid-batch kill: with
`promoteBatchSize: 2`, the run is killed after the first batch of 2 is
accepted, leaving 2 mirrors promoting and 3 ACTIVE. Two rollback tests
(`rollback_e2e_test.go`) fail the resume's `verify_fence` step with the test-only
failure hook (`KCP_TEST_FAIL_AT`): after a kill at the fence the rollback must
lift the interrupted run's fence; after a mid-batch kill it must keep the fence,
and a plain re-run then completes. A third fails a resume at `wait_for_lags`,
before its own fence step: it must still lift the interrupted run's fence and,
on static, set consumer offset sync back to the baseline. A detection test
(`detection_e2e_test.go`) writes straight to the source of already-promoted topics during a mid-batch
resume; the fence check must catch it and keep the fence. Two operator-fence
tests (`operator_fence_e2e_test.go`, dynamic only) add an operator's own
produce-only fence over exactly the batch's topics to the live route: the fence
step, a resume, the switch and a rollback must all keep it, and remove only
kcp's fence. On static, a third (`TestOperatorFence_StaticRouteIsRefused`)
fences the route with the operator's own fence first: execute must refuse and
leave the route and the mirrors as they were.

A drift test (`drift_e2e_test.go`, static only) is design §8.1's regression
test. A run is killed after promote, then the CFK operator is scaled to 0. The
resumed run's switchover patch lands on the gateway CR but never reaches the
pods, so the switch times out, with no rollback past promote. The CR then says
switched while producers through the gateway are still fenced. kcp's pod check
sees the operator hasn't caught up with the CR (`status.observedGeneration` is
behind `metadata.generation`), so `--dry-run` and a re-run both refuse, and the
re-run makes no gateway write. Before the pod check, both reported nothing to
do and exited 0. The operator is scaled back up when the test ends. The
runner's Role grants `deployments/scale` for this test alone. A second drift
test, `TestDrift_TerminatingPodRefusesUntilSettled`, kills a run after the
fence and runs `--dry-run` straight away: the fence's rollout reports complete
while the replaced pod still terminates, so the pod check refuses with the
rollout-in-progress hint, and the same dry-run passes once the gateway settles.
A third, `TestDrift_GatewayPodRestartRerunProceeds` (dynamic only), kills a run
after promote, deletes every gateway pod and waits for the replacements. They
must report the CR's configId at boot, so `--dry-run` passes the pod check and
the re-run completes. The runner's Role grants `delete` on pods for this test
alone.

**The settle wait.** On a hot-reload-off (static) gateway, a rollout reports
complete while the replaced pod terminates for up to its grace period, and
kcp's pod check refuses in that window, by design. The resume tests are about
resuming, not timing, so before every kcp run the harness waits for the
gateway to settle (`waitForGatewaySettled`): the Deployment's rollout
complete and no `app=<gateway>` pod terminating. It doesn't wait for the
operator to observe the CR, so it returns while the operator is stopped. Only
the terminating-pod test skips it (`runKCPUnsettled`). It's a no-op on dynamic.

The env vars are named
`KCP_TBM_*`, as in the `migration-tbm` suite this one was built from.
