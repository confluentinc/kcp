#!/bin/bash
# Creates test topics for integration testing

set -e

BOOTSTRAP=$1

echo "Setting up test data on $BOOTSTRAP"

# Create test topics
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic test-topic-1 --partitions 3 --replication-factor 1 || true
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic test-topic-2 --partitions 1 --replication-factor 1 || true

echo "Test topics created successfully"
