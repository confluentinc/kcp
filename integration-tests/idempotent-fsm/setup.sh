#!/usr/bin/env bash
# Stands up the live topology for the idempotent-migration-FSM resume suite on its
# own Minikube profile (kcp-e2e-idempotent-<mode>, see state.sh): a source CP Kafka (PLAINTEXT) and a
# destination CP Kafka (SASL_SSL) linked by one ClusterLink, plus a Confluent
# Gateway — dynamic-mode (hot reload) or static-mode (rollout), per GATEWAY_MODE.
# The engine (migplan.Reconcile) reads the live gateway CR; the harness drives
# kcp migration execute and interrupts it at kill points to prove resume.
#
# Composition (see the plan): the cluster/operator/gateway-image spine is copied
# from integration-tests/migration (chart 0.1838.0, cpc-gateway 1.4.0-master-*,
# SASL_SSL destination, cluster link, REST proxy); the licence/configId/hotReload
# plumbing is copied from integration-tests/migration-hot-reload. The steps this
# suite shares with integration-tests/route-conversion (version pins, minikube,
# images, operator, clusters, licence, client-tls, REST credential, REST class,
# swap-auth secrets) live in ../lib/cp-e2e-env.sh, which both setups source.
#
# Why chart 0.1838.0 and NOT hot-reload's 0.1718.34: 0.1718.34 predates the
# dynamic-route CRD (mode: dynamic, plural streamingDomains, rules.routing,
# unmatchedTopics). 0.1838.0 has BOTH the dynamic-route CRD and spec.configId, so
# it is the only pin that supports this suite. The configId guard below fails fast
# if that assumption is ever wrong.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_DIR="${SCRIPT_DIR}/testdata/manifests"
TEMPLATES_DIR="${MANIFESTS_DIR}/templates"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"
# shellcheck source=../lib/cp-e2e-env.sh
. "${SCRIPT_DIR}/../lib/cp-e2e-env.sh"

NAMESPACE="confluent"

# --- Gateway / link / topic contract (single source of truth; mirrored into .env) ---
# GATEWAY_MODE selects the route mode under test: dynamic (TBM) or static (AAO).
# The route/domain/gateway names differ per mode and must match the deployed
# gateway template (gateway-dynamic.yaml / gateway-static.yaml).
if [ "${GATEWAY_MODE}" = "static" ]; then
  GATEWAY_NAME="${GATEWAY_NAME:-aao-gateway}"
  GATEWAY_REPLICAS="${GATEWAY_REPLICAS:-1}"
  ROUTE_NAME="${ROUTE_NAME:-migration-route}"
  SOURCE_DOMAIN="source-kafka-cluster"
  DEST_DOMAIN="destination-kafka-cluster"
  # The static route binds streamingDomain by {name, bootstrapServerId}. The
  # route-reset helper (static-only) flips streamingDomain back to source
  # between migrations, so it needs the source domain's bootstrap-server id
  # (matches gateway-static.yaml's source-kafka-cluster UNAUTHED endpoint).
  SOURCE_BOOTSTRAP_ID="UNAUTHED"
else
  GATEWAY_NAME="${GATEWAY_NAME:-tbm-gateway}"
  GATEWAY_REPLICAS="${GATEWAY_REPLICAS:-2}"
  ROUTE_NAME="${ROUTE_NAME:-tbm-route}"
  SOURCE_DOMAIN="source-domain"
  DEST_DOMAIN="destination-domain"
  SOURCE_BOOTSTRAP_ID=""  # dynamic routes bind per-topic via rules, no streamingDomain
fi
CLUSTER_LINK_NAME="${CLUSTER_LINK_NAME:-tbm-link}"
TOPIC_PREFIX="${TOPIC_PREFIX:-tbm-topic-}"
# Each kill-point test promotes a disjoint 5-topic slice in 031..085 (see the
# *_test.go files); promotion is irreversible, so all must exist AND be mirrored.
# The pool runs 001..085, all mirrored; 001..030 are unused headroom.
SOURCE_TOPIC_COUNT="${SOURCE_TOPIC_COUNT:-85}"   # tbm-topic-001..085 on source
MIRRORED_COUNT="${MIRRORED_COUNT:-85}"           # 001..085 all mirrored on the link
# Exists on BOTH source and destination as standalone topics (mirror of neither) —
# the input for the "exists on target but is not a mirror" halt, which verdict.go
# classifies only when a topic is onSource && MirrorNone && onTarget.
ORPHAN_TOPIC="${ORPHAN_TOPIC:-tbm-orphan}"
SOURCE_BOOTSTRAP="source-kafka.${NAMESPACE}.svc.cluster.local:9071"
DEST_BOOTSTRAP="destination-kafka.${NAMESPACE}.svc.cluster.local:9071"
REST_ENDPOINT="http://destination-kafka.${NAMESPACE}.svc.cluster.local:8090"

