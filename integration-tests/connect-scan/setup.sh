#!/usr/bin/env bash
# Stand up the Connect env: 1 plaintext broker + three single-node Connect workers
# (unauthenticated + HTTP-Basic + mTLS), and create a test connector on each so the
# scanner has something to find. The Go test (connect_scan_test.go) assumes this ran.
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

# wait_rest <label> <base-url> [curl-auth-args...] — poll the Connect REST root.
wait_rest() {
  local label="$1" url="$2"; shift 2
  echo "Waiting for Kafka Connect REST — $label ($url)..."
  for i in $(seq 1 60); do
    if curl -s "$@" "$url/" > /dev/null 2>&1; then
      echo "  $label ready!"
      return 0
    fi
    [ "$i" = "60" ] && { echo "ERROR: $label REST did not become ready in time"; return 1; }
    sleep 2
  done
}

# create_connector <label> <base-url> [curl-auth-args...] — POST the test connector, with retry.
create_connector() {
  local label="$1" url="$2"; shift 2
  echo "Creating test connector — $label..."
  for i in $(seq 1 30); do
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" "$@" -X POST "$url/connectors" \
      -H "Content-Type: application/json" -d "$CONNECTOR_JSON" 2>/dev/null)
    if [ "$code" = "201" ] || [ "$code" = "200" ]; then
      echo "  connector created on $label (HTTP $code)"
      return 0
    fi
    echo "  $label connector create returned HTTP $code, retrying ($i/30)..."
    sleep 2
  done
  echo "ERROR: failed to create connector on $label"
  return 1
}

echo "Generating mTLS certificates..."
bash generate-certs.sh

echo "Starting Connect env (broker + 3 Connect workers)..."
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
  if curl -s http://localhost:18781/jolokia/version > /dev/null 2>&1; then
    echo "  Jolokia ready!"
    break
  fi
  [ "$i" = "30" ] && echo "WARNING: Jolokia not ready in time; the metrics subtest may fail"
  sleep 2
done

echo "Connect env ready."
