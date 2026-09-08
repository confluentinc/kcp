#!/bin/sh
# Seeds Prometheus with 30 days of a single relabelled Connect series.
# Uses promtool to create TSDB blocks from OpenMetrics format, mirroring
# integration-tests/osk-scan/seed-prometheus-data.sh.
#
# This harness has no JMX exporter scraping Kafka Connect into Prometheus, so
# the DEFAULT series (kafka_connect_worker_task_count) never exists here at
# all. The only series seeded is the RELABELLED one
# (credentials/prometheus-relabelled.yaml's connect_metric_names override),
# so task-count resolving at all is proof the override drove the query rather
# than the default series name — no magnitude comparison needed.

set -e

PROM_URL="${PROM_URL:-http://connect-prometheus:9090}"
DATA_DIR="/tmp/prom-seed"
mkdir -p "$DATA_DIR"

# Generate 30 days of data at 1-hour intervals (720 data points).
NOW=$(date +%s)
START=$((NOW - 30 * 24 * 3600))
STEP=3600

METRICS_FILE="$DATA_DIR/metrics.txt"
> "$METRICS_FILE"

# acme_connect_task_count is a gauge: hold it at a distinctive constant (424)
# for the whole range so the scan's sum(...) aggregate reflects that value.
t=$START
while [ $t -le $NOW ]; do
  echo "acme_connect_task_count 424 ${t}" >> "$METRICS_FILE"
  t=$((t + STEP))
done

echo "# EOF" >> "$METRICS_FILE"

LINES=$(wc -l < "$METRICS_FILE")
echo "Generated $LINES metric lines covering 30 days"

echo "Creating TSDB blocks..."
promtool tsdb create-blocks-from openmetrics "$METRICS_FILE" /prometheus

echo "Reloading Prometheus to pick up new blocks..."
wget -q -O /dev/null --post-data='' "${PROM_URL}/-/reload" 2>/dev/null || true

sleep 2

echo "Verifying seeded data..."
RESULT=$(wget -q -O - "${PROM_URL}/api/v1/query?query=acme_connect_task_count" 2>/dev/null || echo "query failed")
echo "Verification result: $RESULT"

echo "Done! Seeded 30 days of acme_connect_task_count into Prometheus"
