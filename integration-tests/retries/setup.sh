#!/usr/bin/env bash
# Stands up the live topology for the retries suite on its own Minikube profile
# (kcp-e2e-retries, see state.sh): a source CP Kafka (PLAINTEXT) and a destination CP
# Kafka (SASL_SSL) linked by one ClusterLink with mirror topics, a dynamic-route
# Confluent Gateway (hot reload), and a runner pod that can inject network faults
# (NET_ADMIN + iptables). The suite drives the real `kcp migration execute` from the
# runner pod and makes the source, the destination or the destination REST endpoint
# disappear from kcp's side while it runs.
#
# Self-contained: every script and manifest this uses is in this folder.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_DIR="${SCRIPT_DIR}/testdata/manifests"
TEMPLATES_DIR="${MANIFESTS_DIR}/templates"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"
# shellcheck source=env.sh
. "${SCRIPT_DIR}/env.sh"

NAMESPACE="confluent"

# --- Gateway / link / topic contract (single source of truth; mirrored into .env) ---
GATEWAY_NAME="${GATEWAY_NAME:-retries-gateway}"
GATEWAY_REPLICAS="${GATEWAY_REPLICAS:-2}"
ROUTE_NAME="${ROUTE_NAME:-retries-route}"
SOURCE_DOMAIN="source-domain"
DEST_DOMAIN="destination-domain"
CLUSTER_LINK_NAME="${CLUSTER_LINK_NAME:-retries-link}"
TOPIC_PREFIX="${TOPIC_PREFIX:-retries-topic-}"
# Each test promotes its own disjoint 5-topic slice; promotion is irreversible, so
# every topic must exist and be mirrored before the first test.
SOURCE_TOPIC_COUNT="${SOURCE_TOPIC_COUNT:-60}"
MIRRORED_COUNT="${MIRRORED_COUNT:-60}"
SOURCE_BOOTSTRAP="source-kafka.${NAMESPACE}.svc.cluster.local:9071"
DEST_BOOTSTRAP="destination-kafka.${NAMESPACE}.svc.cluster.local:9071"
REST_ENDPOINT="http://destination-kafka.${NAMESPACE}.svc.cluster.local:8090"

topic_name() { printf '%s%03d' "${TOPIC_PREFIX}" "$1"; }

echo "=== KCP retries E2E setup ==="
echo "Profile:        ${PROFILE}"
echo "CFK chart:      ${CFK_CHART_OCI} (${CFK_CHART_VERSION})"
echo "Gateway image:  ${GATEWAY_IMAGE} (dynamic mode)"
echo "Topics:         ${SOURCE_TOPIC_COUNT} source / ${MIRRORED_COUNT} mirrored"
echo ""

e2e_preflight
e2e_start_minikube
e2e_pull_images
e2e_install_operator
e2e_create_credentials
e2e_deploy_kafka

e2e_install_licence
e2e_create_client_tls
e2e_create_rest_credentials_secret

# --- Runner pod (also probes REST below). Its readiness waits for iptables. ---
echo "Deploying the kcp runner pod..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/kcp-runner.yaml"
wait_for_pods "app=kcp-runner"

# --- Source topics ---
echo "Creating ${SOURCE_TOPIC_COUNT} source topics..."
for i in $(seq 1 "${SOURCE_TOPIC_COUNT}"); do
  t="$(topic_name "$i")"
  sed -e "s/__TOPIC_NAME__/${t}/g" -e "s/__CLUSTER_REF__/source-kafka/g" \
    "${TEMPLATES_DIR}/test-topic.yaml" | kubectl --context "${PROFILE}" apply -f - >/dev/null
done
echo "Waiting for source topics to reach CREATED..."
for i in $(seq 1 "${SOURCE_TOPIC_COUNT}"); do
  t="$(topic_name "$i")"
  for _ in $(seq 1 60); do
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" get kafkatopic "${t}" \
      -o jsonpath='{.status.state}' 2>/dev/null | grep -q CREATED && break
    sleep 3
  done
done
echo "  ✓ source topics created"

e2e_apply_rest_class

# --- Cluster link mirroring 001..MIRRORED_COUNT. A dynamic route requires
# consumer.offset.sync.enable off (a reconcile precondition). ---
echo "Creating cluster link ${CLUSTER_LINK_NAME} mirroring ${MIRRORED_COUNT} topics..."
LINK_CR="${RENDERED_DIR}/cluster-link.yaml"
{
  cat <<YAML
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
YAML
  for i in $(seq 1 "${MIRRORED_COUNT}"); do printf '    - name: %s\n' "$(topic_name "$i")"; done
  cat <<YAML
  configs:
    consumer.offset.sync.enable: "false"
YAML
} > "${LINK_CR}"
kubectl --context "${PROFILE}" apply -f "${LINK_CR}"
for _ in $(seq 1 60); do
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" get clusterlink "${CLUSTER_LINK_NAME}" \
    -o jsonpath='{.status.state}' 2>/dev/null | grep -q CREATED && break
  sleep 5
done

DEST_CLUSTER_ID="$(kubectl --context "${PROFILE}" -n "${NAMESPACE}" get kafka destination-kafka -o jsonpath='{.status.clusterID}' 2>/dev/null || echo '')"
[ -n "${DEST_CLUSTER_ID}" ] || { echo "FATAL: could not read destination cluster ID"; exit 1; }

