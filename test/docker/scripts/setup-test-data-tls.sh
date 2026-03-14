#!/bin/bash
# Creates test topics and ACLs for TLS cluster (runs inside container)

set -e

CONTAINER_NAME="kcp-test-kafka-tls"
INTERNAL_BOOTSTRAP="kafka-tls:29092"
CMD_CONFIG="--command-config /etc/kafka/client.properties"

echo "Creating test topics on TLS cluster..."

docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --create --topic test-topic-1 --partitions 3 --replication-factor 1 || true

docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --create --topic test-topic-2 --partitions 1 --replication-factor 1 || true

docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --create --topic orders --partitions 3 --replication-factor 1 || true

docker exec $CONTAINER_NAME kafka-topics \
    --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --create --topic events --partitions 2 --replication-factor 1 || true

echo "TLS test topics created successfully"

echo "Creating test ACLs on TLS cluster..."

# Team 1 - read/write on orders topic
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team1 --operation Read --operation Write --topic orders || true
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team1 --operation Read --group team1-consumer-group || true

# Team 2 - read-only on orders, write on events
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team2 --operation Read --topic orders || true
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team2 --operation Write --topic events || true
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team2 --operation Read --group team2-consumer-group || true

# Team 3 - read on all topics (wildcard)
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team3 --operation Read --topic '*' || true
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team3 --operation Read --group '*' || true

# Team 4 - admin on test-topic-1
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team4 --operation All --topic test-topic-1 || true

# Team 5 - describe/read on specific topics
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team5 --operation Describe --topic test-topic-1 || true
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team5 --operation Describe --topic test-topic-2 || true
docker exec $CONTAINER_NAME kafka-acls --bootstrap-server $INTERNAL_BOOTSTRAP $CMD_CONFIG \
    --add --allow-principal User:team5 --operation Read --topic events || true

echo "TLS test ACLs created successfully"
