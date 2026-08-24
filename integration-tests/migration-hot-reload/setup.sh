#!/usr/bin/env bash
# Stands up a throwaway cluster carrying a LICENSED, hot-reload-capable Confluent
# Gateway, for the suite that proves kcp verifies a gateway transition by config
# revision rather than by pod turnover.
#
# Separate from integration-tests/migration on purpose. That suite pins CFK
# 0.1514.19, whose CRD has neither spec.configId nor spec.hotReload, and it
# annotates its resources with platform.confluent.io/block-reconcile to stop the
# operator re-asserting declared config over REST-set values. Both are
# disqualifying here: without configId in the CRD kcp downgrades to rollout
# verification and the hot-reload path is never taken, and block-reconcile would
# stop CFK processing a configId bump at all — the suite would pass by never
# testing anything. Nothing here is annotated, and the cluster is its own profile
# so no resource from the other suite can be left lying around.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_DIR="${SCRIPT_DIR}/manifests"
TEMPLATES_DIR="${MANIFESTS_DIR}/templates"
RENDERED_DIR="${SCRIPT_DIR}/.rendered"

PROFILE="${PROFILE:-kcp-e2e-hotreload}"
NAMESPACE="confluent"
GATEWAY_NAME="${GATEWAY_NAME:-hotreload-gateway}"
GATEWAY_REPLICAS="${GATEWAY_REPLICAS:-2}"

# --- Version pins. Each is load-bearing; see the table in the README. ---
# The public Helm repo tops out at 0.1718.10, whose CRD predates spec.configId,
# so the chart has to come from a local checkout.
CFK_CHART_PATH="${CFK_CHART_PATH:-${HOME}/cfk-setup/gateway-hotreload/cfk-chart-v3.3.x}"
# Docker Hub's operator also stops at 0.1718.10, so the image comes from the
# internal ECR mirror. It is published amd64-only.
OPERATOR_IMAGE="${OPERATOR_IMAGE:-635910096382.dkr.ecr.us-east-1.amazonaws.com/kcp/confluent-operator:v0.1718.34-amd64}"
OPERATOR_REGISTRY="${OPERATOR_IMAGE%%/*}"
OPERATOR_REPO_TAG="${OPERATOR_IMAGE#*/}"
OPERATOR_REPO="${OPERATOR_REPO_TAG%:*}"
OPERATOR_TAG="${OPERATOR_REPO_TAG##*:}"
GATEWAY_TAG="${GATEWAY_TAG:-1.3.0}"
INIT_TAG="${INIT_TAG:-3.3.0}"
# The broker behind the gateway's SOURCE domain. Not a version pin in the same
# sense as the others — nothing here depends on this being 7.6.0, it just matches
# what the other integration suites already use, so it is usually cached.
KAFKA_IMAGE="${KAFKA_IMAGE:-confluentinc/cp-kafka:7.6.0}"
SOURCE_BOOTSTRAP="kafka-source.${NAMESPACE}.svc.cluster.local:9092"
# CFK publishes the route endpoint on a Service it creates for the gateway.
GATEWAY_BOOTSTRAP="${GATEWAY_NAME}.${NAMESPACE}.svc.cluster.local:9595"
SOURCE_TOPIC="${SOURCE_TOPIC:-kcp-hr-fence-probe}"

LICENSE_SECRET_ID="${LICENSE_SECRET_ID:-kcp/e2e/gateway-license}"
# CI reads the licence from GCP Secret Manager instead of AWS, over Semaphore's OIDC
# workload-identity federation (see .semaphore and migration-tooling/.infra). When set,
# this secret name takes precedence over the AWS path — but not over an explicit
# GATEWAY_LICENSE_KEY.
GATEWAY_LICENSE_GCP_SECRET="${GATEWAY_LICENSE_GCP_SECRET:-}"
# CI likewise has no ambient AWS credentials for the ECR operator-image pull. When
# these are set (the Semaphore path), the narrowly-scoped ECR access key is read
# from GCP Secret Manager — over the same OIDC federation as the licence — and
# exported for the ECR login below. Both must be set together; leave them unset
# locally, where an ambient AWS profile is used instead.
ECR_AWS_KEY_GCP_SECRET="${ECR_AWS_KEY_GCP_SECRET:-}"
ECR_AWS_SECRET_GCP_SECRET="${ECR_AWS_SECRET_GCP_SECRET:-}"
AWS_REGION="${AWS_REGION:-us-east-1}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-900s}"

