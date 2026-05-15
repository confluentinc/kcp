#!/bin/bash
# Run kcp scan self-managed-connectors against Kafka Connect.
# Assumes setup.sh has already been run (base Kafka cluster).

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$ROOT_DIR"

echo ""
echo "=========================================="
echo "  Kafka Connect Scan Test"
echo "=========================================="

# Start Kafka Connect with the profile
echo "Starting Kafka Connect..."
cd "$SCRIPT_DIR"
docker compose --profile connect up -d
cd "$ROOT_DIR"

# Wait for Connect to be ready
echo "Waiting for Kafka Connect to be ready..."
MAX_WAIT=60
WAIT_TIME=0

while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    if curl -s http://localhost:8083/ > /dev/null 2>&1; then
        echo "Kafka Connect is ready!"
        break
    fi
    echo "Connect not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 2
    WAIT_TIME=$((WAIT_TIME + 2))
done

if [ $WAIT_TIME -ge $MAX_WAIT ]; then
    echo "ERROR: Kafka Connect failed to start within $MAX_WAIT seconds"
    exit 1
fi

# Additional wait for Connect to fully initialize
echo "Waiting for Connect to fully initialize..."
sleep 5

# Create a state file for the test
STATE="test-state-kafka-connect.json"

# First, run a basic OSK scan to populate the state file with the cluster
echo "Populating state file with OSK cluster..."
./kcp scan clusters --source-type osk \
    --credentials-file integration-tests/osk-scan/credentials/kafka-plaintext.yaml \
    --state-file "$STATE"

# Now scan for self-managed connectors
echo ""
echo "Running: ./kcp scan self-managed-connectors --state-file $STATE --connect-rest-url http://localhost:8083 --cluster-id osk-kafka --use-unauthenticated"
./kcp scan self-managed-connectors \
    --state-file "$STATE" \
    --connect-rest-url http://localhost:8083 \
    --cluster-id osk-kafka \
    --use-unauthenticated

echo ""
echo "Results:"
TOPIC_COUNT=$(jq '.osk_sources.clusters[0].kafka_admin_client_information.topics.details | length' "$STATE")
ACL_COUNT=$(jq '.osk_sources.clusters[0].kafka_admin_client_information.acls | length' "$STATE")
CONNECTOR_COUNT=$(jq '.osk_sources.clusters[0].kafka_admin_client_information.self_managed_connectors.connectors | length // 0' "$STATE")
CONNECTOR_HOST_COUNT=$(jq '[.osk_sources.clusters[0].kafka_admin_client_information.self_managed_connectors.connectors[]? | select(.connect_host != null and .connect_host != "")] | length' "$STATE")
echo "  Topics: $TOPIC_COUNT, ACLs: $ACL_COUNT, Connectors: $CONNECTOR_COUNT (with connect_host populated: $CONNECTOR_HOST_COUNT)"

# Real assertions — fail loudly if the REST scanner regresses or stops
# populating ConnectHost (which the UI's per-host grouping depends on).
if [ "$CONNECTOR_COUNT" -le 0 ]; then
    echo "ERROR: expected at least one self-managed connector in state, found $CONNECTOR_COUNT"
    exit 1
fi
if [ "$CONNECTOR_HOST_COUNT" -le 0 ]; then
    echo "ERROR: connectors are present but none have connect_host populated; the UI's per-Connect-host grouping will break"
    exit 1
fi
echo ""

# ----------------------------------------------------------------------------
# kcp scan connect-topics — discover Connect worker URLs by parsing the
# connect-status topic. Verifies the discovery command finds the worker URL
# the docker-compose Connect profile advertises (CONNECT_REST_ADVERTISED_HOST_NAME
# = localhost, CONNECT_REST_PORT = 8083 → http://localhost:8083).
# ----------------------------------------------------------------------------
echo "=========================================="
echo "  scan connect-topics Test"
echo "=========================================="

# Snapshot state hash to verify R8 (no state mutation).
STATE_HASH_BEFORE=$(shasum -a 256 "$STATE" | cut -d' ' -f1)

CONNECT_URLS_FILE="connect-urls.txt"
echo "Running: ./kcp scan connect-topics --credentials-file ... --state-file $STATE --cluster-id osk-kafka --topics connect-status"
./kcp scan connect-topics \
    --credentials-file integration-tests/osk-scan/credentials/kafka-plaintext.yaml \
    --state-file "$STATE" \
    --cluster-id osk-kafka \
    --topics connect-status > "$CONNECT_URLS_FILE"

echo ""
echo "Discovered Connect worker URLs:"
cat "$CONNECT_URLS_FILE"
echo ""

# Verify stdout is non-empty.
if [ ! -s "$CONNECT_URLS_FILE" ]; then
    echo "ERROR: scan connect-topics produced no output; expected at least one worker URL"
    exit 1
fi

# Verify the docker-compose Connect worker's advertised address appears in
# stdout. CONNECT_REST_ADVERTISED_HOST_NAME=localhost and CONNECT_REST_PORT=8083
# in integration-tests/osk-scan/docker-compose.yml. The command emits the raw
# worker_id without scheme inference.
if ! grep -q '^localhost:8083$' "$CONNECT_URLS_FILE"; then
    echo "ERROR: expected 'localhost:8083' in scan connect-topics output, got:"
    cat "$CONNECT_URLS_FILE"
    exit 1
fi

# Verify the state file was not mutated (R8 — phase-1 contract).
STATE_HASH_AFTER=$(shasum -a 256 "$STATE" | cut -d' ' -f1)
if [ "$STATE_HASH_BEFORE" != "$STATE_HASH_AFTER" ]; then
    echo "ERROR: state file was modified by 'scan connect-topics'; phase-1 contract is no-write."
    echo "  before: $STATE_HASH_BEFORE"
    echo "  after:  $STATE_HASH_AFTER"
    exit 1
fi

rm -f "$CONNECT_URLS_FILE"
echo "scan connect-topics test passed!"
echo ""

# Clean up Connect (leave base Kafka running for potential other tests)
echo "Stopping Kafka Connect..."
cd "$SCRIPT_DIR"
docker compose --profile connect down
cd "$ROOT_DIR"

echo "Kafka Connect scan test passed!"
