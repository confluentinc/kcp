# TBM hot-reload E2E

Runs a real Topic-Based Migration over a **live dynamic-mode Confluent Gateway
with hot reload** and a real cluster link. The system under test is the
reconciliation engine `migplan.Reconcile`: the suite asserts it **refuses**
batches on known-bad conditions and **succeeds** several batches, applying the
engine's rendered fence/switchover route edits to the live gateway via hot reload
(no pod roll) and promoting cluster-link mirrors between batches. `execute-tbm` is
asserted only thinly (its FSM is currently a noop).

The suite runs on its own Minikube profile (`kcp-e2e-tbm`), separate from
`make test-migration` and `make test-migration-hot-reload`.

> **You need a CP Enterprise Gateway licence to run this suite at all — a purely
> local run included.** The gateway gates its config-file watcher on the licence,
> and CFK reports success without it, so an unlicensed run would pass every
> Kubernetes assertion while proving nothing. `setup.sh` refuses to start without a
> resolvable licence and fails fast if the gateway logs the trial-mode warning.

## How to run

```bash
make test-migration-tbm            # setup, run, teardown (teardown on EXIT)
make test-migration-tbm-setup      # provision the cluster only
make test-migration-tbm-run        # (re-)run the suite against a live cluster, no teardown
make test-migration-tbm-teardown   # destroy the profile + per-run artefacts

# Narrow the run to specific tests:
make test-migration-tbm-run GOTEST_FLAGS='-test.run TestHalt'
# or positionally: bash integration-tests/migration-tbm/run.sh 'TestHalt'
```

`setup.sh` writes `.env` and `.rendered/` (both gitignored). `run.sh` cross-compiles
the e2e test binary and a linux `kcp` binary for the node architecture, copies them
plus every rendered manifest into the already-deployed `kcp-runner` pod, and execs
the test binary in-cluster.

## Prerequisites

On `PATH`: `minikube`, `kubectl`, `helm`, `docker`, `openssl`, `keytool` (JDK), and
either `aws` (default licence/ECR path) or `gcloud` (CI licence path). Go is fetched
via `GOTOOLCHAIN=auto` against the version pinned in `.go-version`.

- **CP Enterprise licence** — resolved by `setup.sh` in precedence order:
  1. `GATEWAY_LICENSE_KEY` (the JWT itself), else
  2. `GATEWAY_LICENSE_GCP_SECRET` (GCP Secret Manager name — the CI path over
     Semaphore OIDC workload identity), else
  3. AWS Secrets Manager secret `kcp/e2e/gateway-license` (authenticate first, e.g.
     `sso`). Override the id with `LICENSE_SECRET_ID`.
  The JWT is piped into a k8s Secret — never on argv, disk, or logs.
- **ECR access** for the private CFK chart + operator + gateway images. Locally an
  ambient AWS profile is used; in CI set `ECR_AWS_KEY_GCP_SECRET` +
  `ECR_AWS_SECRET_GCP_SECRET` to load static ECR creds from GCP Secret Manager.

### Image / chart env knobs (all defaulted in `setup.sh`)

| Var | Default | Notes |
|---|---|---|
| `CFK_CHART_OCI` | `oci://635910096382.dkr.ecr.us-east-1.amazonaws.com/kcp/confluent-for-kubernetes` | ECR mirror |
| `CFK_CHART_VERSION` | `0.1838.0` | First chart with **both** the dynamic-route CRD and `spec.configId` |
| `OPERATOR_IMAGE` | `635910096382.../kcp/confluent-operator:v0.1838.0-amd64` | amd64-only; side-loaded and emulated on arm64 nodes |
| `GATEWAY_IMAGE` / `GATEWAY_TAG` | `.../kcp/cpc-gateway:1.4.0-master-430-arm64` (arm64) / `...-424-amd64` | Proven dynamic-routing build; native-arch |
| `INIT_IMAGE` | `confluentinc/confluent-init-container:3.3.0` | Matches CFK 3.3.x |
| `CP_SERVER_TAG` | `8.1.2` | Source + destination brokers |

`0.1718.34` (the `migration-hot-reload` pin) is **not** reused: it predates the
dynamic-route CRD. `0.1838.0` is the only pin supporting both dynamic routing and
`spec.configId`; a CRD guard in `setup.sh` fails fast if that ever changes.

## Topic partition (single source of truth in `setup.sh`)

| Range | Mirrored? | Role |
|-------|-----------|------|
| `tbm-topic-001`–`044` | yes | success batches 01–04 (11 topics each) |
| `tbm-topic-045` | yes | reserved — promoted-but-not-switched halt (never in a success batch) |
| `tbm-topic-046`–`048` | yes | mirrored headroom |
| `tbm-topic-049`–`055` | no | un-mirrored halt input ("not on the cluster link") |
| `tbm-dest-only` | n/a (destination only) | "exists on target but is not a mirror" halt input |

Totals: **55 source topics / 48 mirrored on the link / 1 destination-only**. Counts
are shell variables (`SOURCE_TOPIC_COUNT`, `MIRRORED_COUNT`, `SUCCESS_HI`,
`RESERVED_TOPIC`) mirrored into `.env` so the Go tests read the same boundaries.

## Security posture

- Destination SASL password: delivered to the runner **only** via the
  `tbm-rest-credentials` Secret → pod-spec env (`KCP_TBM_DEST_SASL_USER`,
  `KCP_TBM_DEST_SASL_PASSWORD`), and inside the rendered batch manifests (in-pod
  files). It is never placed on a `kubectl exec` argv (apiserver audit-log leak).
- Batch/halt manifests are committed credential-free under `testdata/batches/*.tmpl`;
  the throwaway SASL is injected only at render time into `.rendered/` (gitignored).
- Generated CA / JKS / cert material and `.env` live under gitignored paths; no
  secret bytes are committed. Test credentials are synthetic throwaways.

## CI enablement (Semaphore) is a future task

This suite is **local opt-in only** — no `.semaphore/semaphore.yml` job is added.
Wiring it into CI needs the private operator image + non-public CFK chart access and
the GCP-federated licence/ECR credentials; the image/licence knobs above already
read from env/secret with the same precedence so a later CI task can inject them.