# The CFK canary is a full extra gateway JVM, so the node has to fit
# replicas + 1. Sizing this by replicas rather than a constant is what stops the
# rig's failure mode recurring: two m5.large nodes could not fit 3 JVMs plus a
# canary and the kubelet was OOM-killed, which looks exactly like a hot-reload
# that never propagated.
#
# The source broker is one more JVM again (capped at 512m heap / 1400Mi), hence
# the headroom over the 8192 this ran on before it had a data plane.
MINIKUBE_CPUS="${MINIKUBE_CPUS:-4}"
MINIKUBE_MEMORY="${MINIKUBE_MEMORY:-10240}"

echo "=== kcp hot-reload E2E setup ==="
echo "Profile:        ${PROFILE}"
echo "Gateway:        ${GATEWAY_NAME} (${GATEWAY_REPLICAS} replicas, +1 canary during a hot reload)"
echo "Gateway image:  confluentinc/confluent-gateway-for-cloud:${GATEWAY_TAG}"
echo "Source broker:  ${KAFKA_IMAGE} -> ${SOURCE_BOOTSTRAP} (topic ${SOURCE_TOPIC})"
echo "CFK chart:      ${CFK_CHART_PATH}"
echo ""

# --- Preflight -------------------------------------------------------------
for bin in minikube kubectl helm docker; do
  command -v "$bin" >/dev/null 2>&1 || { echo "FATAL: ${bin} is required but not on PATH"; exit 1; }
done

if [ ! -d "${CFK_CHART_PATH}" ]; then
  cat >&2 <<EOF
FATAL: CFK chart not found at ${CFK_CHART_PATH}

  This suite needs chart 0.1718.34 (appVersion 3.3.0), the first version whose
  Gateway CRD declares spec.configId. The public Helm repo only serves up to
  0.1718.10, so the chart must come from a local checkout of confluent-operator
  at origin/v3.3.x.

  Point CFK_CHART_PATH at that chart directory and re-run.
EOF
  exit 1
fi

chart_version="$(awk '/^version:/ {print $2; exit}' "${CFK_CHART_PATH}/Chart.yaml" 2>/dev/null || true)"
if ! grep -q 'configId' "${CFK_CHART_PATH}/crds/platform.confluent.io_gateways.yaml" 2>/dev/null; then
  echo "FATAL: the chart at ${CFK_CHART_PATH} (version ${chart_version:-unknown}) has a Gateway CRD without spec.configId." >&2
  echo "       kcp would detect this cluster as pre-hot-reload and verify by pod rollout, so the suite would test nothing." >&2
  exit 1
fi
echo "  ✓ chart ${chart_version} declares spec.configId"

# The licence is the one input with no in-cluster substitute. Take it from the
# environment when set, else from GCP Secret Manager (the CI path), else from AWS
# Secrets Manager. It is never written to disk and never passed as an argument —
# see create_license_secret.
if [ -n "${GATEWAY_LICENSE_KEY:-}" ]; then
  echo "  ✓ using licence from GATEWAY_LICENSE_KEY"
elif [ -n "${GATEWAY_LICENSE_GCP_SECRET:-}" ]; then
  command -v gcloud >/dev/null 2>&1 || {
    echo "FATAL: GATEWAY_LICENSE_GCP_SECRET is set but the gcloud CLI is not on PATH to read it" >&2
    exit 1
  }
  # Mirrors the AWS branch: prove there is an active credential, not that it can read this
  # specific secret — the read itself happens in create_license_secret, straight into a pipe.
  if ! gcloud auth print-access-token >/dev/null 2>&1; then
    echo "FATAL: GATEWAY_LICENSE_GCP_SECRET is set but there is no active gcloud credential to read ${GATEWAY_LICENSE_GCP_SECRET}." >&2
    echo "       Authenticate to GCP or export GATEWAY_LICENSE_KEY with a CP Enterprise licence." >&2
    exit 1
  fi
  echo "  ✓ gcloud credential present; licence will be read from GCP secret ${GATEWAY_LICENSE_GCP_SECRET}"
