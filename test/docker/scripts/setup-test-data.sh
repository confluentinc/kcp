#!/bin/bash
# Creates test topics and ACLs for integration testing (plaintext/KRaft)

set -e

BOOTSTRAP=$1

echo "Setting up test data on $BOOTSTRAP"

# Create test topics
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic test-topic-1 --partitions 3 --replication-factor 1 || true
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic test-topic-2 --partitions 1 --replication-factor 1 || true
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic orders --partitions 3 --replication-factor 1 || true
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic events --partitions 2 --replication-factor 1 || true

echo "Test topics created successfully"

# Create test ACLs
echo "Creating test ACLs..."

# Team 1 - read/write on orders topic
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team1 --operation Read --operation Write --topic orders || true
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team1 --operation Read --group team1-consumer-group || true

# Team 2 - read-only on orders, write on events
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team2 --operation Read --topic orders || true
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team2 --operation Write --topic events || true
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team2 --operation Read --group team2-consumer-group || true

# Team 3 - read on all topics (wildcard)
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team3 --operation Read --topic '*' || true
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team3 --operation Read --group '*' || true

# Team 4 - admin on test-topic-1
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team4 --operation All --topic test-topic-1 || true

# Team 5 - describe/read on cluster
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team5 --operation Describe --topic test-topic-1 || true
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team5 --operation Describe --topic test-topic-2 || true
kafka-acls --bootstrap-server $BOOTSTRAP --add --allow-principal User:team5 --operation Read --topic events || true

echo "Test ACLs created successfully"
