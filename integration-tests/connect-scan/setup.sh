#!/usr/bin/env bash
# Stand up the minimal Connect env (1 plaintext broker + Connect worker with
# Jolokia), then create a test connector so the scanner has something to find.
# The Go test (connect_scan_test.go) assumes this has run.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "Starting Connect env (broker + Connect worker)..."
docker compose up -d

# ── Wait for the Connect REST API ────────────────────────────────────────────
echo "Waiting for Kafka Connect REST (:18083)..."
for i in $(seq 1 60); do
  if curl -s http://localhost:18083/ > /dev/null 2>&1; then
    echo "Kafka Connect is ready!"
    break
  fi
  [ "$i" = "60" ] && { echo "ERROR: Kafka Connect did not become ready in time"; exit 1; }
  sleep 2
done

# ── Create a test connector (retry: Connect finishes its rebalance after REST is up) ─
echo "Creating test connector..."
created=false
for i in $(seq 1 30); do
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:18083/connectors \
    -H "Content-Type: application/json" \
    -d '{
      "name": "test-heartbeat",
      "config": {
        "connector.class": "org.apache.kafka.connect.mirror.MirrorHeartbeatConnector",
        "tasks.max": "1",
        "source.cluster.alias": "source",
        "target.cluster.alias": "target",
        "source.cluster.bootstrap.servers": "connect-kafka:29092",
        "target.cluster.bootstrap.servers": "connect-kafka:29092"
      }
    }' 2>/dev/null)
  if [ "$code" = "201" ] || [ "$code" = "200" ]; then
    echo "Connector created (HTTP $code)"
    created=true
    break
  fi
  echo "  connector create returned HTTP $code, retrying ($i/30)..."
  sleep 2
done
[ "$created" = "true" ] || { echo "ERROR: failed to create connector"; curl -s http://localhost:18083/connectors; exit 1; }

# ── Wait for Jolokia on the Connect worker (:18781) ───────────────────────────
echo "Waiting for Jolokia on the Connect worker (:18781)..."
for i in $(seq 1 30); do
  if curl -s http://localhost:18781/jolokia/version > /dev/null 2>&1; then
    echo "Jolokia is ready!"
    break
  fi
  [ "$i" = "30" ] && echo "WARNING: Jolokia not ready in time; the metrics subtest may fail"
  sleep 2
done

echo "Connect env ready."
