# Route conversion (d2s) — live E2E

Proves `spec.route.convertTo: static` end to end against a **live Confluent
Gateway** (dynamic route, hot reload), a real cluster link and real clients
(Minikube profile `kcp-e2e-route-conversion`): a dynamic route left by a
finished topic-based migration is converted to a static route on the
destination, with the consumer groups' committed offsets copied across.

Each test drives the real `kcp migration execute`, optionally stopping it with
the killpoint seams (`KCP_TEST_CANCEL_AFTER=<state>` for an abrupt exit after
a state, `KCP_TEST_FAIL_AT=<event>` for a step failure), inspects the live
world it leaves (gateway CR, mirror states, offsets on both clusters), and
re-runs the same manifest. The binary runs **inside the cluster** in the
`rc-runner` pod (image `cp-server`, so `kafka-verifiable-producer` and
`kafka-verifiable-consumer` are on its PATH); `run.sh` compiles the suite and
a linux `kcp`, streams them and the rendered credential files in with
`kubectl exec -i ... cat` (the image has no `tar`, so `kubectl cp` cannot be
used), and runs the suite there.

## The environment

`setup.sh` stands up a source CP Kafka (PLAINTEXT) and a destination CP Kafka
(SASL_SSL), the cluster link `rc-link` mirroring `rc-topic-001`…`006`
(`001` and `002` have 3 partitions) with `consumer.offset.sync.enable=false`,
`rc-orphan` on the source only, and the gateway `rc-gateway` (2 replicas)
whose route `rc-route` clients really use: TLS to the gateway, swap auth from
the gateway to the destination. It writes 300 records to every link topic,
promotes every mirror, and leaves the route in its post-TBM shape (every link
topic routed to the destination, coordination on the source, no fence).

The setup steps shared with `integration-tests/idempotent-fsm` (version pins,
minikube, images, operator, clusters, licence, client-tls, REST credential,
REST class, swap-auth secrets) live in `../lib/cp-e2e-env.sh`, and the cluster
manifests are idempotent-fsm's (`../idempotent-fsm/testdata/manifests`).

Every test starts with a reset: every link mirror promoted (including one an
earlier test added), the route back in its post-TBM shape over every link
topic (fresh `configId`, hot-reloaded on every pod), and every consumer group
deleted on both clusters.

## The tests

| File | Tests |
|---|---|
| `baseline_e2e_test.go` | `TestConversion_Baseline`: dry run, execute, offsets copied with metadata and leader epoch -1, re-run is nothing to do |
| `resume_e2e_test.go` | `TestResume_After{Initialized,Fenced,FenceVerified,OffsetsSynced,Switched}`: interrupted after each state, then resumed |
| `rollback_e2e_test.go` | failures at `verify_fence`, `sync_offsets` and `switch`, and at `fence` on a re-run after an interrupted fence |
| `refusal_e2e_test.go` | an unpromoted link topic, a group committing outside the link, a group live on the destination, a direct commit during the window, a refusal with kcp's fence already up |
| `continuous_e2e_test.go` | `TestContinuousClients_Conversion` and `TestContinuousClients_ResumeAfterOffsetsSynced`: producers and consumers through the gateway across the conversion |

The continuous-client tests assert **no missed record** (every acknowledged
value read at least once by each group) and **bounded re-reads**: a value read
twice must be either one first read within [onset-10s, onset+15s] of the
fence onset (its commit was blocked; at most one batch per manual-commit
member, or one auto-commit interval's worth) or one first read no earlier than
5s before the switch completed and re-read within 60s after it (at most
`max.poll.records` per member). The checker (`checker.go`) is a Go port of the
manual runs' `analyze.py`; it and the route inspection (`route.go`) are
untagged and unit-tested by `make test-go`.

## Tangible evidence

`run.sh` saves every run to `.reports/<date>-<time>/` (gitignored), whether
the tests pass or fail:

```
.reports/2026-10-09-101500/
├── run.log
└── TestContinuousClients_Conversion/
    ├── manifest.yaml
    ├── kcp-run-1-execute.log           ← kcp's raw output, per run
    ├── consumer-ct-manual-a.log/.err   ← the clients' JSON lines and log4j output
    ├── producer-rc-topic-001.log/.err
    ├── checker.txt                     ← the checker's verdict per group
    └── test.log
```

The other tests' folders hold `before.md` / `after*.md` (the route, every
mirror's state, the seeded groups' offsets on both clusters) and their kcp
logs.

## Running

Needs AWS credentials (ECR images and the CP Enterprise licence in AWS Secrets
Manager `kcp/e2e/gateway-license`) — locally, `assume` a nonprod-admin profile
first.

```bash
make test-route-conversion-setup                             # once (minutes)
make test-route-conversion-run RUN=TestConversion_Baseline   # one test, or omit RUN for all
make test-route-conversion-teardown
```

The whole suite takes 35-45 minutes after setup. The environment uses about
5 GiB of Docker memory at rest (its profile is capped at 12 GiB) and can be up
alongside both idempotent-fsm environments if Docker has the memory. CI runs
it as the Semaphore block `integration: route-conversion e2e`.

The env vars are named `KCP_RC_*`; the destination credential reaches the pod
only through the `tbm-rest-credentials` Secret, never a `kubectl exec` argv.
