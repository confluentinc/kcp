#!/usr/bin/env bash
# Stands up the live topology for the route-conversion suite on its own Minikube
# profile (kcp-e2e-route-conversion, see state.sh): a source CP Kafka (PLAINTEXT)
# and a destination CP Kafka (SASL_SSL) linked by one ClusterLink, a dynamic-mode
# Confluent Gateway with hot reload that clients really use (swap auth to the
# destination), and the rc-runner pod the tests run in.
#
# It ends in the state a finished topic-based migration leaves: every link
# mirror promoted (STOPPED) and the route sending every link topic to the
# destination with group coordination on the source and no fence. That is the
# state `spec.route.convertTo: static` converts from. The shared steps (version
# pins, minikube, images, operator, clusters, licence, client-tls, REST
# credential, REST class, swap-auth secrets) come from ../lib/cp-e2e-env.sh,
# which idempotent-fsm's setup.sh sources too.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATES_DIR="${SCRIPT_DIR}/testdata/manifests/templates"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"
# shellcheck source=../lib/cp-e2e-env.sh
. "${SCRIPT_DIR}/../lib/cp-e2e-env.sh"

NAMESPACE="confluent"

# --- Gateway / link / topic contract (single source of truth; mirrored into .env) ---
GATEWAY_NAME="${GATEWAY_NAME:-rc-gateway}"
GATEWAY_REPLICAS="${GATEWAY_REPLICAS:-2}"
ROUTE_NAME="${ROUTE_NAME:-rc-route}"
SOURCE_DOMAIN="source-domain"
DEST_DOMAIN="destination-domain"
SOURCE_BOOTSTRAP_ID="SOURCE"
DEST_BOOTSTRAP_ID="DESTINATION"
GATEWAY_BOOTSTRAP="bootstrap.gw.local:9595"
CLUSTER_LINK_NAME="${CLUSTER_LINK_NAME:-rc-link}"
TOPIC_PREFIX="rc-topic-"
LINK_TOPIC_COUNT=6
# On the source only and never on the link: the input for the untracked-topic
# warning and for the commits-outside-the-link refusal.
ORPHAN_TOPIC="rc-orphan"
SOURCE_BOOTSTRAP="source-kafka.${NAMESPACE}.svc.cluster.local:9071"
DEST_BOOTSTRAP="destination-kafka.${NAMESPACE}.svc.cluster.local:9071"
REST_ENDPOINT="http://destination-kafka.${NAMESPACE}.svc.cluster.local:8090"
RUNNER="rc-runner"
# Records written to every link topic before promotion, so seeded offsets sit
# below every partition's high-water mark on both clusters.
SEED_RECORDS=300
MIN_PARTITION_RECORDS=10

# topic_name — zero-padded link topic name for an index (1 -> rc-topic-001).
topic_name() { printf '%s%03d' "${TOPIC_PREFIX}" "$1"; }
# partitions_of — rc-topic-001 and rc-topic-002 have 3 partitions, the rest 1.
partitions_of() { case "$1" in 1 | 2) echo 3 ;; *) echo 1 ;; esac; }

k() { kubectl --context "${PROFILE}" -n "${NAMESPACE}" "$@"; }

# rest METHOD URL [BODY] — one destination REST call, made from the runner pod
# (the REST endpoint is in-cluster). The credential comes from the pod's own env,
# so it never appears on this exec argv.
rest() {
  k exec "${RUNNER}" -- sh -c \
    'curl -sS -f -u "${KCP_RC_DEST_SASL_USER}:${KCP_RC_DEST_SASL_PASSWORD}" -X "$1" -H "Content-Type: application/json" ${3:+--data "$3"} "$2"' \
    _ "$@"
}

LINK_TOPICS=()
for i in $(seq 1 "${LINK_TOPIC_COUNT}"); do LINK_TOPICS+=("$(topic_name "$i")"); done