# --- Gateway (dynamic, hot reload) ---
echo "Rendering + applying the initial dynamic gateway CR..."
GATEWAY_CR="${RENDERED_DIR}/gateway-dynamic.yaml"
sed -e "s/__GATEWAY_NAME__/${GATEWAY_NAME}/g" \
    -e "s/__REPLICAS__/${GATEWAY_REPLICAS}/g" \
    -e "s|__GATEWAY_IMAGE__|${GATEWAY_IMAGE}|g" \
    -e "s|__INIT_IMAGE__|${INIT_IMAGE}|g" \
    -e "s/__ROUTE_NAME__/${ROUTE_NAME}/g" \
    -e "s|__SOURCE_BOOTSTRAP__|${SOURCE_BOOTSTRAP}|g" \
    -e "s|__DEST_BOOTSTRAP__|${DEST_BOOTSTRAP}|g" \
    "${TEMPLATES_DIR}/gateway-dynamic.yaml" > "${GATEWAY_CR}"
kubectl --context "${PROFILE}" apply -f "${GATEWAY_CR}"

echo "Waiting for ${GATEWAY_REPLICAS} gateway pod(s) to be Ready..."
wait_for_pods "app=${GATEWAY_NAME}"

# Without an Enterprise licence the config-file watcher never starts and every hot
# reload is silently dropped.
echo "Checking the gateway accepted the licence..."
if kubectl --context "${PROFILE}" -n "${NAMESPACE}" logs -l "app=${GATEWAY_NAME}" --tail=-1 2>/dev/null \
     | grep -qi "Hot-reload feature is not enabled"; then
  echo "FATAL: gateway reports 'Hot-reload feature is not enabled' — licence not accepted as Enterprise." >&2
  exit 1
fi
echo "  ✓ no trial-mode hot-reload warning in gateway logs"

# --- Block reconcile once mirrors are visible over REST ---
echo "Blocking CFK reconciliation on the cluster link..."
mirrors_url="${REST_ENDPOINT}/kafka/v3/clusters/${DEST_CLUSTER_ID}/links/${CLUSTER_LINK_NAME}/mirrors"
for _ in $(seq 1 60); do
  got="$(kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec kcp-runner -- \
    wget -qO- "${mirrors_url}" 2>/dev/null | grep -o mirror_topic_name | wc -l | tr -d ' ' || echo 0)"
  [ "${got:-0}" -ge "${MIRRORED_COUNT}" ] && break
  sleep 5
done
kubectl --context "${PROFILE}" -n "${NAMESPACE}" annotate clusterlink "${CLUSTER_LINK_NAME}" \
  platform.confluent.io/block-reconcile=true --overwrite

# Credentials are files the manifests reference, rendered here (gitignored) and copied
# into the runner pod by run.sh. The destination SASL secret lives only here.
printf 'unauthenticated_plaintext: {}\n' > "${RENDERED_DIR}/source-creds.yaml"
printf 'sasl_plain:\n  username: "%s"\n  password: "%s"\n  tls: true\ninsecure_skip_tls_verify: true\n' \
  "${DEST_SASL_USER}" "${DEST_SASL_PASSWORD}" > "${RENDERED_DIR}/dest-kafka-creds.yaml"
printf 'sasl_plain:\n  username: "%s"\n  password: "%s"\n  tls: true\ninsecure_skip_tls_verify: true\n' \
  "${DEST_SASL_USER}" "not-the-password" > "${RENDERED_DIR}/dest-kafka-creds-wrong.yaml"
printf 'api_key: "%s"\napi_secret: "%s"\n' \
  "${DEST_SASL_USER}" "${DEST_SASL_PASSWORD}" > "${RENDERED_DIR}/link-creds.yaml"

# --- Write .env (no secrets: the SASL password lives only in the k8s Secret) ---
{
  echo "# Generated by setup.sh - do not edit. Contains no secrets."
  echo "KCP_RETRIES_KUBE_CONTEXT=${PROFILE}"
  echo "KCP_RETRIES_NAMESPACE=${NAMESPACE}"
  echo "KCP_RETRIES_KCP_POD=kcp-runner"
  echo "KCP_RETRIES_GATEWAY_NAME=${GATEWAY_NAME}"
  echo "KCP_RETRIES_ROUTE_NAME=${ROUTE_NAME}"
  echo "KCP_RETRIES_SOURCE_DOMAIN=${SOURCE_DOMAIN}"
  echo "KCP_RETRIES_DEST_DOMAIN=${DEST_DOMAIN}"
  echo "KCP_RETRIES_SOURCE_BOOTSTRAP=${SOURCE_BOOTSTRAP}"
  echo "KCP_RETRIES_DEST_BOOTSTRAP=${DEST_BOOTSTRAP}"
  echo "KCP_RETRIES_REST_ENDPOINT=${REST_ENDPOINT}"
  echo "KCP_RETRIES_DEST_CLUSTER_ID=${DEST_CLUSTER_ID}"
  echo "KCP_RETRIES_CLUSTER_LINK_NAME=${CLUSTER_LINK_NAME}"
  echo "KCP_RETRIES_TOPIC_PREFIX=${TOPIC_PREFIX}"
} > "${ENV_FILE}"

echo ""
echo "=== Setup complete ==="
echo "Environment written to ${ENV_FILE}"
echo "Dest cluster ID: ${DEST_CLUSTER_ID}"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pods
echo ""
echo "Run tests with: ${SCRIPT_DIR}/run.sh [TestNameRegex]"
