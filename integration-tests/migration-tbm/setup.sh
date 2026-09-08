#!/usr/bin/env bash
# Stands up the live topology for the TBM hot-reload integration suite on its own
# Minikube profile (kcp-e2e-tbm): a source CP Kafka (PLAINTEXT) and a destination
# CP Kafka (SASL_SSL) linked by one ClusterLink, plus a DYNAMIC-mode Confluent
# Gateway with hot reload. The engine (migplan.Reconcile) reads the live gateway
# CR, and the harness re-applies its rendered fence/switchover route edits with a
# bumped spec.configId (hot reload, no pod roll) between mirror promotions.
#
# Composition (see the plan): the cluster/operator/gateway-image spine is copied
# from integration-tests/migration (chart 0.1838.0, cpc-gateway 1.4.0-master-*,
# SASL_SSL destination, cluster link, REST proxy); the licence/configId/hotReload
# plumbing is copied from integration-tests/migration-hot-reload.
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
BATCHES_DIR="${SCRIPT_DIR}/testdata/batches"
RENDERED_DIR="${SCRIPT_DIR}/.rendered"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

PROFILE="${PROFILE:-kcp-e2e-tbm}"
NAMESPACE="confluent"

# --- Version pins (spine from integration-tests/migration) ---
AWS_REGION="${AWS_REGION:-us-east-1}"
CFK_CHART_OCI="${CFK_CHART_OCI:-oci://635910096382.dkr.ecr.us-east-1.amazonaws.com/kcp/confluent-for-kubernetes}"
CFK_CHART_VERSION="${CFK_CHART_VERSION:-0.1838.0}"
OPERATOR_IMAGE="${OPERATOR_IMAGE:-635910096382.dkr.ecr.us-east-1.amazonaws.com/kcp/confluent-operator:v0.1838.0-amd64}"
OPERATOR_REGISTRY="${OPERATOR_IMAGE%%/*}"
OPERATOR_REPO_TAG="${OPERATOR_IMAGE#*/}"
OPERATOR_REPO="${OPERATOR_REPO_TAG%:*}"
OPERATOR_TAG="${OPERATOR_REPO_TAG##*:}"
# The dynamic-routing gateway build. 1.4.0-master-430 is the proven dynamic build
# (see ~/gateway-testing/gateway-dynamic-local.yaml). Native-arch tag so the
# gateway pod needs no emulation; only the operator image is amd64-only.
case "$(uname -m)" in
  arm64 | aarch64) GATEWAY_TAG_DEFAULT="1.4.0-master-430-arm64" ;;
  *) GATEWAY_TAG_DEFAULT="1.4.0-master-424-amd64" ;;
esac
GATEWAY_TAG="${GATEWAY_TAG:-${GATEWAY_TAG_DEFAULT}}"
GATEWAY_IMAGE="${GATEWAY_IMAGE:-635910096382.dkr.ecr.us-east-1.amazonaws.com/kcp/cpc-gateway:${GATEWAY_TAG}}"
INIT_IMAGE="${INIT_IMAGE:-confluentinc/confluent-init-container:3.3.0}"
CP_SERVER_TAG="${CP_SERVER_TAG:-8.1.2}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-900s}"

# --- Gateway / link / topic contract (single source of truth; mirrored into .env) ---
GATEWAY_NAME="${GATEWAY_NAME:-tbm-gateway}"
GATEWAY_REPLICAS="${GATEWAY_REPLICAS:-2}"
ROUTE_NAME="${ROUTE_NAME:-tbm-route}"
SOURCE_DOMAIN="source-domain"
DEST_DOMAIN="destination-domain"
CLUSTER_LINK_NAME="${CLUSTER_LINK_NAME:-tbm-link}"
TOPIC_PREFIX="${TOPIC_PREFIX:-tbm-topic-}"
SOURCE_TOPIC_COUNT="${SOURCE_TOPIC_COUNT:-55}"   # tbm-topic-001..055 on source
MIRRORED_COUNT="${MIRRORED_COUNT:-48}"           # 001..048 mirrored on the link
SUCCESS_HI="${SUCCESS_HI:-44}"                    # 001..044 selectable by success batches
RESERVED_TOPIC="${RESERVED_TOPIC:-45}"           # 045 mirrored, reserved for promoted-not-switched halt
# Exists on BOTH source and destination as standalone topics (mirror of neither) —
# the input for the "exists on target but is not a mirror" halt, which verdict.go
# classifies only when a topic is onSource && MirrorNone && onTarget.
ORPHAN_TOPIC="${ORPHAN_TOPIC:-tbm-orphan}"
SOURCE_BOOTSTRAP="source-kafka.${NAMESPACE}.svc.cluster.local:9071"
DEST_BOOTSTRAP="destination-kafka.${NAMESPACE}.svc.cluster.local:9071"
REST_ENDPOINT="http://destination-kafka.${NAMESPACE}.svc.cluster.local:8090"
# Well-known throwaway test credential for the destination SASL/REST leg. Matches
# destination-credentials.yaml (testuser/testpassword). Delivered to the runner
# via the tbm-rest-credentials Secret (pod-spec env), never on a kubectl exec argv.
DEST_SASL_USER="${DEST_SASL_USER:-testuser}"
DEST_SASL_PASSWORD="${DEST_SASL_PASSWORD:-testpassword}"