echo "=== KCP route-conversion E2E setup ==="
echo "Profile:        ${PROFILE}"
echo "CFK chart:      ${CFK_CHART_OCI} (${CFK_CHART_VERSION})"
echo "Gateway image:  ${GATEWAY_IMAGE} (dynamic mode, clients through it)"
echo "Link topics:    ${LINK_TOPICS[*]} (+ ${ORPHAN_TOPIC} on the source only)"
echo ""

# --- Preflight, minikube, images, operator, credentials, clusters (shared) ---
e2e_preflight
e2e_start_minikube
e2e_pull_images
e2e_install_operator
e2e_create_credentials
e2e_deploy_kafka

# --- Secrets: licence, gateway client-tls, runner REST creds (shared) ---
e2e_install_licence
e2e_create_client_tls
e2e_create_rest_credentials_secret

# --- Source topics: the link topics + one source-only topic ---
echo "Creating ${LINK_TOPIC_COUNT} link topics + ${ORPHAN_TOPIC} on the source..."
for i in $(seq 1 "${LINK_TOPIC_COUNT}"); do
  t="$(topic_name "$i")"
  k apply -f - >/dev/null <<EOF
apiVersion: platform.confluent.io/v1beta1
kind: KafkaTopic
metadata:
  name: ${t}
  namespace: ${NAMESPACE}
spec:
  replicas: 1
  partitionCount: $(partitions_of "$i")
  configs:
    cleanup.policy: delete
  kafkaClusterRef:
    name: source-kafka
EOF
done
k apply -f - >/dev/null <<EOF
apiVersion: platform.confluent.io/v1beta1
kind: KafkaTopic
metadata:
  name: ${ORPHAN_TOPIC}
  namespace: ${NAMESPACE}
spec:
  replicas: 1
  partitionCount: 1
  kafkaClusterRef:
    name: source-kafka
EOF
echo "Waiting for source topics to reach CREATED..."
for t in "${LINK_TOPICS[@]}" "${ORPHAN_TOPIC}"; do
  created=""
  for _ in $(seq 1 60); do
    if k get kafkatopic "${t}" -o jsonpath='{.status.state}' 2>/dev/null | grep -q CREATED; then created=1; break; fi
    sleep 3
  done
  [ -n "${created}" ] || { echo "FATAL: topic ${t} never reached CREATED" >&2; exit 1; }
done
echo "  ✓ source topics created"

# --- KafkaRestClass (shared) + cluster link mirroring the link topics ---
e2e_apply_rest_class
# consumer.offset.sync.enable must be off: a dynamic route (and so a
# conversion) refuses a link that syncs offsets itself.
echo "Creating cluster link ${CLUSTER_LINK_NAME} mirroring ${LINK_TOPIC_COUNT} topics (consumer.offset.sync.enable=false)..."
LINK_CR="${RENDERED_DIR}/cluster-link.yaml"
mkdir -p "${RENDERED_DIR}"
{
  cat <<EOF
apiVersion: platform.confluent.io/v1beta1
kind: ClusterLink
metadata:
  name: ${CLUSTER_LINK_NAME}
  namespace: ${NAMESPACE}
spec:
  sourceKafkaCluster:
    kafkaRestClassRef:
      name: source-kafka-rest-class
    bootstrapEndpoint: ${SOURCE_BOOTSTRAP}
  destinationKafkaCluster:
    kafkaRestClassRef:
      name: destination-kafka-rest-class
  mirrorTopics:
EOF
  for t in "${LINK_TOPICS[@]}"; do printf '    - name: %s\n' "${t}"; done
  cat <<EOF
  configs:
    consumer.offset.sync.enable: "false"
EOF
} > "${LINK_CR}"
k apply -f "${LINK_CR}"
for _ in $(seq 1 60); do
  k get clusterlink "${CLUSTER_LINK_NAME}" -o jsonpath='{.status.state}' 2>/dev/null | grep -q CREATED && break
  sleep 5
done