# topic_name — zero-padded source topic name for an index (1 -> tbm-topic-001).
topic_name() { printf '%s%03d' "${TOPIC_PREFIX}" "$1"; }

echo "=== KCP idempotent-fsm E2E setup ==="
echo "Profile:        ${PROFILE}"
echo "CFK chart:      ${CFK_CHART_OCI} (${CFK_CHART_VERSION})"
echo "Gateway image:  ${GATEWAY_IMAGE} (${GATEWAY_MODE} mode)"
echo "Topics:         ${SOURCE_TOPIC_COUNT} source / ${MIRRORED_COUNT} mirrored"
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

# --- Runner pod (deployed here so its built-in wget can probe REST below) ---
# Spec + RBAC + REST-cred env are authored in kcp-runner.yaml; the tbm-rest-credentials
# secret it references exists by now. run.sh later cp's binaries into this pod.
echo "Deploying the kcp runner pod..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/kcp-runner.yaml"
wait_for_pods "app=kcp-runner"

# --- Source topics (001..NNN) + one destination-only non-mirror topic ---
echo "Creating ${SOURCE_TOPIC_COUNT} source topics + ${ORPHAN_TOPIC} (both clusters)..."
for i in $(seq 1 "${SOURCE_TOPIC_COUNT}"); do
  t="$(topic_name "$i")"
  sed -e "s/__TOPIC_NAME__/${t}/g" -e "s/__CLUSTER_REF__/source-kafka/g" \
    "${TEMPLATES_DIR}/test-topic.yaml" | kubectl --context "${PROFILE}" apply -f - >/dev/null
done
# Two KafkaTopic CRs with distinct metadata.name but a shared spec.name, so the same
# Kafka topic (ORPHAN_TOPIC) exists on both clusters (metadata.name must be unique per
# namespace; spec.name is the actual topic name).
for ref in source-kafka destination-kafka; do
  kubectl --context "${PROFILE}" apply -f - >/dev/null <<EOF
apiVersion: platform.confluent.io/v1beta1
kind: KafkaTopic
metadata:
  name: ${ORPHAN_TOPIC}-${ref}
  namespace: ${NAMESPACE}
spec:
  name: ${ORPHAN_TOPIC}
  replicas: 1
  partitionCount: 1
  kafkaClusterRef:
    name: ${ref}
EOF
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

# --- KafkaRestClass (shared) ---
e2e_apply_rest_class

# --- Cluster link mirroring 001..MIRRORED_COUNT ---
# consumer.offset.sync.enable: a dynamic route requires it OFF (a reconcile
# precondition). A static route doesn't check it; the static suite starts it ON
# so the offset-sync pause test can observe kcp disabling and restoring it.
if [ "${GATEWAY_MODE}" = "static" ]; then
  LINK_OFFSET_SYNC="true"
else
  LINK_OFFSET_SYNC="false"
fi
echo "Creating cluster link ${CLUSTER_LINK_NAME} mirroring ${MIRRORED_COUNT} topics (consumer.offset.sync.enable=${LINK_OFFSET_SYNC})..."
LINK_CR="${RENDERED_DIR}/cluster-link.yaml"
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
  for i in $(seq 1 "${MIRRORED_COUNT}"); do printf '    - name: %s\n' "$(topic_name "$i")"; done
  cat <<EOF
  configs:
    consumer.offset.sync.enable: "${LINK_OFFSET_SYNC}"
EOF
} > "${LINK_CR}"
kubectl --context "${PROFILE}" apply -f "${LINK_CR}"
for _ in $(seq 1 60); do
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" get clusterlink "${CLUSTER_LINK_NAME}" \
    -o jsonpath='{.status.state}' 2>/dev/null | grep -q CREATED && break
  sleep 5
done

# --- Discover the destination cluster ID ---
DEST_CLUSTER_ID="$(kubectl --context "${PROFILE}" -n "${NAMESPACE}" get kafka destination-kafka -o jsonpath='{.status.clusterID}' 2>/dev/null || echo '')"
[ -n "${DEST_CLUSTER_ID}" ] || { echo "FATAL: could not read destination cluster ID"; exit 1; }