# --- Licence knobs (from integration-tests/migration-hot-reload) ---
LICENSE_SECRET_ID="${LICENSE_SECRET_ID:-kcp/e2e/gateway-license}"
GATEWAY_LICENSE_GCP_SECRET="${GATEWAY_LICENSE_GCP_SECRET:-}"

# --- ECR credential knobs (CI path; no-op locally where an ambient AWS profile is used) ---
ECR_AWS_KEY_GCP_SECRET="${ECR_AWS_KEY_GCP_SECRET:-}"
ECR_AWS_SECRET_GCP_SECRET="${ECR_AWS_SECRET_GCP_SECRET:-}"

# Two CP clusters + gateway JVMs + operator need more headroom than migration's
# single-purpose profiles.
MINIKUBE_CPUS="${MINIKUBE_CPUS:-4}"
MINIKUBE_MEMORY="${MINIKUBE_MEMORY:-12288}"

# ensure_ecr_aws_creds — populate AWS creds from GCP Secret Manager when the CI
# knobs are set, else leave the ambient AWS profile untouched. Idempotent. Copied
# from integration-tests/migration/setup.sh; see it for why the session token is
# cleared.
ensure_ecr_aws_creds() {
  [ -n "${_ecr_aws_creds_ready:-}" ] && return 0
  if [ -n "${ECR_AWS_KEY_GCP_SECRET}" ] && [ -n "${ECR_AWS_SECRET_GCP_SECRET}" ]; then
    command -v gcloud >/dev/null 2>&1 || {
      echo "FATAL: ECR_AWS_*_GCP_SECRET is set but the gcloud CLI is not on PATH" >&2; exit 1; }
    AWS_ACCESS_KEY_ID="$(gcloud secrets versions access latest --secret="${ECR_AWS_KEY_GCP_SECRET}")"
    AWS_SECRET_ACCESS_KEY="$(gcloud secrets versions access latest --secret="${ECR_AWS_SECRET_GCP_SECRET}")"
    if [ -z "${AWS_ACCESS_KEY_ID}" ] || [ -z "${AWS_SECRET_ACCESS_KEY}" ]; then
      echo "FATAL: ECR credentials read from GCP Secret Manager are empty" >&2; exit 1; fi
    export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
    unset AWS_SESSION_TOKEN AWS_PROFILE AWS_DEFAULT_PROFILE
  fi
  _ecr_aws_creds_ready=1
}

# wait_for_pods — wait for at least one matching pod, then for all to be Ready.
wait_for_pods() {
  local label="$1"
  echo "  Waiting for pods with label ${label} to appear..."
  until kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pod -l "${label}" --no-headers 2>/dev/null | grep -q .; do
    sleep 5
  done
  echo "  Waiting for pods with label ${label} to be Ready (timeout: ${WAIT_TIMEOUT})..."
  local deadline=$((SECONDS + ${WAIT_TIMEOUT%s}))
  while true; do
    if kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=Ready pod -l "${label}" --timeout=15s 2>/dev/null; then
      echo "  ✓ Pods with label ${label} are Ready"; return 0; fi
    if [ $SECONDS -ge $deadline ]; then
      echo "  ✗ Timed out waiting for pods with label ${label}"
      kubectl --context "${PROFILE}" -n "${NAMESPACE}" describe pod -l "${label}" | tail -30
      return 1; fi
    echo "  [$(date +%H:%M:%S)] Pods with label ${label}:"
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pod -l "${label}" -o wide --no-headers 2>/dev/null || true
  done
}

