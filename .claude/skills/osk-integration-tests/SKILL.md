---
name: osk-integration-tests
description: Use when running, debugging, or modifying OSK Kafka integration tests in integration-tests/osk-scan.
allowed-tools:
  - Read
  - Bash
  - Grep
  - Glob
paths:
  - "integration-tests/osk-scan/**"
---

# OSK Integration Tests

Canonical reference for the Docker Compose environment under `integration-tests/osk-scan/` that exercises `kcp scan clusters --source-type osk` across all supported auth methods and metrics backends. This skill is the new home for the variant table and credentials map; `CLAUDE.md` no longer carries them.

## Run

```bash
make test-osk-scan
```

This runs `setup.sh` → `run.sh` → `teardown.sh`. The setup script generates TLS certificates, brings up the compose stack, waits for brokers and Prometheus to be healthy, seeds Prometheus with synthetic data, and creates topics and ACLs. The run script invokes `kcp scan clusters` against each variant. Teardown brings the stack down and cleans volumes.

To run the Kafka Connect variant separately: `make test-kafka-connect` (uses `run-connect.sh` instead of `run.sh`).

## Variant table

| Test Variant | Kafka Port | Metrics Port | Auth Method |
|---|---|---|---|
| kafka-plaintext | 9092 | — | None |
| kafka-sasl | 9093 | — | SASL/SCRAM-SHA-256 |
| kafka-tls | 9094 | — | mTLS |
| kafka-sasl-ssl | 9095 | — | SASL/SCRAM + TLS |
| jmx-noauth | 9092 | 8778 (Jolokia) | None |
| jmx-auth | 9096 | 8779 (Jolokia) | Username/password |
| jmx-tls | 9097 | 8780 (Jolokia) | Username/password + TLS |
| prometheus-noauth | 9092 | 9290 (Prometheus) | None |
| prometheus-auth | 9092 | 9291 (Prometheus) | Basic auth |
| prometheus-tls | 9092 | 9292 (Prometheus) | Basic auth + TLS |

All Kafka environments have an authorizer enabled and are populated with 4 topics (`test-topic-1`, `test-topic-2`, `orders`, `events`) and 12 ACLs across 5 team principals. Prometheus environments are pre-seeded with 30 days of synthetic metrics.

## Credentials

User/password pairs:

| System | Username | Password |
|---|---|---|
| SASL Kafka | `kafkauser` | `kafkapass` |
| JMX Jolokia (auth + TLS variants) | `monitorUser` | `monitorPass` |
| Prometheus (auth + TLS variants) | `promuser` | `prompass` |

Per-variant credential YAML files live in `integration-tests/osk-scan/credentials/`:

```
kafka-plaintext.yaml      kafka-sasl.yaml         kafka-sasl-plain.yaml
kafka-sasl-ssl.yaml       kafka-tls.yaml
jmx-noauth.yaml           jmx-auth.yaml           jmx-tls.yaml
prometheus-noauth.yaml    prometheus-auth.yaml    prometheus-tls.yaml
```

TLS certificates are generated fresh by `generate-certs.sh` on each setup. The script writes `ca.pem`, `server.pem`/`server-key.pem`, `client.pem`/`client-key.pem`, and the keystore/truststore files used by Kafka and Prometheus.

## Layout

```
integration-tests/osk-scan/
  docker-compose.yml          # multi-listener KRaft Kafka + 2 JMX brokers + 3 Prometheus + producers/consumers
  kafka_server_jaas.conf      # SASL credentials for the Kafka brokers
  prometheus.yml              # base Prometheus scrape config
  configs/
    prometheus-web-auth.yml   # basic-auth web config for prometheus-auth variant
    prometheus-web-tls.yml    # TLS web config for prometheus-tls variant
  credentials/                # 11 YAML files (one per variant; see above)
  setup.sh                    # certs + compose up + topic/ACL seeding + Prometheus seed
  run.sh                      # invokes `kcp scan clusters` against each variant
  run-connect.sh              # invokes the Kafka Connect scan variant
  teardown.sh                 # compose down + volume cleanup
  generate-certs.sh           # generates TLS material under ./certs/
  seed-prometheus-data.sh     # populates Prometheus with 30 days of synthetic data
```

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Cannot start service ...: bind: address already in use` | Previous compose stack still running, or host process bound to 9092/9093/etc. | `cd integration-tests/osk-scan && ./teardown.sh` then check `lsof -i :9092` for stragglers. |
| TLS variants fail with `unknown authority` or `bad certificate` | Certs are stale (regenerated each setup; CA bundle in client config doesn't match) | Re-run `./setup.sh`; do not reuse `./certs/` from a prior run. |
| Prometheus variants return empty metrics | Prometheus seed data didn't load (containers came up out of order) | Wait for `seed-prometheus-data.sh` to finish in setup logs. If it failed, run it manually after compose is healthy. |
| `kcp scan clusters` fails with `connection refused` shortly after setup | Brokers not yet ready when `run.sh` starts | The setup script has health waits, but if a manual run, sleep ~15s before the first scan. |
| Authorizer denies admin operations | Test credentials don't match what the authorizer expects | Check the principal in `kafka_server_jaas.conf` matches `kafkauser`; verify the relevant ACL exists from `setup.sh` output. |
| `make test-osk-scan` hangs on teardown | Container exit ordering — Kafka shutdown can hold volumes open | `docker compose -f integration-tests/osk-scan/docker-compose.yml down -v --remove-orphans` to force. |

## Related Make targets

- `make test-schema-registry` — separate Schema Registry integration test environment under `integration-tests/schema-registry/`. Different compose stack, different credentials (`schemauser`/`schemapass`), different test topics.
- `make test-migration` — end-to-end migration test using Minikube + Confluent for Kubernetes. Tagged `e2e`, 15-minute timeout. Distinct from OSK scan tests.

## When modifying

- Adding a new variant: extend `docker-compose.yml`, add a credential YAML to `credentials/`, append a row to the variant table above, and add a scan invocation to `run.sh`.
- Changing credentials: update both the YAML file and `kafka_server_jaas.conf` (or the Prometheus web config) to keep them in sync.
- Regenerating certs: only `generate-certs.sh` should write under `./certs/`; the directory is gitignored.
