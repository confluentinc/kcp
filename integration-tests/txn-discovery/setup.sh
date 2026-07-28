#!/bin/bash
# Start the broker for the `kcp migration txn-discovery` integration suite.
#
# Self-contained by design: the Go suite (build tag e2e_txndiscovery) creates
# every topic and produces every transaction in-process with sarama, so there
# are no seed scripts here and nothing to keep in sync with the assertions.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINER_NAME="kcp-test-txn-discovery-kafka"

echo "Starting txn-discovery broker..."
docker compose -f "$SCRIPT_DIR/docker-compose.yml" up -d

echo "Waiting for Kafka to be ready..."
MAX_WAIT=90
WAIT_TIME=0
while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    if docker exec "$CONTAINER_NAME" kafka-broker-api-versions --bootstrap-server localhost:9092 > /dev/null 2>&1; then
        echo "Kafka is ready!"
        break
    fi
    echo "Kafka not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 2
    WAIT_TIME=$((WAIT_TIME + 2))
done

if [ $WAIT_TIME -ge $MAX_WAIT ]; then
    echo "Timeout waiting for Kafka"
    docker compose -f "$SCRIPT_DIR/docker-compose.yml" logs --tail 100 kafka
    exit 1
fi

echo ""
echo "Environment is ready."
echo "  Plaintext bootstrap: localhost:29092"