else
  command -v aws >/dev/null 2>&1 || {
    echo "FATAL: GATEWAY_LICENSE_KEY is unset and the aws CLI is not on PATH to fetch ${LICENSE_SECRET_ID}" >&2
    exit 1
  }
  if ! aws sts get-caller-identity >/dev/null 2>&1; then
    echo "FATAL: GATEWAY_LICENSE_KEY is unset and there are no working AWS credentials to read ${LICENSE_SECRET_ID}." >&2
    echo "       Authenticate (e.g. 'sso') or export GATEWAY_LICENSE_KEY with a CP Enterprise licence." >&2
    exit 1
  fi
  echo "  ✓ AWS credentials present; licence will be read from ${LICENSE_SECRET_ID}"
fi

# --- Cluster ---------------------------------------------------------------
if minikube status --profile "${PROFILE}" &>/dev/null; then
  echo "Reusing existing minikube profile '${PROFILE}'..."
else
  echo "Starting minikube..."
  minikube start \
    --profile "${PROFILE}" \
    --driver=docker \
    --cpus="${MINIKUBE_CPUS}" \
    --memory="${MINIKUBE_MEMORY}" \
    --disk-size=20g \
    --kubernetes-version=v1.30.0
fi

kubectl --context "${PROFILE}" create namespace "${NAMESPACE}" --dry-run=client -o yaml \
  | kubectl --context "${PROFILE}" apply -f -

# --- Images ----------------------------------------------------------------
# Pulled on the host and side-loaded rather than pulled by the kubelet. That
# keeps the ECR credential on the host instead of installing it into the cluster
# as an imagePullSecret, and it is what makes the amd64-only operator image work
# on an arm64 host: the host resolves the platform explicitly and the node's
# runtime executes it under emulation.
echo "Preparing images..."
if ! docker image inspect "${OPERATOR_IMAGE}" >/dev/null 2>&1; then
  echo "  Authenticating to ECR..."
  # CI has no ambient AWS credentials. When the ECR pull secrets are configured
  # (Semaphore), read the narrowly-scoped access key from GCP Secret Manager and
  # export it here — straight into the env var, never echoed, never on disk, never
  # passed as an argument. Locally these knobs are unset and the ambient AWS
  # profile is used unchanged.
  if [ -n "${ECR_AWS_KEY_GCP_SECRET:-}" ] && [ -n "${ECR_AWS_SECRET_GCP_SECRET:-}" ]; then
    command -v gcloud >/dev/null 2>&1 || {
      echo "FATAL: ECR_AWS_*_GCP_SECRET is set but the gcloud CLI is not on PATH to read the ECR credentials" >&2
      exit 1
    }
    AWS_ACCESS_KEY_ID="$(gcloud secrets versions access latest --secret="${ECR_AWS_KEY_GCP_SECRET}")"
    AWS_SECRET_ACCESS_KEY="$(gcloud secrets versions access latest --secret="${ECR_AWS_SECRET_GCP_SECRET}")"
    export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
  fi
  aws ecr get-login-password --region "${AWS_REGION}" \
    | docker login --username AWS --password-stdin "${OPERATOR_REGISTRY}" >/dev/null
  echo "  Pulling operator image (amd64)..."
  docker pull --platform linux/amd64 "${OPERATOR_IMAGE}" >/dev/null
fi
for img in "confluentinc/confluent-gateway-for-cloud:${GATEWAY_TAG}" \
           "confluentinc/confluent-init-container:${INIT_TAG}" \
           "${KAFKA_IMAGE}"; do
  docker image inspect "$img" >/dev/null 2>&1 || { echo "  Pulling ${img}..."; docker pull "$img" >/dev/null; }
done

echo "  Loading images into ${PROFILE} (slow on first run)..."
for img in "${OPERATOR_IMAGE}" \
           "confluentinc/confluent-gateway-for-cloud:${GATEWAY_TAG}" \
           "confluentinc/confluent-init-container:${INIT_TAG}" \
           "${KAFKA_IMAGE}"; do
  minikube image load "$img" --profile "${PROFILE}"
done

# --- Licence ---------------------------------------------------------------
# The JWT reaches the cluster through a pipe: never an argv entry (visible in ps),
# never a temp file, never echoed. The rendered manifests below reference the
# Secret by name, so no rendered file contains it either.
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
    manifest="$(aws secretsmanager get-secret-value \
        --secret-id "${LICENSE_SECRET_ID}" --region "${AWS_REGION}" \
        --query SecretString --output text \
      | kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic gateway-license \
          --from-file=license.txt=/dev/stdin --dry-run=client -o yaml)"
  fi
  printf '%s' "${manifest}" | kubectl --context "${PROFILE}" apply -f - >/dev/null
}