# --- Discover the destination cluster ID ---
DEST_CLUSTER_ID="$(k get kafka destination-kafka -o jsonpath='{.status.clusterID}' 2>/dev/null || echo '')"
[ -n "${DEST_CLUSTER_ID}" ] || { echo "FATAL: could not read destination cluster ID"; exit 1; }

# --- Gateway (dynamic, hot reload, swap auth to the destination) ---
e2e_stage_swap_auth_secrets
echo "Rendering + applying the gateway CR (post-TBM route)..."
GATEWAY_CR="${RENDERED_DIR}/gateway-route-conversion.yaml"
LINK_TOPICS_FLOW="$(printf '%s, ' "${LINK_TOPICS[@]}")"; LINK_TOPICS_FLOW="${LINK_TOPICS_FLOW%, }"
sed -e "s/__GATEWAY_NAME__/${GATEWAY_NAME}/g" \
    -e "s/__REPLICAS__/${GATEWAY_REPLICAS}/g" \
    -e "s|__GATEWAY_IMAGE__|${GATEWAY_IMAGE}|g" \
    -e "s|__INIT_IMAGE__|${INIT_IMAGE}|g" \
    -e "s/__ROUTE_NAME__/${ROUTE_NAME}/g" \
    -e "s|__SOURCE_BOOTSTRAP__|${SOURCE_BOOTSTRAP}|g" \
    -e "s|__DEST_BOOTSTRAP__|${DEST_BOOTSTRAP}|g" \
    -e "s/__LINK_TOPICS__/${LINK_TOPICS_FLOW}/g" \
    "${TEMPLATES_DIR}/gateway-route-conversion.yaml" > "${GATEWAY_CR}"
k apply -f "${GATEWAY_CR}"
echo "Waiting for ${GATEWAY_REPLICAS} gateway pod(s) to be Ready..."
wait_for_pods "app=${GATEWAY_NAME}"
echo "Checking the gateway accepted the licence..."
if k logs -l "app=${GATEWAY_NAME}" --tail=-1 2>/dev/null | grep -qi "Hot-reload feature is not enabled"; then
  echo "FATAL: gateway reports 'Hot-reload feature is not enabled' — licence not accepted as Enterprise." >&2
  exit 1
fi
echo "  ✓ no trial-mode hot-reload warning in gateway logs"

# --- Client truststore: trust the gateway's self-signed client-tls certificate ---
echo "Building the client truststore from the gateway keystore..."
TRUST_DIR="$(mktemp -d)"
k get secret client-tls -o 'jsonpath={.data.keystore\.jks}' | base64 -d > "${TRUST_DIR}/keystore.jks"
keytool -exportcert -alias gateway -keystore "${TRUST_DIR}/keystore.jks" -storepass changeit \
  -rfc -file "${TRUST_DIR}/gateway.pem" >/dev/null 2>&1
keytool -importcert -noprompt -alias gateway -file "${TRUST_DIR}/gateway.pem" \
  -keystore "${TRUST_DIR}/truststore.jks" -storepass changeit -storetype JKS >/dev/null 2>&1
apply_secret rc-gw-truststore --from-file=truststore.jks="${TRUST_DIR}/truststore.jks"
rm -rf "${TRUST_DIR}"

# --- Runner pod: hostAliases point the route's SNI names at the gateway Service ---
GATEWAY_IP=""
for _ in $(seq 1 60); do
  GATEWAY_IP="$(k get svc "${GATEWAY_NAME}" -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true)"
  [ -n "${GATEWAY_IP}" ] && break
  sleep 5
