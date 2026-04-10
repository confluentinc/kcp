#!/bin/sh
# Seeds Prometheus with 30 days of synthetic Kafka metrics
# Uses promtool to create TSDB blocks from OpenMetrics format

set -e

PROM_URL="${PROM_URL:-http://prometheus:9090}"
DATA_DIR="/tmp/prom-seed"
mkdir -p "$DATA_DIR"

# Generate 30 days of data at 1-hour intervals (720 data points)
NOW=$(date +%s)
START=$((NOW - 30 * 24 * 3600))
STEP=3600

generate_metric_line() {
  name=$1
  timestamp=$2
  value=$3
  echo "${name} ${value} ${timestamp}"
}

# Create OpenMetrics format file
METRICS_FILE="$DATA_DIR/metrics.txt"
> "$METRICS_FILE"

# Generate realistic data with cumulative counters for _total metrics
# Counter metrics accumulate: value = previous + rate * step_seconds
# Gauge metrics are point-in-time values
t=$START
i=0
cum_bytes_in=0
cum_bytes_out=0
cum_msgs_in=0
while [ $t -le $NOW ]; do
  hour=$(( (t / 3600) % 24 ))

  # Higher traffic during business hours (8-18)
  if [ $hour -ge 8 ] && [ $hour -le 18 ]; then
    traffic_mult=2
  else
    traffic_mult=1
  fi

  variance=$(( (i * 7 + 13) % 20 ))

  # Counter metrics: accumulate rate * step_seconds (3600s)
  # BytesInPerSec rate: 50000-200000 bytes/sec → accumulate over 1 hour
  rate_bytes_in=$(( 50000 + traffic_mult * 50000 + variance * 1000 ))
  cum_bytes_in=$(( cum_bytes_in + rate_bytes_in * STEP ))
  generate_metric_line "kafka_server_brokertopicmetrics_bytesinpersec_total" "$t" "$cum_bytes_in" >> "$METRICS_FILE"

  # BytesOutPerSec rate: 30000-150000 bytes/sec
  rate_bytes_out=$(( 30000 + traffic_mult * 30000 + variance * 800 ))
  cum_bytes_out=$(( cum_bytes_out + rate_bytes_out * STEP ))
  generate_metric_line "kafka_server_brokertopicmetrics_bytesoutpersec_total" "$t" "$cum_bytes_out" >> "$METRICS_FILE"

  # MessagesInPerSec rate: 100-500 msgs/sec
  rate_msgs_in=$(( 100 + traffic_mult * 100 + variance * 5 ))
  cum_msgs_in=$(( cum_msgs_in + rate_msgs_in * STEP ))
  generate_metric_line "kafka_server_brokertopicmetrics_messagesinpersec_total" "$t" "$cum_msgs_in" >> "$METRICS_FILE"

  # Gauge metrics: point-in-time values
  generate_metric_line "kafka_server_replicamanager_partitioncount" "$t" "50" >> "$METRICS_FILE"

  conns=$(( 5 + traffic_mult * 5 + variance % 10 ))
  generate_metric_line "kafka_server_socketservermetrics_connection_count" "$t" "$conns" >> "$METRICS_FILE"

  storage_gb_x100=$(( 500 + i * 100 / 720 ))
  storage_bytes=$(( storage_gb_x100 * 1073741824 / 100 ))
  generate_metric_line "kafka_log_log_size" "$t" "$storage_bytes" >> "$METRICS_FILE"

  t=$((t + STEP))
  i=$((i + 1))
done

# Add EOF marker for OpenMetrics
echo "# EOF" >> "$METRICS_FILE"

LINES=$(wc -l < "$METRICS_FILE")
echo "Generated $LINES metric lines covering 30 days"

# Import into Prometheus TSDB
echo "Creating TSDB blocks..."
promtool tsdb create-blocks-from openmetrics "$METRICS_FILE" /prometheus

echo "Reloading Prometheus to pick up new blocks..."
wget -q -O /dev/null --post-data='' "${PROM_URL}/-/reload" 2>/dev/null || true

# Wait for blocks to be loaded
sleep 2

# Verify data is queryable
echo "Verifying seeded data..."
RESULT=$(wget -q -O - "${PROM_URL}/api/v1/query?query=kafka_server_replicamanager_partitioncount" 2>/dev/null || echo "query failed")
echo "Verification result: $RESULT"

echo "Done! Seeded 30 days of Kafka metrics into Prometheus"