# topic_name — zero-padded source topic name for an index (1 -> tbm-topic-001).
topic_name() { printf '%s%03d' "${TOPIC_PREFIX}" "$1"; }

echo "=== KCP TBM hot-reload E2E setup ==="
echo "Profile:        ${PROFILE}"
echo "CFK chart:      ${CFK_CHART_OCI} (${CFK_CHART_VERSION})"
echo "Gateway image:  ${GATEWAY_IMAGE} (dynamic mode, hot reload)"
echo "Topics:         ${SOURCE_TOPIC_COUNT} source / ${MIRRORED_COUNT} mirrored / success 001..$(printf '%03d' "${SUCCESS_HI}") / reserved $(printf '%03d' "${RESERVED_TOPIC}")"
echo ""

# --- Preflight ---
for bin in minikube kubectl helm docker openssl keytool; do
  command -v "$bin" >/dev/null 2>&1 || { echo "FATAL: ${bin} is required but not on PATH"; exit 1; }
done

# The licence is the one input with no in-cluster substitute (from hot-reload).
if [ -n "${GATEWAY_LICENSE_KEY:-}" ]; then
  echo "  ✓ using licence from GATEWAY_LICENSE_KEY"
elif [ -n "${GATEWAY_LICENSE_GCP_SECRET:-}" ]; then
  command -v gcloud >/dev/null 2>&1 || { echo "FATAL: GATEWAY_LICENSE_GCP_SECRET set but gcloud not on PATH" >&2; exit 1; }
  gcloud auth print-access-token >/dev/null 2>&1 || { echo "FATAL: no active gcloud credential to read ${GATEWAY_LICENSE_GCP_SECRET}" >&2; exit 1; }
  echo "  ✓ gcloud credential present; licence from GCP secret ${GATEWAY_LICENSE_GCP_SECRET}"
else
  command -v aws >/dev/null 2>&1 || { echo "FATAL: GATEWAY_LICENSE_KEY unset and aws CLI not on PATH for ${LICENSE_SECRET_ID}" >&2; exit 1; }
  aws sts get-caller-identity >/dev/null 2>&1 || {
    echo "FATAL: GATEWAY_LICENSE_KEY unset and no working AWS credentials to read ${LICENSE_SECRET_ID}." >&2
    echo "       Authenticate (e.g. 'sso') or export GATEWAY_LICENSE_KEY with a CP Enterprise licence." >&2
    exit 1; }
  echo "  ✓ AWS credentials present; licence from ${LICENSE_SECRET_ID}"
fi

# --- Minikube ---
if minikube status --profile "${PROFILE}" &>/dev/null; then
  echo "Reusing existing minikube profile '${PROFILE}'..."
else
  echo "Starting minikube..."
  minikube start --profile "${PROFILE}" --driver=docker \
    --cpus="${MINIKUBE_CPUS}" --memory="${MINIKUBE_MEMORY}" \
    --disk-size=25g --kubernetes-version=v1.30.0
fi
eval "$(minikube docker-env --profile "${PROFILE}" 2>/dev/null || true)"
KUBECONFIG_PATH="${HOME}/.kube/config"

# --- Images (private operator + gateway from ECR; public CP images in background) ---
echo "Authenticating to ECR..."
ensure_ecr_aws_creds
aws ecr get-login-password --region "${AWS_REGION}" \
  | docker login --username AWS --password-stdin "${OPERATOR_REGISTRY}" >/dev/null
echo "Pulling operator (amd64) and gateway images..."
docker pull --platform linux/amd64 "${OPERATOR_IMAGE}" >/dev/null
docker pull "${GATEWAY_IMAGE}" >/dev/null
echo "Pre-pulling public images in background..."
docker pull "docker.io/confluentinc/cp-server:${CP_SERVER_TAG}" &>/dev/null &
docker pull "docker.io/${INIT_IMAGE}" &>/dev/null &
docker pull "docker.io/alpine:3.20" &>/dev/null &

# --- Namespace ---
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/namespace.yaml"

# --- CFK operator (chart from ECR OCI mirror) ---
echo "Pulling CFK chart ${CFK_CHART_VERSION} from the ECR mirror..."
CHART_REGISTRY="${CFK_CHART_OCI#oci://}"; CHART_REGISTRY="${CHART_REGISTRY%%/*}"
aws ecr get-login-password --region "${AWS_REGION}" \
  | helm registry login --username AWS --password-stdin "${CHART_REGISTRY}" >/dev/null