if [ "${GATEWAY_MODE}" = "static" ]; then
  # --- Gateway (static route, AAO — swap-auth staging ported from migration/setup.sh) ---
  e2e_stage_swap_auth_secrets

  echo "Rendering + applying the initial static gateway CR..."
  GATEWAY_CR="${RENDERED_DIR}/gateway-static.yaml"
  sed -e "s/__GATEWAY_NAME__/${GATEWAY_NAME}/g" \
      -e "s|__GATEWAY_IMAGE__|${GATEWAY_IMAGE}|g" \
      -e "s|__INIT_IMAGE__|${INIT_IMAGE}|g" \
      "${TEMPLATES_DIR}/gateway-static.yaml" > "${GATEWAY_CR}"
  kubectl --context "${PROFILE}" apply -f "${GATEWAY_CR}"
  echo "Waiting for ${GATEWAY_REPLICAS} gateway pod(s) to be Ready..."
  wait_for_pods "app=${GATEWAY_NAME}"
else
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

  # Prove the licence took: without an Enterprise licence the config-file watcher
  # never starts and every hot reload is silently dropped (from hot-reload/setup.sh).
  echo "Checking the gateway accepted the licence..."
  if kubectl --context "${PROFILE}" -n "${NAMESPACE}" logs -l "app=${GATEWAY_NAME}" --tail=-1 2>/dev/null \
       | grep -qi "Hot-reload feature is not enabled"; then
    echo "FATAL: gateway reports 'Hot-reload feature is not enabled' — licence not accepted as Enterprise." >&2
    exit 1
  fi
  echo "  ✓ no trial-mode hot-reload warning in gateway logs"
fi

# --- Block reconcile once mirrors are visible over REST (from migration/setup.sh) ---
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

# Credentials are files, not inline blocks. The manifests reference three shared
# credentials files at /workspace/rendered/; render them here (into .rendered/,
# gitignored) so run.sh cp's them into the runner pod alongside the manifests.
# The destination SASL secret lives only here, never committed.
printf 'unauthenticated_plaintext: {}\n' > "${RENDERED_DIR}/source-creds.yaml"
printf 'sasl_plain:\n  username: "%s"\n  password: "%s"\n  tls: true\ninsecure_skip_tls_verify: true\n' \
  "${DEST_SASL_USER}" "${DEST_SASL_PASSWORD}" > "${RENDERED_DIR}/dest-kafka-creds.yaml"
printf 'api_key: "%s"\napi_secret: "%s"\n' \
  "${DEST_SASL_USER}" "${DEST_SASL_PASSWORD}" > "${RENDERED_DIR}/link-creds.yaml"
# --- Write .env (no secrets: the SASL password lives only in the k8s Secret) ---
{
  echo "# Generated by setup.sh - do not edit. Contains no secrets."
  echo "KCP_TBM_KUBECONFIG=${KUBECONFIG_PATH}"
  echo "KCP_TBM_KUBE_CONTEXT=${PROFILE}"
  echo "KCP_TBM_NAMESPACE=${NAMESPACE}"
  echo "KCP_TBM_KCP_POD=kcp-runner"
  echo "KCP_TBM_RENDERED_DIR=${RENDERED_DIR}"
  echo "KCP_TBM_GATEWAY_MODE=${GATEWAY_MODE}"
  echo "KCP_TBM_GATEWAY_NAME=${GATEWAY_NAME}"
  echo "KCP_TBM_ROUTE_NAME=${ROUTE_NAME}"
  echo "KCP_TBM_SOURCE_DOMAIN=${SOURCE_DOMAIN}"
  echo "KCP_TBM_SOURCE_BOOTSTRAP_ID=${SOURCE_BOOTSTRAP_ID}"
  echo "KCP_TBM_DEST_DOMAIN=${DEST_DOMAIN}"
  echo "KCP_TBM_SOURCE_BOOTSTRAP=${SOURCE_BOOTSTRAP}"
  echo "KCP_TBM_DEST_BOOTSTRAP=${DEST_BOOTSTRAP}"
  echo "KCP_TBM_REST_ENDPOINT=${REST_ENDPOINT}"
  echo "KCP_TBM_DEST_CLUSTER_ID=${DEST_CLUSTER_ID}"
  echo "KCP_TBM_CLUSTER_LINK_NAME=${CLUSTER_LINK_NAME}"
  echo "KCP_TBM_TOPIC_PREFIX=${TOPIC_PREFIX}"
} > "${ENV_FILE}"

echo ""
echo "=== Setup complete ==="
echo "Environment written to ${ENV_FILE}"
echo "Dest cluster ID: ${DEST_CLUSTER_ID}"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pods
echo ""
echo "Run tests with: GATEWAY_MODE=${GATEWAY_MODE} make test-idempotent-fsm-run"
