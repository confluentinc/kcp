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
in `001..100`. Each mode has its own environment, so a slice may be reused
across modes: drift refused (operator stopped
mid-switch) `001..005` (one test per mode), drift refused (operator stopped
mid-unfence) `006..010` (static only), a terminating gateway pod refused until
the gateway settles `091..095` (static only, never promotes), a switch a
trial-licence gateway never loads `091..095` (dynamic only), a resume after
every gateway pod restarts `096..100` (dynamic only), a CR change before a
rollback `096..100` (static only), rollback when a resume fails before its own fence step
`011..015`, a static route fenced by an operator is refused `016..020`
(static only), an operator's fence kept through a resumed migration `021..025`
and through a rollback `026..030` (dynamic only), unrouted-producer detection
on a mid-batch resume `031..035`,
rollback on a mid-batch resume `036..040`, rollback on a resume
`041..045`, after-switch with the offset-sync pause on `046..050` (static
only), baseline `051..055`, after-fence `056..060`, after-promote
`061..065`, after-switch `066..070`, during-promote `071..075`,
after-offset-sync-pause `076..080` (static only), mid-batch `081..085`, a CR
change before promote `086..090` (both modes). On the dynamic
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

`TestDrift_TBMStalledSwitchRerunIsRefused` (dynamic only) is the TBM form of
the stalled-switch test. It uses one run, held after promote (the hold seam,
below), because a new run's first gateway step is the hot-reload check, which
would stop a run started with the operator already down. The hot-reload check,
the fence and promote run with the operator up; the operator is scaled to 0
while the run is held. On release the switch patch lands on the CR and its
wait times out (`--rollout-timeout 45s`, `--hot-reload-timeout 45s`), with no
rollback past promote. The CR is switched and unfenced for the slice and
`status.observedGeneration` is behind `metadata.generation`. `--dry-run` and a
re-run both refuse with `the operator has not caught up with the CR`, and
neither moves the CR's generation. There is no data-plane probe: the dynamic
gateway's client listener needs TLS with SNI host routing.

`TestDrift_UnfenceStalledRerunIsRefused` (static only) is the reverse
direction: the CR says unfenced while the pods still serve the fence. A run is
killed after the fence, then the CFK operator is scaled to 0. The resumed run's
`verify_fence` step fails through the failure hook, nothing is promoted, so it
rolls back; the unfence lands on the CR but the operator never observes it, and
the run exits non-zero with `--rollout-timeout 45s`. The CR is unfenced,
`status.observedGeneration` is behind `metadata.generation`, and a producer
that was acked before the migration gets no acks through the gateway.
`--dry-run` and a re-run both refuse with `the operator has not caught up with
the CR`, and neither moves the CR's generation. The operator is scaled back up
when the test ends.

`TestDrift_TrialLicenceSwitchRerunIsRefused` (dynamic only) reaches the pod
check's configId comparison, which a stopped operator never does. A run is
killed after promote. The test then removes `GATEWAY_LICENSES` from the CR's
pod template with a JSON patch, which rolls the pods onto trial limits: they
load the current, fenced config at start, but never hot-reload a later one.
The test applies the switched route to the CR by hand, with a fresh
`spec.configId`, built with the reconcile core's own fence and routing
operations and written as TBM's switch writes it. The operator observes it
(`status.observedGeneration` equals `metadata.generation`), no pod rolls, and
for 30 seconds every pod's `GET /config` still reports the previous configId,
read through kcp's own pod check. `--dry-run` and a re-run both refuse with
the pod check's out-of-sync detail (`every gateway pod reports configId ...,
but the CR's spec.configId is ...`), and neither moves the CR's generation.
The licence entry is put back when the test ends, and the test waits for the
pods to roll and report the CR's configId. kcp's own switch can't create this
drift: its hot-reload check stops a trial-licence run before the switch.

A generation test (`generation_e2e_test.go`, both modes) covers kcp's check on
the gateway CR's `metadata.generation`. Run 1 is held after `fence_verified`
(the hold seam, below), and while it is held the test changes the CR's
`spec.configId` with a JSON patch, which moves the generation. Once released,
kcp's pre-promote check sees a generation it didn't write and stops with a
non-zero exit and no gateway write: the output says the CR changed and that
kcp is stopping with the fence applied and nothing promoted, the CR's
generation is still the change's, every topic is still fenced and every mirror
is still `ACTIVE`. A plain re-run then completes, and every mirror is
`STOPPED`.

A second generation test (static only, with the offset-sync pause on) covers
the write guard on a rollback. Run 1 sets both the failure hook on
`verify_fence` and the hold after `fenced`; while it is held, the test changes
the CR's `spec.configId`. Once released, kcp pauses offset sync, `verify_fence`
fails, and the rollback's unfence is refused because the generation is not the
one kcp's fence left. kcp exits non-zero: the output names the failure hook and
says kcp is stopping with no gateway write, with the fence applied and nothing
promoted. The CR's generation is still the change's, every topic is still
fenced, every mirror is still `ACTIVE`, and the link's consumer offset sync is
still paused (the restore is skipped). A plain re-run then completes, every
mirror is `STOPPED`, and offset sync is back on.

**The hold seam.** Beside the kill-point and failure hook, kcp has a test-only
hold: with `KCP_TEST_HOLD_AFTER=<state>` and `KCP_TEST_HOLD_FILE=<file>`, kcp
creates `<file>.held` after reaching that state and blocks until `<file>`
exists. `runKCPHeldAt` runs kcp that way, waits for `.held` (up to 5 minutes, or
until kcp exits first, which fails the test with kcp's output), runs the
test's step against the live world, then creates the release file.
`runKCPHeldAtWith` adds further environment, such as the failure hook. A
cleanup creates the release file too, so a failed test never leaves kcp blocked.

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
