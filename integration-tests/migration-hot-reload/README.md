# Gateway hot-reload E2E

Proves, against a real licensed Confluent Gateway, that kcp confirms a gateway
transition by **config revision** rather than by pod turnover.

```bash
make test-migration-hot-reload            # setup, run, teardown
make test-migration-hot-reload-setup      # provision only
make test-migration-hot-reload-run        # re-run against a live cluster
make test-migration-hot-reload-teardown   # destroy

# Opt-in: measure the fence settle margin instead of just asserting it exists.
KCP_HR_SETTLE_ITERATIONS=5 make test-migration-hot-reload-run
```

## Why this is a separate suite

`make test-migration` pins CFK `0.1514.19`, whose Gateway CRD has neither
`spec.configId` nor `spec.hotReload`. On that cluster kcp correctly downgrades to
rollout verification, so the hot-reload path is never taken. That suite also
annotates its resources with `platform.confluent.io/block-reconcile` to stop the
operator re-asserting declared config over REST-set values — which here would
stop CFK processing a `configId` bump at all.

Either alone would make a hot-reload assertion pass while testing nothing, so
this suite gets its own Minikube profile (`kcp-e2e-hotreload`) and nothing in it
is annotated.

## Why every transition must hot-reload

Per CFK's rules an in-place route edit reloads in place, while adding or removing
a route, a streaming domain or a TLS secret rolls the pods. The rendered CRs
declare **both** streaming domains up front and change only the route between
transitions, so fence, switchover and rollback are all guaranteed hot reloads.

That is what lets the suite assert the strong thing: pod UIDs, container restart
counts and the Deployment's `metadata.generation` must all be unchanged across a
transition. A pod restart here is a failure, not an expected mechanism.

## The source broker, and what it buys

A single-node KRaft broker (`manifests/kafka-source.yaml`) backs the **source**
streaming domain, so the fence can be observed as a data-plane fact — writes stop
landing — and not merely as a converged `configId`. The **destination** domain is
deliberately left unroutable: no test produces through it, and a second broker
would cost node memory for no assertion.

The question it answers is specific. `detectUnroutedProducers` takes its first
offset snapshot the instant `WaitForGatewayConfigID` returns and aborts the
migration if the source's log end offset moves afterwards, with no tolerance — a
single message on any partition trips it. If the fence is not fully settled at
that instant, an in-flight write lands after snapshot 1 and the migration rolls
back for a rogue producer that does not exist.
`TestFenceStopsSourceWritesOnceConfigIDConverges` is that invariant. The test
binary already runs in-cluster, so it produces with sarama in-process rather than
coordinating a separate producer pod.

Keep the broker stupid. The moment it grows cluster links, mirror topics or
promotion, this stops being a gateway rig and becomes a second migration e2e.

### Two traps this rig has already hit

**A NotReady broker forges the fence.** A headless Service withdraws unready
endpoints from DNS; the gateway then cannot resolve its upstream and the producer
sees `BROKER_NOT_AVAILABLE` — indistinguishable from a working fence, in exactly
the measurement that matters. Hence `publishNotReadyAddresses: true`: Kubernetes
readiness must not be able to manufacture the signal under test.

**A JVM readiness probe causes the above.** `kafka-broker-api-versions` spawns a
full JVM inside a memory-capped container on every probe period; under contention
it times out and reports a perfectly healthy broker as NotReady. The probe is a
`tcpSocket` check for that reason, and the one-off `kafka-topics` call in
`setup.sh` overrides `KAFKA_HEAP_OPTS` so it does not claim the broker's own heap.

Related: `kafka-console-producer` exits `0` even when its send is rejected, so
assertions here are on offsets, never on client exit codes.

## Version pins — each is load-bearing

| Component | Pin | Why |
|---|---|---|
| CFK chart | `0.1718.34` (appVersion 3.3.0) | First chart whose Gateway CRD declares `spec.configId`. **The public Helm repo tops out at `0.1718.10`**, so the chart must come from a local checkout — set `CFK_CHART_PATH` |
| CFK operator image | `v0.1718.34-amd64` from internal ECR | Docker Hub's operator also stops at `0.1718.10`. Published amd64-only |
| Gateway | `confluent-gateway-for-cloud:1.3.0` | First release serving `GET /config`. 1.2.x has the licence hook but no endpoint. Multi-arch |
| Init container | `confluent-init-container:3.3.0` | Matches CFK 3.3.x. Multi-arch |
| Licence | CP **Enterprise**, non-expired | The gateway gates its config-file watcher on it |
| Source broker | `cp-kafka:7.6.0` | Not load-bearing. Matches the other integration suites so it is usually already cached |

## The licence

The gateway applies trial feature limits without an Enterprise licence, and then
**never starts its config-file watcher** — while CFK renders the new config,
projects it into every pod and reports `hot-reload-status=Succeeded`. No
Kubernetes or CFK signal shows the difference; only `GET /config` does. That
asymmetry is why kcp has `VerifyHotReloadCapability`, and why `setup.sh` greps the
gateway log for the trial-mode warning before declaring itself ready.

`setup.sh` takes the licence from `GATEWAY_LICENSE_KEY` if set, else from AWS
Secrets Manager at `kcp/e2e/gateway-license` (authenticate first). The JWT reaches
the cluster through a pipe into `kubectl create secret`: never on argv, never
written to disk, never logged. The rendered CRs reference the Secret by name only.

## Architecture note

The operator image is amd64-only, but the suite still runs on an arm64 host: the
image is pulled on the host with an explicit `--platform` and side-loaded, and the
node's runtime executes it under emulation (verified — the operator reports
`x86_64` and runs normally). The gateway itself is multi-arch and runs natively,
so only the operator is emulated.

Side-loading also keeps the ECR credential on the host instead of installing it
into the cluster as an `imagePullSecret`.

## Why the suite runs inside the cluster

kcp verifies a transition by dialling **each gateway pod's IP** on the `/config`
port. Pod IPs are not routable from the host under the Minikube docker driver, so
a host-side run could only test a port-forwarded stand-in — and the per-pod
distinction is precisely what is being tested. `run.sh` compiles the suite for the
node's architecture, ships it to a runner pod and executes it there.

The repeated `Neither --kubeconfig nor --master was specified` lines in the output
are client-go noting it fell back to the in-cluster service account. That is the
intended path here, not a warning about the run.

## Measured behaviour

On a 2-replica gateway, a `configId`-only apply converges on every pod in **~2s**,
with pod UIDs, restart counts and the Deployment generation all unchanged. kcp's
`DefaultHotReloadTimeout` of 90s is therefore very conservative.
