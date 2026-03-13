#!/bin/bash
# Creates test topics for TLS cluster (runs inside container)

set -e

CONTAINER_NAME="kcp-test-kafka-tls"

echo "Creating test topics on TLS cluster..."

# Run kafka-topics inside the container using internal SSL broker address
# Note: TLS cluster uses SSL for internal communication, but we can use the internal listener
docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server kafka-tls:29092 \
    --command-config /etc/kafka/client.properties \
    --create \
    --topic test-topic-1 \
    --partitions 3 \
    --replication-factor 1 || true

docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server kafka-tls:29092 \
    --command-config /etc/kafka/client.properties \
    --create \
    --topic test-topic-2 \
    --partitions 1 \
    --replication-factor 1 || true

echo "TLS test topics created successfully"
