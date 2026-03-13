#!/bin/bash
# Creates test topics for SASL cluster (runs inside container)

set -e

CONTAINER_NAME="kcp-test-kafka-sasl"

echo "Creating test topics on SASL cluster..."

# Run kafka-topics inside the container using internal broker address
docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server localhost:29092 \
    --create \
    --topic test-topic-1 \
    --partitions 3 \
    --replication-factor 1 || true

docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server localhost:29092 \
    --create \
    --topic test-topic-2 \
    --partitions 1 \
    --replication-factor 1 || true

echo "SASL test topics created successfully"
