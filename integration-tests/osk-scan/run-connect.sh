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
jq -r '.osk_sources.clusters[0].kafka_admin_client_information | "  Topics: \(.topics.details | length), ACLs: \(.acls | length), Connectors: \(if .self_managed_connectors then (.self_managed_connectors | length) else 0 end)"' "$STATE"
echo ""

# Clean up Connect (leave base Kafka running for potential other tests)
echo "Stopping Kafka Connect..."
cd "$SCRIPT_DIR"
docker compose --profile connect down
cd "$ROOT_DIR"

echo "Kafka Connect scan test passed!"