CHART_UNPACK_DIR="$(mktemp -d)"
helm pull "${CFK_CHART_OCI}" --version "${CFK_CHART_VERSION}" --untar --untardir "${CHART_UNPACK_DIR}" >/dev/null
CFK_CHART_PATH="${CHART_UNPACK_DIR}/$(basename "${CFK_CHART_OCI}")"

echo "Installing CFK operator..."
if helm status confluent-operator --namespace "${NAMESPACE}" --kube-context "${PROFILE}" &>/dev/null; then
  echo "CFK operator already installed, skipping..."
else
  helm install confluent-operator "${CFK_CHART_PATH}" \
    --namespace "${NAMESPACE}" --kube-context "${PROFILE}" \
    --set namespaced=false \
    --set "image.registry=${OPERATOR_REGISTRY}" \
    --set "image.repository=${OPERATOR_REPO}" \
    --set "image.tag=${OPERATOR_TAG}" \
    --set image.pullPolicy=IfNotPresent \
    --wait --timeout "${WAIT_TIMEOUT}"
fi
wait_for_pods "app=confluent-operator"

# Fail loudly if the CRD cannot express hot reload, rather than letting kcp
# quietly pick rollout verification (guard copied from hot-reload/setup.sh).
if [ -z "$(kubectl --context "${PROFILE}" get crd gateways.platform.confluent.io \
      -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.configId}' 2>/dev/null)" ]; then
  echo "FATAL: the installed Gateway CRD does not declare spec.configId — hot reload cannot be verified" >&2
  exit 1
fi
echo "  ✓ installed CRD declares spec.configId"

# --- Credentials + CA (before destination Kafka, which auto-generates certs) ---
echo "Creating credentials..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/source-credentials.yaml"
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/destination-credentials.yaml"
if ! kubectl --context "${PROFILE}" -n "${NAMESPACE}" get secret ca-pair-sslcerts &>/dev/null; then
  echo "Generating CA keypair for auto-generated TLS certs..."
  openssl req -x509 -newkey rsa:2048 -keyout /tmp/tbm-ca-key.pem -out /tmp/tbm-ca-cert.pem \
    -days 365 -nodes -subj "/CN=KCPTBMTestCA" 2>/dev/null
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret tls ca-pair-sslcerts \
    --cert=/tmp/tbm-ca-cert.pem --key=/tmp/tbm-ca-key.pem
  rm -f /tmp/tbm-ca-key.pem /tmp/tbm-ca-cert.pem
fi

# --- KRaft controllers + Kafka brokers (parallel) ---
echo "Deploying KRaft controllers..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/source-kraftcontroller.yaml"
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/destination-kraftcontroller.yaml"
wait_for_pods "app=source-kraftcontroller" & p1=$!
wait_for_pods "app=destination-kraftcontroller" & p2=$!
wait $p1 $p2 || { echo "FATAL: KRaft controllers failed to start"; exit 1; }

echo "Deploying Kafka brokers..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/source-kafka.yaml"
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/destination-kafka.yaml"
wait_for_pods "app=source-kafka" & p1=$!
wait_for_pods "app=destination-kafka" & p2=$!
wait $p1 $p2 || { echo "FATAL: Kafka brokers failed to start"; exit 1; }

# --- Secrets: licence (stdin pipe), gateway client-tls, runner REST creds ---
# Licence JWT reaches the cluster through a pipe — never argv, temp file, or echo
# (from hot-reload/setup.sh).
create_license_secret() {
  local manifest
  if [ -n "${GATEWAY_LICENSE_KEY:-}" ]; then
    manifest="$(printf '%s' "${GATEWAY_LICENSE_KEY}" \
      | kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic gateway-license \
          --from-file=license.txt=/dev/stdin --dry-run=client -o yaml)"
  elif [ -n "${GATEWAY_LICENSE_GCP_SECRET:-}" ]; then
    manifest="$(gcloud secrets versions access latest --secret="${GATEWAY_LICENSE_GCP_SECRET}" \
      | kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic gateway-license \
          --from-file=license.txt=/dev/stdin --dry-run=client -o yaml)"
  else
    manifest="$(aws secretsmanager get-secret-value --secret-id "${LICENSE_SECRET_ID}" \
        --region "${AWS_REGION}" --query SecretString --output text \
      | kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic gateway-license \
          --from-file=license.txt=/dev/stdin --dry-run=client -o yaml)"
  fi
  printf '%s' "${manifest}" | kubectl --context "${PROFILE}" apply -f - >/dev/null
}
echo "Installing gateway licence..."
create_license_secret
echo "  ✓ secret/gateway-license created (contents not logged)"

