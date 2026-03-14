#!/bin/bash
# Waits for Kafka to be ready

BOOTSTRAP=$1
MAX_WAIT=60
WAIT_TIME=0

echo "Waiting for Kafka at $BOOTSTRAP..."

while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    if kafka-broker-api-versions --bootstrap-server $BOOTSTRAP > /dev/null 2>&1; then
        echo "Kafka is ready!"
        exit 0
    fi

    echo "Kafka not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 2
    WAIT_TIME=$((WAIT_TIME + 2))
done

echo "Timeout waiting for Kafka"
exit 1
