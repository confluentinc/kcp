# migplan live integration tests

LIVE, local-only integration tests for the `migplan` migration reconciliation
engine (`internal/services/migplan`). They exercise the real provider seams and
the whole engine against two `cp-server` clusters and a real cluster link — no
mocks. Gated behind `//go:build e2e` so they never run in the normal suite.

Requires **Docker** (the compose env) and a Go toolchain. There is **no live
gateway** here: the engine reads routing rules from a static gateway-CR fixture
(`testdata/gateway.yaml`) while reaching the clusters + link through the live
providers. Applying the produced artifacts to a real TBR gateway is deferred.

## Environment

Two single-broker `confluentinc/cp-server` clusters, plaintext only (no
SASL/TLS, so no cert dependency), defined in `docker-compose.yml`:

| Cluster | Kafka (PLAINTEXT) | REST v3            | cluster_id               |
| ------- | ----------------- | ------------------ | ------------------------ |
| source  | `localhost:19092` | `http://localhost:18090` | `6ub6fPVJRzKjE4i-REkq-A` |
| dest    | `localhost:29092` | `http://localhost:28090` | `LKsbYRvfTM-TVXKjdjgdxA` |

- Source topics: `team-a.orders`, `team-a.payments`, `billing-v2`, `team-b.audit`.
- Cluster link `migplan-link` runs on **dest** (destination-initiated, reaches
  source over the docker network at `source:29092`).
- Mirror topics (all ACTIVE, keyed by source name): `team-a.orders`,
  `team-a.payments`, `billing-v2`.
- `team-b.audit` is on the source but **deliberately NOT mirrored** — the
  fail-fast (F3) case.
- Consumer offset sync is **not** enabled on the link.

## Run

```bash
./setup.sh                                              # up + provision (idempotent)
go test -tags e2e ./integration-tests/migplan/ -v      # from the repo root
./teardown.sh                                           # down -v (removes volumes)
```

`setup.sh` is safe to re-run: topic/link/mirror creation tolerate
already-exists, and it polls REST readiness and mirror status instead of
sleeping on fixed timers.

Quick manual liveness check (expects 3):

```bash
curl -s http://localhost:28090/kafka/v3/clusters/LKsbYRvfTM-TVXKjdjgdxA/links/migplan-link/mirrors \
  | grep -o '"mirror_status":"ACTIVE"' | wc -l
```

## What the tests assert

`providers_e2e_test.go`
- source topic lister returns the four source topics and not `__consumer_offsets`;
- target topic lister returns the three mirror topics (and not the un-mirrored `team-b.audit`);
- cluster-link status returns the three mirrors keyed by source name == ACTIVE,
  `team-b.audit` absent from the map, `OffsetSyncEnabled == false`.

`reconcile_e2e_test.go`
- happy path: the three ACTIVE-mirror topics all classify MIGRATABLE, artifacts
  list the three sorted topics, fence + switchover rules contain them, and
  switchover routes them to the target domain `cc`;
- fail-fast: `team-b.audit` (on source, not a mirror) refuses with nil artifacts
  and an F3 reason that references the cluster link.