done
[ -n "${GATEWAY_IP}" ] || { echo "FATAL: gateway Service ${GATEWAY_NAME} has no ClusterIP" >&2; exit 1; }
echo "Deploying the ${RUNNER} pod (gateway Service ${GATEWAY_IP})..."
sed -e "s/__GATEWAY_IP__/${GATEWAY_IP}/g" "${TEMPLATES_DIR}/rc-runner.yaml" > "${RENDERED_DIR}/rc-runner.yaml"
kubectl --context "${PROFILE}" apply -f "${RENDERED_DIR}/rc-runner.yaml"
wait_for_pods "app=${RUNNER}"
for tool in tar curl kafka-console-producer kafka-get-offsets kafka-topics kafka-verifiable-producer kafka-verifiable-consumer; do
  k exec "${RUNNER}" -- sh -c "command -v ${tool}" >/dev/null 2>&1 || {
    echo "FATAL: ${tool} is not on the PATH of the ${RUNNER} image (cp-server:${CP_SERVER_TAG})" >&2; exit 1; }
done
echo "  ✓ ${RUNNER} has tar, curl and the Kafka client tools"

# --- Records on every link topic, before promotion ---
# Keyed records spread over every partition (murmur2 of k0..k299 is fixed, so the
# spread is the same on every run); the minimum per partition is checked below.
echo "Writing ${SEED_RECORDS} records to each link topic on the source..."
for t in "${LINK_TOPICS[@]}"; do
  seq 0 $((SEED_RECORDS - 1)) | awk '{print "k" $1 ":v" $1}' \
    | k exec -i "${RUNNER}" -- kafka-console-producer --bootstrap-server "${SOURCE_BOOTSTRAP}" \
        --topic "${t}" --property parse.key=true --property key.separator=: >/dev/null
done
for t in "${LINK_TOPICS[@]}"; do
  offsets="$(k exec "${RUNNER}" -- kafka-get-offsets --bootstrap-server "${SOURCE_BOOTSTRAP}" --topic "${t}" 2>/dev/null)"
  echo "${offsets}" | awk -F: -v min="${MIN_PARTITION_RECORDS}" -v t="${t}" '
    NF == 3 { n++; if ($3 < min) { print "FATAL: " t " partition " $2 " has only " $3 " records" > "/dev/stderr"; bad = 1 } }
    END { if (n == 0) { print "FATAL: no offsets read for " t > "/dev/stderr"; bad = 1 } exit bad }'
done
echo "  ✓ every link-topic partition holds at least ${MIN_PARTITION_RECORDS} records"

# --- Block reconcile once mirrors are visible over REST (from migration/setup.sh) ---
mirrors_url="${REST_ENDPOINT}/kafka/v3/clusters/${DEST_CLUSTER_ID}/links/${CLUSTER_LINK_NAME}/mirrors"
echo "Waiting for the ${LINK_TOPIC_COUNT} mirrors to appear over REST..."
got=0
for _ in $(seq 1 60); do
  got="$(rest GET "${mirrors_url}" 2>/dev/null | grep -o '"mirror_topic_name"' | wc -l | tr -d ' ')" || got=0
  [ "${got:-0}" -ge "${LINK_TOPIC_COUNT}" ] && break
  sleep 5
done
[ "${got:-0}" -ge "${LINK_TOPIC_COUNT}" ] || { echo "FATAL: only ${got} of ${LINK_TOPIC_COUNT} mirrors visible over REST" >&2; exit 1; }
echo "Blocking CFK reconciliation on the cluster link..."
k annotate clusterlink "${CLUSTER_LINK_NAME}" platform.confluent.io/block-reconcile=true --overwrite

# --- Promote every link mirror (what a finished topic-based migration leaves) ---
echo "Promoting every link mirror through the REST API..."
names="$(printf '"%s",' "${LINK_TOPICS[@]}")"
rest POST "${mirrors_url}:promote" "{\"mirror_topic_names\":[${names%,}]}" >/dev/null
stopped=0
for _ in $(seq 1 60); do
  stopped="$(rest GET "${mirrors_url}" 2>/dev/null | grep -o '"mirror_status": *"STOPPED"' | wc -l | tr -d ' ')" || stopped=0
  [ "${stopped:-0}" -ge "${LINK_TOPIC_COUNT}" ] && break
  sleep 5
