#!/usr/bin/env bash
# Stand up the Connect env: 1 plaintext broker + four single-node Connect workers
# (unauthenticated, HTTP-Basic, mTLS, and HTTPS+Basic), and create a test connector
# on each so the scanner has something to find. Each worker is its own Connect group
# (distinct group.id + internal topics), so they don't rebalance against each other.
# The Go test (connect_scan_test.go) assumes this ran.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

CONNECTOR_JSON='{
  "name": "test-heartbeat",
  "config": {
    "connector.class": "org.apache.kafka.connect.mirror.MirrorHeartbeatConnector",
    "tasks.max": "1",
    "source.cluster.alias": "source",
    "target.cluster.alias": "target",
    "source.cluster.bootstrap.servers": "connect-kafka:29092",
    "target.cluster.bootstrap.servers": "connect-kafka:29092"
  }
}'

# wait_rest <label> <base-url> [curl-auth-args...] — block until the Connect REST
# API is genuinely serving requests, not merely accepting a TCP connection.
#
# We poll GET /connectors and require a 2xx (curl -sf). Connect's listener accepts
# connections — and answers "/" — while the herder is still starting up and joining
# the group, a window in which /connectors 404s and a freshly-POSTed connector would
# too. Gating on /connectors is the real "herder is ready" signal, which removes the
# transient 404-on-create races seen in CI.
#
# --connect-timeout/--max-time bound each attempt so a half-open or mid-handshake
# listener fails that attempt in seconds; the `if` is what keeps `set -e` from
# aborting the whole script on an expected not-ready-yet failure. 90 attempts (~a
# few minutes) is generous headroom for a slow agent without ever hanging.
wait_rest() {
  local label="$1" url="$2"; shift 2
  echo "Waiting for Kafka Connect REST — $label ($url)..."
  for i in $(seq 1 90); do
    if curl -sf --connect-timeout 3 --max-time 5 "$@" "$url/connectors" > /dev/null 2>&1; then
      echo "  $label ready!"
      return 0
    fi
    [ "$i" = "90" ] && { echo "ERROR: $label REST did not become ready in time"; return 1; }
    sleep 2
  done
}

# create_connector <label> <base-url> [curl-auth-args...] — POST the test connector,
# retrying until it sticks.
#
# Uses `if curl -sf …; then`, never `code=$(curl …)`: under `set -euo pipefail` a
# command substitution that captures a non-zero curl exit (e.g. a --max-time timeout,
# exit 28) makes the assignment fail and aborts the whole script — which is exactly
# the `make ... Error 28` failure observed in CI, where a single slow POST killed the
# run instead of being retried. -sf turns any non-2xx (a herder still rebalancing can
# briefly 404/409) or transport error into a retry.
#
# The POST is also treated idempotently: if an attempt's response was lost to a
# timeout but the connector was in fact created, the follow-up GET catches it so we
# don't loop forever re-POSTing an existing connector.
create_connector() {
  local label="$1" url="$2"; shift 2
  echo "Creating test connector — $label..."
  for i in $(seq 1 30); do
    if curl -sf --connect-timeout 3 --max-time 10 "$@" -X POST "$url/connectors" \
         -H "Content-Type: application/json" -d "$CONNECTOR_JSON" > /dev/null 2>&1; then
      echo "  connector created on $label"
      return 0
    fi
    if curl -sf --connect-timeout 3 --max-time 5 "$@" "$url/connectors/test-heartbeat" > /dev/null 2>&1; then
      echo "  connector already present on $label"
      return 0
    fi
    [ "$i" = "30" ] && { echo "ERROR: failed to create connector on $label"; return 1; }
    echo "  $label not ready for connector create, retrying ($i/30)..."
    sleep 2
  done
}

echo "Generating mTLS certificates..."
bash generate-certs.sh

echo "Starting Connect env (broker + 4 Connect workers)..."
docker compose up -d

# ── Unauthenticated worker (:18083) ──────────────────────────────────────────
wait_rest "unauthenticated" "http://localhost:18083"
create_connector "unauthenticated" "http://localhost:18083"

# ── Basic-auth worker (:18085) ───────────────────────────────────────────────
BASIC_AUTH=(-u connectuser:connectpass)
wait_rest "basic-auth" "http://localhost:18085" "${BASIC_AUTH[@]}"
create_connector "basic-auth" "http://localhost:18085" "${BASIC_AUTH[@]}"

# ── mTLS worker (:18086, https) ──────────────────────────────────────────────
MTLS_AUTH=(--cacert certs/ca-cert.pem --cert certs/client-cert.pem --key certs/client-key.pem)
wait_rest "mtls" "https://localhost:18086" "${MTLS_AUTH[@]}"
create_connector "mtls" "https://localhost:18086" "${MTLS_AUTH[@]}"

# ── HTTPS + Basic-auth worker (:18087, https, server TLS, no client cert) ─────
BASIC_TLS_AUTH=(--cacert certs/ca-cert.pem -u connectuser:connectpass)
wait_rest "basic-tls" "https://localhost:18087" "${BASIC_TLS_AUTH[@]}"
create_connector "basic-tls" "https://localhost:18087" "${BASIC_TLS_AUTH[@]}"

# ── Jolokia on the unauthenticated worker (:18781) for the metrics subtest ────
echo "Waiting for Jolokia on the unauthenticated Connect worker (:18781)..."
for i in $(seq 1 30); do
  if curl -s --connect-timeout 3 --max-time 5 http://localhost:18781/jolokia/version > /dev/null 2>&1; then
    echo "  Jolokia ready!"
    break
  fi
  [ "$i" = "30" ] && echo "WARNING: Jolokia not ready in time; the metrics subtest may fail"
  sleep 2
done

echo "Connect env ready."
