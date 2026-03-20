#!/bin/bash
# Registers test schemas in both unauthenticated and basic auth Schema Registry instances

set -e

SR_UNAUTH="http://localhost:8081"
SR_BASIC_AUTH="http://localhost:8082"
SR_USERNAME="schemauser"
SR_PASSWORD="schemapass"

echo "Waiting for Schema Registry (unauthenticated) to be ready..."
for i in $(seq 1 30); do
    if curl -s "$SR_UNAUTH/subjects" > /dev/null 2>&1; then
        echo "Schema Registry (unauthenticated) is ready"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "Timeout waiting for Schema Registry (unauthenticated)"
        exit 1
    fi
    sleep 2
done

echo "Waiting for Schema Registry (basic auth) to be ready..."
for i in $(seq 1 30); do
    if curl -s -u "$SR_USERNAME:$SR_PASSWORD" "$SR_BASIC_AUTH/subjects" > /dev/null 2>&1; then
        echo "Schema Registry (basic auth) is ready"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "Timeout waiting for Schema Registry (basic auth)"
        exit 1
    fi
    sleep 2
done

# Function to register a schema
register_schema() {
    local url=$1
    local auth=$2
    local subject=$3
    local schema=$4
    local schema_type=$5

    local auth_flag=""
    if [ -n "$auth" ]; then
        auth_flag="-u $auth"
    fi

    local body
    if [ "$schema_type" = "JSON" ]; then
        body="{\"schemaType\": \"JSON\", \"schema\": $(echo "$schema" | jq -Rs .)}"
    else
        body="{\"schema\": $(echo "$schema" | jq -Rs .)}"
    fi

    local response
    response=$(curl -s -w "\n%{http_code}" $auth_flag \
        -X POST "$url/subjects/$subject/versions" \
        -H "Content-Type: application/vnd.schemaregistry.v1+json" \
        -d "$body")

    local http_code=$(echo "$response" | tail -1)
    local body_response=$(echo "$response" | head -1)

    if [ "$http_code" = "200" ]; then
        echo "  Registered $subject (schema ID: $(echo $body_response | jq -r '.id'))"
    else
        echo "  Failed to register $subject: $body_response (HTTP $http_code)"
    fi
}

# Avro schema for orders topic
ORDERS_SCHEMA='{
  "type": "record",
  "name": "Order",
  "namespace": "com.example.orders",
  "fields": [
    {"name": "order_id", "type": "string"},
    {"name": "customer_id", "type": "string"},
    {"name": "product", "type": "string"},
    {"name": "amount", "type": "double"},
    {"name": "currency", "type": {"type": "string", "default": "USD"}},
    {"name": "timestamp", "type": {"type": "long", "logicalType": "timestamp-millis"}}
  ]
}'

# JSON Schema for events topic
EVENTS_SCHEMA='{
  "type": "object",
  "properties": {
    "event_id": {"type": "string"},
    "event_type": {"type": "string", "enum": ["click", "view", "purchase", "signup"]},
    "source": {"type": "string"},
    "payload": {"type": "object"},
    "timestamp": {"type": "string", "format": "date-time"}
  },
  "required": ["event_id", "event_type", "source", "timestamp"]
}'

# Simple Avro schema for test-topic-1
TEST_TOPIC_1_SCHEMA='{
  "type": "record",
  "name": "TestRecord",
  "namespace": "com.example.test",
  "fields": [
    {"name": "id", "type": "int"},
    {"name": "name", "type": "string"},
    {"name": "active", "type": "boolean", "default": true}
  ]
}'

# Avro key schema for orders topic
ORDERS_KEY_SCHEMA='{
  "type": "record",
  "name": "OrderKey",
  "namespace": "com.example.orders",
  "fields": [
    {"name": "order_id", "type": "string"}
  ]
}'

echo ""
echo "=== Registering schemas on unauthenticated Schema Registry ($SR_UNAUTH) ==="
register_schema "$SR_UNAUTH" "" "orders-value" "$ORDERS_SCHEMA" "AVRO"
register_schema "$SR_UNAUTH" "" "orders-key" "$ORDERS_KEY_SCHEMA" "AVRO"
register_schema "$SR_UNAUTH" "" "events-value" "$EVENTS_SCHEMA" "JSON"
register_schema "$SR_UNAUTH" "" "test-topic-1-value" "$TEST_TOPIC_1_SCHEMA" "AVRO"

echo ""
echo "=== Registering schemas on basic auth Schema Registry ($SR_BASIC_AUTH) ==="
register_schema "$SR_BASIC_AUTH" "$SR_USERNAME:$SR_PASSWORD" "orders-value" "$ORDERS_SCHEMA" "AVRO"
register_schema "$SR_BASIC_AUTH" "$SR_USERNAME:$SR_PASSWORD" "orders-key" "$ORDERS_KEY_SCHEMA" "AVRO"
register_schema "$SR_BASIC_AUTH" "$SR_USERNAME:$SR_PASSWORD" "events-value" "$EVENTS_SCHEMA" "JSON"
register_schema "$SR_BASIC_AUTH" "$SR_USERNAME:$SR_PASSWORD" "test-topic-1-value" "$TEST_TOPIC_1_SCHEMA" "AVRO"

echo ""
echo "=== Verifying schemas ==="
echo "Unauthenticated subjects:"
curl -s "$SR_UNAUTH/subjects" | jq .
echo ""
echo "Basic auth subjects:"
curl -s -u "$SR_USERNAME:$SR_PASSWORD" "$SR_BASIC_AUTH/subjects" | jq .

echo ""
echo "Schema Registry test data setup complete!"
echo "  Unauthenticated: $SR_UNAUTH"
echo "  Basic Auth:      $SR_BASIC_AUTH (user: $SR_USERNAME, pass: $SR_PASSWORD)"