echo "Installing gateway licence..."
create_license_secret
echo "  ✓ secret/gateway-license created (contents not logged)"

# --- CFK operator ----------------------------------------------------------
if helm status confluent-operator --namespace "${NAMESPACE}" --kube-context "${PROFILE}" &>/dev/null; then
  echo "CFK operator already installed, skipping..."
else
  echo "Installing CFK operator ${chart_version}..."
  helm install confluent-operator "${CFK_CHART_PATH}" \
    --namespace "${NAMESPACE}" \
    --kube-context "${PROFILE}" \
    --set namespaced=false \
    --set "image.registry=${OPERATOR_REGISTRY}" \
    --set "image.repository=${OPERATOR_REPO}" \
    --set "image.tag=${OPERATOR_TAG}" \
    --set image.pullPolicy=Never \
    --wait --timeout "${WAIT_TIMEOUT}"
fi

echo "Waiting for the operator to be Ready..."
kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=Ready pod \
  -l app=confluent-operator --timeout="${WAIT_TIMEOUT}"

# Fail loudly if the installed CRD cannot express what the suite tests, rather
# than letting kcp quietly pick rollout verification.
if [ -z "$(kubectl --context "${PROFILE}" get crd gateways.platform.confluent.io \
      -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.configId}' 2>/dev/null)" ]; then
  echo "FATAL: the installed Gateway CRD does not declare spec.configId" >&2
  exit 1
fi
echo "  ✓ installed CRD declares spec.configId"

# --- Source Kafka ----------------------------------------------------------
# Applied before the gateway so the upstream resolves the first time the gateway
# builds its virtual cluster, rather than after a failed resolution.
echo "Deploying the source Kafka broker..."
sed -e "s|__KAFKA_IMAGE__|${KAFKA_IMAGE}|g" "${MANIFESTS_DIR}/kafka-source.yaml" \
  | kubectl --context "${PROFILE}" apply -f -

kubectl --context "${PROFILE}" -n "${NAMESPACE}" rollout status deploy/kafka-source \
  --timeout="${WAIT_TIMEOUT}"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=Ready pod \
  -l app=kafka-source --timeout="${WAIT_TIMEOUT}"

# Created here rather than by auto-creation so a topic that never appears fails
# setup loudly instead of surfacing as an unexplained produce error mid-suite.
# The heap override matters: KAFKA_HEAP_OPTS in the pod env would otherwise give
# this short-lived CLI JVM the broker's own 512m inside a 1400Mi container.
echo "Creating topic ${SOURCE_TOPIC}..."
kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec deploy/kafka-source -- \
  env KAFKA_HEAP_OPTS="-Xmx192m" kafka-topics \
    --bootstrap-server localhost:9092 \
    --create --if-not-exists \
    --topic "${SOURCE_TOPIC}" \
    --partitions 1 --replication-factor 1 >/dev/null
echo "  ✓ topic ${SOURCE_TOPIC} present"

# --- Render the three transition CRs ---------------------------------------
# fence  = the same route plus a fence block   -> in-place route edit
# switch = the same route on the other domain  -> in-place route edit
# Neither adds or removes a route, a streaming domain or a secret, so CFK's rules
# make both pure hot reloads.
mkdir -p "${RENDERED_DIR}"

# The fence block goes via a file rather than an awk -v: awk cannot take a
# multi-line variable value, and silently dropping the block would render a
# "fenced" CR identical to the initial one — a suite that passes without fencing.
FENCE_BLOCK_FILE="${RENDERED_DIR}/route-fence-block.yaml"
cat > "${FENCE_BLOCK_FILE}" <<'FENCEBLOCK'
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
FENCEBLOCK

render() {
  local name="$1" extra_file="$2" domain="$3" bootstrap="$4"
  local out="${RENDERED_DIR}/gateway-${name}.yaml"
  awk -v ef="${extra_file}" '
    $0 == "__ROUTE_EXTRA__" {
      if (ef != "") { while ((getline line < ef) > 0) print line }
      next
    }
    { print }
  ' "${TEMPLATES_DIR}/gateway-base.yaml" \
    | sed -e "s/__GATEWAY_NAME__/${GATEWAY_NAME}/g" \
          -e "s/__REPLICAS__/${GATEWAY_REPLICAS}/g" \
          -e "s/__GATEWAY_TAG__/${GATEWAY_TAG}/g" \
          -e "s/__INIT_TAG__/${INIT_TAG}/g" \
          -e "s/__ROUTE_DOMAIN__/${domain}/g" \
          -e "s/__ROUTE_BOOTSTRAP__/${bootstrap}/g" \
          -e "s|__SOURCE_BOOTSTRAP__|${SOURCE_BOOTSTRAP}|g" \
      > "${out}"
  printf '%s\n' "${out}"
}

