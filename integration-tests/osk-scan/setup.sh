#!/bin/bash
# Start the OSK scan broker and create test data.
# Self-contained — no dependencies on other test infrastructure.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Generate TLS certificates
bash "$SCRIPT_DIR/generate-certs.sh"

# Start the broker
echo "Starting OSK scan broker..."
docker compose -f "$SCRIPT_DIR/docker-compose.yml" up -d

# Wait for Kafka to be ready on the internal plaintext listener
echo "Waiting for Kafka to be ready..."
CONTAINER_NAME="kcp-test-osk-kafka"
MAX_WAIT=60
WAIT_TIME=0

while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    if docker exec $CONTAINER_NAME kafka-broker-api-versions --bootstrap-server osk-kafka:29092 > /dev/null 2>&1; then
        echo "Kafka is ready!"
        break
    fi
    echo "Kafka not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 2
    WAIT_TIME=$((WAIT_TIME + 2))
done

if [ $WAIT_TIME -ge $MAX_WAIT ]; then
    echo "Timeout waiting for Kafka"
    exit 1
fi

# Create test topics
echo "Creating test topics..."
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-topics --bootstrap-server osk-kafka:29092 \
    --create --if-not-exists --topic test-topic-1 --partitions 3 --replication-factor 1 > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-topics --bootstrap-server osk-kafka:29092 \
    --create --if-not-exists --topic test-topic-2 --partitions 1 --replication-factor 1 > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-topics --bootstrap-server osk-kafka:29092 \
    --create --if-not-exists --topic orders --partitions 3 --replication-factor 1 > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-topics --bootstrap-server osk-kafka:29092 \
    --create --if-not-exists --topic events --partitions 2 --replication-factor 1 > /dev/null || true
echo "Test topics created."

# Create test topics on JMX brokers
for BROKER_CONTAINER in kcp-test-osk-kafka-jmx-auth kcp-test-osk-kafka-jmx-tls; do
    BROKER_HOST="${BROKER_CONTAINER#kcp-test-}"
    echo "Waiting for $BROKER_CONTAINER to be ready..."
    MAX_WAIT=60
    WAIT_TIME=0
    while [ $WAIT_TIME -lt $MAX_WAIT ]; do
        if docker exec $BROKER_CONTAINER kafka-broker-api-versions --bootstrap-server ${BROKER_HOST}:29092 > /dev/null 2>&1; then
            echo "$BROKER_CONTAINER is ready!"
            break
        fi
        echo "$BROKER_CONTAINER not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
        sleep 2
        WAIT_TIME=$((WAIT_TIME + 2))
    done
    if [ $WAIT_TIME -ge $MAX_WAIT ]; then
        echo "Timeout waiting for $BROKER_CONTAINER"
        exit 1
    fi
    echo "Creating test topics on $BROKER_CONTAINER..."
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-topics --bootstrap-server ${BROKER_HOST}:29092 \
        --create --if-not-exists --topic test-topic-1 --partitions 3 --replication-factor 1 > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-topics --bootstrap-server ${BROKER_HOST}:29092 \
        --create --if-not-exists --topic test-topic-2 --partitions 1 --replication-factor 1 > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-topics --bootstrap-server ${BROKER_HOST}:29092 \
        --create --if-not-exists --topic orders --partitions 3 --replication-factor 1 > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-topics --bootstrap-server ${BROKER_HOST}:29092 \
        --create --if-not-exists --topic events --partitions 2 --replication-factor 1 > /dev/null || true
    echo "Test topics created on $BROKER_CONTAINER."
done

# Create test ACLs
echo "Creating test ACLs..."
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team1 --operation Read --operation Write --topic orders > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team1 --operation Read --group team1-consumer-group > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team2 --operation Read --topic orders > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team2 --operation Write --topic events > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team2 --operation Read --group team2-consumer-group > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team3 --operation Read --topic '*' > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team3 --operation Read --group '*' > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team4 --operation All --topic test-topic-1 > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team5 --operation Describe --topic test-topic-1 > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team5 --operation Describe --topic test-topic-2 > /dev/null || true
docker exec -e KAFKA_OPTS= $CONTAINER_NAME kafka-acls --bootstrap-server osk-kafka:29092 \
    --add --allow-principal User:team5 --operation Read --topic events > /dev/null || true
echo "Test ACLs created."

# Create test ACLs on JMX brokers
for BROKER_CONTAINER in kcp-test-osk-kafka-jmx-auth kcp-test-osk-kafka-jmx-tls; do
    BROKER_HOST="${BROKER_CONTAINER#kcp-test-}"
    echo "Creating test ACLs on $BROKER_CONTAINER..."
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team1 --operation Read --operation Write --topic orders > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team1 --operation Read --group team1-consumer-group > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team2 --operation Read --topic orders > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team2 --operation Write --topic events > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team2 --operation Read --group team2-consumer-group > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team3 --operation Read --topic '*' > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team3 --operation Read --group '*' > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team4 --operation All --topic test-topic-1 > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team5 --operation Describe --topic test-topic-1 > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team5 --operation Describe --topic test-topic-2 > /dev/null || true
    docker exec -e KAFKA_OPTS= $BROKER_CONTAINER kafka-acls --bootstrap-server ${BROKER_HOST}:29092 \
        --add --allow-principal User:team5 --operation Read --topic events > /dev/null || true
    echo "Test ACLs created on $BROKER_CONTAINER."
done

# Wait for all Prometheus seeders to complete
echo "Waiting for Prometheus seeders..."
docker wait kcp-test-osk-prometheus-seeder kcp-test-osk-prometheus-auth-seeder kcp-test-osk-prometheus-tls-seeder > /dev/null
echo "Restarting Prometheus instances to load seeded TSDB blocks..."
docker restart kcp-test-osk-prometheus kcp-test-osk-prometheus-auth kcp-test-osk-prometheus-tls > /dev/null
sleep 3
echo "Prometheus data ready."

# Give producer/consumer time to generate JMX traffic before the scan runs
echo "Waiting for producer to generate Kafka traffic..."
sleep 10

echo ""
echo "Environment is ready."
echo "  Plaintext:       localhost:9092"
echo "  SASL/SCRAM:      localhost:9093"
echo "  TLS/mTLS:        localhost:9094"
echo "  SASL+SSL:        localhost:9095"
echo "  SASL/PLAIN:      localhost:9098"
echo "  JMX-auth Kafka:  localhost:9096"
echo "  JMX-TLS Kafka:   localhost:9097"
echo "  Jolokia:         http://localhost:8778/jolokia"
echo "  Jolokia (auth):  http://localhost:8779/jolokia"
echo "  Jolokia (TLS):   https://localhost:8780/jolokia"
echo "  Prometheus:      http://localhost:9290"
echo "  Prometheus (auth): http://localhost:9291"
echo "  Prometheus (TLS):  https://localhost:9292"