# Self-signed server keystore for the gateway's client->gateway TLS. A dynamic
# route mandates host/SNI, which mandates client TLS; no client ever connects in
# this suite, but the secret must exist and hold a valid keystore or the gateway
# crashloops. Written under .rendered/ (gitignored), never committed.
mkdir -p "${RENDERED_DIR}"
if ! kubectl --context "${PROFILE}" -n "${NAMESPACE}" get secret client-tls &>/dev/null; then
  echo "Generating gateway client-tls keystore..."
  KS="${RENDERED_DIR}/gateway-keystore.jks"
  rm -f "${KS}"
  keytool -genkeypair -alias gateway -keyalg RSA -keysize 2048 -validity 365 \
    -dname "CN=tbm-gateway" \
    -ext "SAN=dns:bootstrap.gw.local,dns:*.gw.local,dns:${GATEWAY_NAME}.${NAMESPACE}.svc.cluster.local" \
    -keystore "${KS}" -storepass changeit -keypass changeit -storetype JKS >/dev/null 2>&1
  # CFK requires the key=value form, not a bare password.
  printf 'jksPassword=changeit' > "${RENDERED_DIR}/jksPassword.txt"
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic client-tls \
    --from-file=keystore.jks="${KS}" \
    --from-file=jksPassword.txt="${RENDERED_DIR}/jksPassword.txt"
fi

# Destination REST/SASL credential for the runner. Kept off every kubectl exec
# argv (apiserver audit-log leak): the runner pod consumes it via pod-spec env
# valueFrom this Secret (see kcp-runner.yaml), and the rendered batch manifests
# carry it as in-pod files — never as an exec argument.
kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic tbm-rest-credentials \
  --from-literal=username="${DEST_SASL_USER}" \
  --from-literal=password="${DEST_SASL_PASSWORD}" \
  --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f - >/dev/null

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
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/kafka-rest-class.yaml"

# --- Cluster link mirroring 001..MIRRORED_COUNT, offset sync DISABLED ---
# Success batches require consumer.offset.sync.enable OFF; the offset-sync halt
# toggles it on for itself and restores it (self-contained, no run-order coupling).
echo "Creating cluster link ${CLUSTER_LINK_NAME} mirroring ${MIRRORED_COUNT} topics..."
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
    consumer.offset.sync.enable: "false"
EOF
} > "${LINK_CR}"
kubectl --context "${PROFILE}" apply -f "${LINK_CR}"
for _ in $(seq 1 60); do
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" get clusterlink "${CLUSTER_LINK_NAME}" \
    -o jsonpath='{.status.state}' 2>/dev/null | grep -q CREATED && break
  sleep 5
done

# --- Discover cluster IDs ---
DEST_CLUSTER_ID="$(kubectl --context "${PROFILE}" -n "${NAMESPACE}" get kafka destination-kafka -o jsonpath='{.status.clusterID}' 2>/dev/null || echo '')"
SOURCE_CLUSTER_ID="$(kubectl --context "${PROFILE}" -n "${NAMESPACE}" get kafka source-kafka -o jsonpath='{.status.clusterID}' 2>/dev/null || echo '')"
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

# Prove the licence took: without an Enterprise licence the config-file watcher
# never starts and every hot reload is silently dropped (from hot-reload/setup.sh).
echo "Checking the gateway accepted the licence..."
if kubectl --context "${PROFILE}" -n "${NAMESPACE}" logs -l "app=${GATEWAY_NAME}" --tail=-1 2>/dev/null \
     | grep -qi "Hot-reload feature is not enabled"; then
  echo "FATAL: gateway reports 'Hot-reload feature is not enabled' — licence not accepted as Enterprise." >&2
  exit 1
fi
echo "  ✓ no trial-mode hot-reload warning in gateway logs"

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