done
[ "${stopped:-0}" -ge "${LINK_TOPIC_COUNT}" ] || { echo "FATAL: only ${stopped} of ${LINK_TOPIC_COUNT} mirrors reached STOPPED" >&2; exit 1; }
echo "  ✓ every link mirror is STOPPED"

# --- Prove clients can talk through the gateway (metadata call) ---
echo "Listing topics through the gateway (${GATEWAY_BOOTSTRAP})..."
k exec "${RUNNER}" -- sh -c 'printf "security.protocol=SSL\nssl.truststore.location=/etc/rc-gw-trust/truststore.jks\nssl.truststore.password=changeit\n" > /workspace/gw-client.properties'
listed="$(k exec "${RUNNER}" -- kafka-topics --bootstrap-server "${GATEWAY_BOOTSTRAP}" \
  --command-config /workspace/gw-client.properties --list 2>/dev/null || true)"
echo "${listed}" | grep -qx "$(topic_name 1)" || {
  echo "FATAL: a metadata call through the gateway did not list $(topic_name 1); got:" >&2
  echo "${listed}" >&2
  exit 1; }
echo "  ✓ metadata through the gateway lists the link topics"

# Credentials are files, not inline blocks; run.sh copies them into the runner.
printf 'unauthenticated_plaintext: {}\n' > "${RENDERED_DIR}/source-creds.yaml"
printf 'sasl_plain:\n  username: "%s"\n  password: "%s"\n  tls: true\ninsecure_skip_tls_verify: true\n' \
  "${DEST_SASL_USER}" "${DEST_SASL_PASSWORD}" > "${RENDERED_DIR}/dest-kafka-creds.yaml"
printf 'api_key: "%s"\napi_secret: "%s"\n' \
  "${DEST_SASL_USER}" "${DEST_SASL_PASSWORD}" > "${RENDERED_DIR}/link-creds.yaml"
chmod 600 "${RENDERED_DIR}"/*-creds.yaml

# --- Write .env (no secrets: the SASL password lives only in the k8s Secret) ---
{
  echo "# Generated by setup.sh - do not edit. Contains no secrets."
  echo "KCP_RC_KUBE_CONTEXT=${PROFILE}"
  echo "KCP_RC_NAMESPACE=${NAMESPACE}"
  echo "KCP_RC_RUNNER_POD=${RUNNER}"
  echo "KCP_RC_GATEWAY_NAME=${GATEWAY_NAME}"
  echo "KCP_RC_ROUTE_NAME=${ROUTE_NAME}"
  echo "KCP_RC_SOURCE_DOMAIN=${SOURCE_DOMAIN}"
  echo "KCP_RC_SOURCE_BOOTSTRAP_ID=${SOURCE_BOOTSTRAP_ID}"
  echo "KCP_RC_DEST_DOMAIN=${DEST_DOMAIN}"
  echo "KCP_RC_DEST_BOOTSTRAP_ID=${DEST_BOOTSTRAP_ID}"
  echo "KCP_RC_SOURCE_BOOTSTRAP=${SOURCE_BOOTSTRAP}"
  echo "KCP_RC_DEST_BOOTSTRAP=${DEST_BOOTSTRAP}"
  echo "KCP_RC_GATEWAY_BOOTSTRAP=${GATEWAY_BOOTSTRAP}"
  echo "KCP_RC_REST_ENDPOINT=${REST_ENDPOINT}"
  echo "KCP_RC_DEST_CLUSTER_ID=${DEST_CLUSTER_ID}"
  echo "KCP_RC_CLUSTER_LINK_NAME=${CLUSTER_LINK_NAME}"
  echo "KCP_RC_TOPIC_PREFIX=${TOPIC_PREFIX}"
  echo "KCP_RC_ORPHAN_TOPIC=${ORPHAN_TOPIC}"
} > "${ENV_FILE}"

echo ""
echo "=== Setup complete ==="
echo "Environment written to ${ENV_FILE}"
echo "Dest cluster ID: ${DEST_CLUSTER_ID}"
k get pods
echo ""
echo "Run tests with: make test-route-conversion-run"