INITIAL_CR="$(render initial "" source-domain SOURCE)"
FENCED_CR="$(render fenced "${FENCE_BLOCK_FILE}" source-domain SOURCE)"
SWITCHOVER_CR="$(render switchover "" destination-domain DESTINATION)"

# A fenced CR that lost its fence block would make the whole suite vacuous.
grep -q 'errorCode: BROKER_NOT_AVAILABLE' "${FENCED_CR}" \
  || { echo "FATAL: rendered fenced CR has no fence block" >&2; exit 1; }
grep -q 'name: destination-domain' "${SWITCHOVER_CR}" \
  || { echo "FATAL: rendered switchover CR does not target the destination domain" >&2; exit 1; }
# An unsubstituted placeholder would leave the source domain unroutable, and the
# data-plane suite would then read a broken upstream as a working fence.
for cr in "${INITIAL_CR}" "${FENCED_CR}" "${SWITCHOVER_CR}"; do
  grep -q "${SOURCE_BOOTSTRAP}" "${cr}" \
    || { echo "FATAL: ${cr} does not point the source domain at ${SOURCE_BOOTSTRAP}" >&2; exit 1; }
done
echo "Rendered transition CRs into ${RENDERED_DIR}"

# --- Gateway ---------------------------------------------------------------
echo "Applying the initial gateway CR..."
kubectl --context "${PROFILE}" apply -f "${INITIAL_CR}"

echo "Waiting for ${GATEWAY_REPLICAS} gateway pod(s) to be Ready..."
until kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pod -l "app=${GATEWAY_NAME}" --no-headers 2>/dev/null | grep -q .; do
  sleep 5
done
kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=Ready pod \
  -l "app=${GATEWAY_NAME}" --timeout="${WAIT_TIMEOUT}"

# --- Prove the licence actually took ---------------------------------------
# The gateway gates its config-file watcher on an Enterprise licence and CFK
# reports success either way, so this is checked here rather than discovered as a
# confusing test failure later.
echo "Checking the gateway accepted the licence..."
if kubectl --context "${PROFILE}" -n "${NAMESPACE}" logs -l "app=${GATEWAY_NAME}" --tail=-1 2>/dev/null \
     | grep -qi "Hot-reload feature is not enabled"; then
  echo "FATAL: the gateway reports 'Hot-reload feature is not enabled'." >&2
  echo "       The licence in ${LICENSE_SECRET_ID} is not being accepted as Enterprise, so the config-file" >&2
  echo "       watcher never starts and every hot reload would be silently dropped." >&2
  exit 1
fi
echo "  ✓ no trial-mode hot-reload warning in the gateway logs"

# --- Write the env file ----------------------------------------------------
ENV_FILE="${SCRIPT_DIR}/.env"
{
  echo "# Generated by setup.sh - do not edit. Contains no secrets."
  echo "KCP_HR_KUBECONFIG=${HOME}/.kube/config"
  echo "KCP_HR_KUBE_CONTEXT=${PROFILE}"
  echo "KCP_HR_NAMESPACE=${NAMESPACE}"
  echo "KCP_HR_GATEWAY_NAME=${GATEWAY_NAME}"
  echo "KCP_HR_GATEWAY_REPLICAS=${GATEWAY_REPLICAS}"
  echo "KCP_HR_INITIAL_CR=${INITIAL_CR}"
  echo "KCP_HR_FENCED_CR=${FENCED_CR}"
  echo "KCP_HR_SWITCHOVER_CR=${SWITCHOVER_CR}"
  echo "KCP_HR_SOURCE_BOOTSTRAP=${SOURCE_BOOTSTRAP}"
  echo "KCP_HR_GATEWAY_BOOTSTRAP=${GATEWAY_BOOTSTRAP}"
  echo "KCP_HR_TOPIC=${SOURCE_TOPIC}"
} > "${ENV_FILE}"

echo ""
echo "=== Setup complete ==="
echo "Environment written to ${ENV_FILE}"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pods