# --- Render batch/halt manifests + copy hermetic fixtures into .rendered/ ---
# Committed under testdata/batches/ as credential-free .tmpl; the throwaway
# destination SASL is injected only here, into .rendered/ (gitignored).
echo "Rendering batch/halt manifests..."
render_batch() {
  local tmpl="$1" out="${RENDERED_DIR}/$(basename "$1" .tmpl)"
  sed -e "s/__NAMESPACE__/${NAMESPACE}/g" \
      -e "s/__GATEWAY_NAME__/${GATEWAY_NAME}/g" \
      -e "s/__ROUTE_NAME__/${ROUTE_NAME}/g" \
      -e "s/__SOURCE_DOMAIN__/${SOURCE_DOMAIN}/g" \
      -e "s/__DEST_DOMAIN__/${DEST_DOMAIN}/g" \
      -e "s|__SOURCE_BOOTSTRAP__|${SOURCE_BOOTSTRAP}|g" \
      -e "s|__DEST_BOOTSTRAP__|${DEST_BOOTSTRAP}|g" \
      -e "s|__REST_ENDPOINT__|${REST_ENDPOINT}|g" \
      -e "s/__DEST_CLUSTER_ID__/${DEST_CLUSTER_ID}/g" \
      -e "s/__SOURCE_CLUSTER_ID__/${SOURCE_CLUSTER_ID}/g" \
      -e "s/__CLUSTER_LINK_NAME__/${CLUSTER_LINK_NAME}/g" \
      -e "s/__DEST_SASL_USER__/${DEST_SASL_USER}/g" \
      -e "s/__DEST_SASL_PASSWORD__/${DEST_SASL_PASSWORD}/g" \
      "${tmpl}" > "${out}"
}
shopt -s nullglob
for tmpl in "${BATCHES_DIR}"/*.yaml.tmpl; do render_batch "${tmpl}"; done
shopt -u nullglob
# Hermetic route-shape probe fixture needs no live creds — copy verbatim.
[ -f "${SCRIPT_DIR}/testdata/gateway-static-probe.yaml" ] && \
  cp "${SCRIPT_DIR}/testdata/gateway-static-probe.yaml" "${RENDERED_DIR}/gateway-static-probe.yaml"

# --- Write .env (no secrets: the SASL password lives only in the k8s Secret) ---
ENV_FILE="${SCRIPT_DIR}/.env"
{
  echo "# Generated by setup.sh - do not edit. Contains no secrets."
  echo "KCP_TBM_KUBECONFIG=${KUBECONFIG_PATH}"
  echo "KCP_TBM_KUBE_CONTEXT=${PROFILE}"
  echo "KCP_TBM_NAMESPACE=${NAMESPACE}"
  echo "KCP_TBM_KCP_POD=kcp-runner"
  echo "KCP_TBM_RENDERED_DIR=${RENDERED_DIR}"
  echo "KCP_TBM_GATEWAY_NAME=${GATEWAY_NAME}"
  echo "KCP_TBM_GATEWAY_REPLICAS=${GATEWAY_REPLICAS}"
  echo "KCP_TBM_ROUTE_NAME=${ROUTE_NAME}"
  echo "KCP_TBM_SOURCE_DOMAIN=${SOURCE_DOMAIN}"
  echo "KCP_TBM_DEST_DOMAIN=${DEST_DOMAIN}"
  echo "KCP_TBM_SOURCE_BOOTSTRAP=${SOURCE_BOOTSTRAP}"
  echo "KCP_TBM_DEST_BOOTSTRAP=${DEST_BOOTSTRAP}"
  echo "KCP_TBM_REST_ENDPOINT=${REST_ENDPOINT}"
  echo "KCP_TBM_DEST_CLUSTER_ID=${DEST_CLUSTER_ID}"
  echo "KCP_TBM_SOURCE_CLUSTER_ID=${SOURCE_CLUSTER_ID}"
  echo "KCP_TBM_CLUSTER_LINK_NAME=${CLUSTER_LINK_NAME}"
  echo "KCP_TBM_TOPIC_PREFIX=${TOPIC_PREFIX}"
  echo "KCP_TBM_SOURCE_TOPIC_COUNT=${SOURCE_TOPIC_COUNT}"
  echo "KCP_TBM_MIRRORED_COUNT=${MIRRORED_COUNT}"
  echo "KCP_TBM_SUCCESS_HI=${SUCCESS_HI}"
  echo "KCP_TBM_RESERVED_TOPIC=${RESERVED_TOPIC}"
  echo "KCP_TBM_ORPHAN_TOPIC=${ORPHAN_TOPIC}"
} > "${ENV_FILE}"

echo ""
echo "=== Setup complete ==="
echo "Environment written to ${ENV_FILE}"
echo "Dest cluster ID: ${DEST_CLUSTER_ID}"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pods
echo ""
echo "Run tests with: make test-migration-tbm-run"
