# Retries — live E2E

Proves `kcp migration cutover execute` rides out a cluster disappearing from its side, against a
**live Confluent Gateway** (dynamic route, hot reload), a real cluster link, a source
Kafka and a destination Kafka (Minikube profile `kcp-e2e-retries`).

Each test drives the real `kcp migration cutover execute` from the runner pod and, timed to a step
kcp prints (for example "Verifying fence"), makes the source Kafka, the destination Kafka
or the destination's REST endpoint disappear from kcp's point of view. A **blip** shorter
than the retry budget (2 minutes) must be recovered: the run completes, the world is
migrated, and kcp logged the retry. An **outage** longer than the budget must fail safe:
kcp gives up, exits non-zero, leaves a consistent world, and a re-run completes.

This folder is self-contained. Nothing here sources or imports a file from another suite;
the environment script and the manifests are copies (see "Provenance").

## How the faults work

The runner pod carries `NET_ADMIN` and `iptables` (`testdata/manifests/kcp-runner.yaml`).
A fault is an iptables rule in the pod's own network namespace, keyed on the target pod's
IP and port, resolved when the fault starts. The clusters are never touched.

| Fault | Rule | What kcp sees |
|---|---|---|
| reset | `OUTPUT -d IP --dport P -j REJECT --reject-with tcp-reset` | new connections refused, open ones reset |
| blackhole | `OUTPUT -d IP --dport P -j DROP` | requests wait for kcp's own timeout |
| replies dropped | `INPUT -s IP --sport P ! --tcp-flags RST RST -j DROP` | requests arrive, answers never do |

"Replies dropped" followed by "reset" is a request the cluster accepted whose answer was
lost. The reset exemption matters: REJECT's own reset comes back through `INPUT` and would
otherwise be dropped too.

A TCP proxy was rejected: Kafka clients reconnect to the broker addresses the cluster
advertises and would skip a proxy on the bootstrap address.

Targets: source Kafka `:9071`, destination Kafka `:9071`, destination REST `:8090`. The
test environment has one broker per cluster, so one rule per target covers all of kcp's
connections to it.

## The tests

A blip is held until kcp has logged a retry, so the fault demonstrably reached a call kcp
retries, then kept 3 s longer and lifted. A fault timed to the banner of a step that
finishes in milliseconds (the lag wait, the zero-lag check) would land after it, so those
faults start during the fence, which takes seconds, and the test asserts which step kcp
retried in.

| Test | Fault | Window | Expected |
|---|---|---|---|
| `TestBlip_SourceResetsAtTheStartOfTheMigration` | source reset, from the start of the run | until kcp retries, +3 s | completes; `kafka.list_offsets` retried (the step hit is logged) |
| `TestBlip_SourceResetsDuringTheUnroutedProducerCheck` | source reset, during the check's 10 s wait | until kcp retries, +3 s | the retry is in the fence check; completes; fence not rolled back |
| `TestBlip_DestinationResetsBeforePromote` | destination reset, from the fence | until kcp retries, +3 s | the retry is in the promote step; completes |
| `TestBlip_RESTResetsBeforePromote` | REST reset, from the fence | until kcp retries, +3 s | the retry is in the promote step; completes |
| `TestBlip_PromoteIsAcceptedButTheReplyIsLost` | REST replies dropped from the fence, then reset after the request | about 4.5 s | completes; every topic STOPPED; `rest.promote` retried |
| `TestOutage_SourceGoneDuringTheUnroutedProducerCheck` | source reset | until kcp gives up | non-zero exit; fence removed; mirrors ACTIVE; re-run completes |
| `TestOutage_RESTGoneAfterPromotionStarted` | REST reset | until kcp gives up | non-zero exit; fence kept; re-run rolls forward |
| `TestStorm_EveryClusterResetsInPulses` | all three, 3 s on / 3 s off | whole run | completes |
| `TestLoginFailure_IsNotRetried` | wrong destination credentials | none | fails within a minute; no retry lines |

Each test migrates its own 5-topic slice (`retries-topic-001..055`), because promotion is
irreversible: **one run of the suite consumes the environment's topics**. Re-run on a fresh
environment (`teardown.sh`, then `setup.sh`). `setup.sh` creates 60 topics.

Every run checks the exit code, the retry lines in kcp's output (`connection error,
retrying`, with the operation name), that no blip ended in `giving up after connection
errors`, and the live world afterwards: mirror status over REST, and the route's fence and
routing from the live gateway CR.

## Running

Needs AWS credentials (the gateway licence is read from AWS Secrets Manager
`kcp/e2e/gateway-license`) or `GATEWAY_LICENSE_KEY` exported, plus `minikube`, `kubectl`, `helm`, `docker`, `openssl` and `keytool`. The
environment needs about 8–9 GiB of Docker memory.

```bash
./setup.sh                      # about 10 minutes the first time
./run.sh                        # the whole suite; the outage tests each take several minutes
./run.sh 'TestBlip_'            # a subset (a -test.run regex)
./teardown.sh
```

`run.sh` saves every run to `.reports/<date>-<time>/` (gitignored): `run.log`, and a folder
per test with the manifest it ran, kcp's raw output per run, the world before and after
(`before.md`, `after.md`), and `test.log`. It flushes the runner's iptables rules on exit,
so a failed run never leaves a cluster cut.

## Not covered

- Kubernetes API calls (the gateway CR) are not retried by kcp, so they are not tested.
- A break in the link between the source and the destination is not a kcp connection failure.
- Static routes (the offset-sync pause and restore) and the dynamic-to-static conversion:
  the first version covers the dynamic route only.

## Provenance

`env.sh` is a trimmed copy of the environment script the idempotent-fsm and route-conversion
suites share (`integration-tests/lib/cp-e2e-env.sh`), without the static-route swap-auth
step. The manifests under `testdata/manifests` are copies of idempotent-fsm's, with the
runner pod rewritten for fault injection and the secret and label names made this suite's
own. They were copied on 2026-10-29. When bumping version pins here, compare them with
those suites'; this folder will not follow them on its own.
