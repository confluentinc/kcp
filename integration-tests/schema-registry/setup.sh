#!/bin/bash
# Set up the Schema Registry integration test environment.
# Starts a KRaft Kafka broker and two Schema Registry instances, then registers test schemas.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$SCRIPT_DIR"

echo ""
echo "=========================================="
echo "  Schema Registry Test Setup"
echo "=========================================="

echo "Generating TLS certificates..."
bash generate-certs.sh

# ── Start containers ──────────────────────────────────────────────────────────
echo ""
echo "Starting Schema Registry environment..."
docker compose -p kcp-test-sr up -d

# ── Wait for Schema Registry instances ────────────────────────────────────────
echo ""
echo "Waiting for Schema Registry (unauthenticated) to be ready..."
for i in $(seq 1 60); do
    if curl -s "http://localhost:8081/subjects" > /dev/null 2>&1; then
        echo "  Schema Registry (unauthenticated) is ready"
        break
    fi
    if [ $i -eq 60 ]; then
        echo "  Timeout waiting for Schema Registry (unauthenticated)"
        exit 1
    fi
    sleep 2
done

echo "Waiting for Schema Registry (basic auth) to be ready..."
for i in $(seq 1 60); do
    if curl -s -u "schemauser:schemapass" "http://localhost:8082/subjects" > /dev/null 2>&1; then
        echo "  Schema Registry (basic auth) is ready"
        break
    fi
    if [ $i -eq 60 ]; then
        echo "  Timeout waiting for Schema Registry (basic auth)"
        exit 1
    fi
    sleep 2
done

# ── Register test schemas ─────────────────────────────────────────────────────

SR_UNAUTH="http://localhost:8081"
SR_BASIC_AUTH="http://localhost:8082"
SR_USERNAME="schemauser"
SR_PASSWORD="schemapass"

register_schema() {
    local url=$1
    local curl_opts=$2   # raw curl auth/TLS args, e.g. "-u user:pass" or "--cacert certs/ca-cert.pem --cert ... --key ..."
    local subject=$3
    local schema=$4
    local schema_type=$5

    local body
    if [ "$schema_type" = "JSON" ]; then
        body="{\"schemaType\": \"JSON\", \"schema\": $(echo "$schema" | jq -Rs .)}"
    else
        body="{\"schema\": $(echo "$schema" | jq -Rs .)}"
    fi

    local response
    # shellcheck disable=SC2086 # curl_opts is a space-separated arg list, intentionally word-split
    response=$(curl -s -w "\n%{http_code}" $curl_opts \
        -X POST "$url/subjects/$subject/versions" \
        -H "Content-Type: application/vnd.schemaregistry.v1+json" \
        -d "$body")

    local http_code=$(echo "$response" | tail -1)
    local body_response=$(echo "$response" | head -1)

    if [ "$http_code" = "200" ]; then
        echo "  Registered $subject (schema ID: $(echo $body_response | jq -r '.id'))"
    else
        echo "  Failed to register $subject: $body_response (HTTP $http_code)"
        return 1
    fi
}

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

ORDERS_KEY_SCHEMA='{
  "type": "record",
  "name": "OrderKey",
  "namespace": "com.example.orders",
  "fields": [
    {"name": "order_id", "type": "string"}
  ]
}'

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

echo ""
echo "Registering schemas on unauthenticated Schema Registry..."
register_schema "$SR_UNAUTH" "" "orders-value" "$ORDERS_SCHEMA" "AVRO"
register_schema "$SR_UNAUTH" "" "orders-key" "$ORDERS_KEY_SCHEMA" "AVRO"
register_schema "$SR_UNAUTH" "" "events-value" "$EVENTS_SCHEMA" "JSON"
register_schema "$SR_UNAUTH" "" "test-topic-1-value" "$TEST_TOPIC_1_SCHEMA" "AVRO"

echo ""
echo "Registering schemas on basic auth Schema Registry..."
BASIC_OPTS="-u $SR_USERNAME:$SR_PASSWORD"
register_schema "$SR_BASIC_AUTH" "$BASIC_OPTS" "orders-value" "$ORDERS_SCHEMA" "AVRO"
register_schema "$SR_BASIC_AUTH" "$BASIC_OPTS" "orders-key" "$ORDERS_KEY_SCHEMA" "AVRO"
register_schema "$SR_BASIC_AUTH" "$BASIC_OPTS" "events-value" "$EVENTS_SCHEMA" "JSON"
register_schema "$SR_BASIC_AUTH" "$BASIC_OPTS" "test-topic-1-value" "$TEST_TOPIC_1_SCHEMA" "AVRO"

# ── HTTPS + Basic-auth SR (:8443) and mTLS SR (:8444) ─────────────────────────
SR_BASIC_TLS="https://localhost:8443"
SR_MTLS="https://localhost:8444"
BASIC_TLS_OPTS="--cacert certs/ca-cert.pem -u $SR_USERNAME:$SR_PASSWORD"
MTLS_OPTS="--cacert certs/ca-cert.pem --cert certs/client-cert.pem --key certs/client-key.pem"

echo ""
echo "Waiting for Schema Registry (basic-tls, :8443) to be ready..."
for i in $(seq 1 60); do
    # shellcheck disable=SC2086
    if curl -s $BASIC_TLS_OPTS "$SR_BASIC_TLS/subjects" > /dev/null 2>&1; then
        echo "  Schema Registry (basic-tls) is ready"; break
    fi
    [ "$i" = "60" ] && { echo "  Timeout waiting for basic-tls SR"; exit 1; }
    sleep 2
done

echo "Waiting for Schema Registry (mtls, :8444) to be ready..."
for i in $(seq 1 60); do
    # shellcheck disable=SC2086
    if curl -s $MTLS_OPTS "$SR_MTLS/subjects" > /dev/null 2>&1; then
        echo "  Schema Registry (mtls) is ready"; break
    fi
    [ "$i" = "60" ] && { echo "  Timeout waiting for mtls SR"; exit 1; }
    sleep 2
done

echo ""
echo "Registering schemas on HTTPS basic-auth Schema Registry (:8443)..."
register_schema "$SR_BASIC_TLS" "$BASIC_TLS_OPTS" "orders-value" "$ORDERS_SCHEMA" "AVRO"
register_schema "$SR_BASIC_TLS" "$BASIC_TLS_OPTS" "orders-key" "$ORDERS_KEY_SCHEMA" "AVRO"
register_schema "$SR_BASIC_TLS" "$BASIC_TLS_OPTS" "events-value" "$EVENTS_SCHEMA" "JSON"
register_schema "$SR_BASIC_TLS" "$BASIC_TLS_OPTS" "test-topic-1-value" "$TEST_TOPIC_1_SCHEMA" "AVRO"

echo ""
echo "Registering schemas on mTLS Schema Registry (:8444)..."
register_schema "$SR_MTLS" "$MTLS_OPTS" "orders-value" "$ORDERS_SCHEMA" "AVRO"
register_schema "$SR_MTLS" "$MTLS_OPTS" "orders-key" "$ORDERS_KEY_SCHEMA" "AVRO"
register_schema "$SR_MTLS" "$MTLS_OPTS" "events-value" "$EVENTS_SCHEMA" "JSON"
register_schema "$SR_MTLS" "$MTLS_OPTS" "test-topic-1-value" "$TEST_TOPIC_1_SCHEMA" "AVRO"

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "Schema Registry environment ready:"
echo "  Unauthenticated: http://localhost:8081"
echo "  Basic Auth:      http://localhost:8082 (schemauser / schemapass)"
echo ""
