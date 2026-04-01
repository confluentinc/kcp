#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_DIR="${SCRIPT_DIR}/manifests"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"
PROFILE="kcp-e2e"
NAMESPACE="confluent"
HELM_REPO="https://packages.confluent.io/helm"
CFK_CHART_VERSION="${CFK_CHART_VERSION:-0.1514.19}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-900s}"

# wait_for_pods waits until at least one pod matching the label exists, then
# waits for all matching pods to be Ready, printing status every 15s.
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
      echo "  ✓ Pods with label ${label} are Ready"
      return 0
    fi
    if [ $SECONDS -ge $deadline ]; then
      echo "  ✗ Timed out waiting for pods with label ${label}"
      kubectl --context "${PROFILE}" -n "${NAMESPACE}" describe pod -l "${label}" | tail -30
      return 1
    fi
    echo "  [$(date +%H:%M:%S)] Pods with label ${label}:"
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pod -l "${label}" -o wide --no-headers 2>/dev/null || true
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" get events --field-selector involvedObject.kind=Pod --sort-by='.lastTimestamp' 2>/dev/null | grep -i "${label%%=*}" | tail -3 || true
  done
}

echo "=== KCP E2E Test Setup ==="
echo "Profile: ${PROFILE}"
echo "CFK chart version: ${CFK_CHART_VERSION}"
echo ""

# --- Minikube ---
if minikube status --profile "${PROFILE}" &>/dev/null; then
  echo "Minikube profile '${PROFILE}' already exists, reusing..."
else
  echo "Starting Minikube..."
  minikube start \
    --profile "${PROFILE}" \
    --driver=docker \
    --cpus=4 \
    --memory=8192 \
    --disk-size=20g \
    --kubernetes-version=v1.30.0
fi

# Point kubectl at the minikube cluster
eval "$(minikube docker-env --profile "${PROFILE}" 2>/dev/null || true)"

# Authenticate with Docker Hub to avoid rate limiting (credentials provided by CI)
if [ -n "${DOCKERHUB_USER:-}" ] && [ -n "${DOCKERHUB_APIKEY:-}" ]; then
  echo "Logging in to Docker Hub..."
  docker login --username "$DOCKERHUB_USER" --password "$DOCKERHUB_APIKEY"
fi

KUBECONFIG_PATH="${HOME}/.kube/config"

echo "Minikube is running."

# --- Namespace ---
echo "Creating namespace..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/namespace.yaml"

# --- CFK Operator ---
echo "Installing CFK operator..."
helm repo add confluentinc "${HELM_REPO}" --force-update
helm repo update
if helm status confluent-operator --namespace "${NAMESPACE}" --kube-context "${PROFILE}" &>/dev/null; then
  echo "CFK operator already installed, skipping..."
else
  HELM_DEBUG=""
  if [ -n "${CI:-}" ]; then
    HELM_DEBUG="--debug"
  fi
  helm install confluent-operator confluentinc/confluent-for-kubernetes \
    --namespace "${NAMESPACE}" \
    --kube-context "${PROFILE}" \
    --version "${CFK_CHART_VERSION}" \
    --set namespaced=false \
    --wait \
    --timeout "${WAIT_TIMEOUT}" \
    ${HELM_DEBUG}
fi

echo "Waiting for CFK operator to be ready..."
wait_for_pods "app=confluent-operator"

# --- Credentials ---
echo "Creating credentials..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/source-credentials.yaml"
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/destination-credentials.yaml"

# --- CA Certificate for TLS (needed before destination Kafka) ---
if ! kubectl --context "${PROFILE}" -n "${NAMESPACE}" get secret ca-pair-sslcerts &>/dev/null; then
  echo "Generating CA keypair for auto-generated TLS certs..."
  openssl req -x509 -newkey rsa:2048 -keyout /tmp/ca-key.pem -out /tmp/ca-cert.pem -days 365 -nodes -subj "/CN=KCPTestCA" 2>/dev/null
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret tls ca-pair-sslcerts \
    --cert=/tmp/ca-cert.pem --key=/tmp/ca-key.pem
  rm -f /tmp/ca-key.pem /tmp/ca-cert.pem
else
  echo "CA keypair secret already exists, skipping..."
fi

# --- KRaft Controllers (parallel) ---
echo "Deploying KRaft controllers..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/source-kraftcontroller.yaml"
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/destination-kraftcontroller.yaml"
echo "Waiting for KRaft controllers..."
wait_for_pods "app=source-kraftcontroller" &
pid1=$!
wait_for_pods "app=destination-kraftcontroller" &
pid2=$!
wait $pid1 $pid2 || { echo "FATAL: KRaft controllers failed to start, aborting setup"; exit 1; }

# --- Kafka Brokers (parallel) ---
echo "Deploying Kafka brokers..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/source-kafka.yaml"
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/destination-kafka.yaml"
echo "Waiting for Kafka brokers..."
wait_for_pods "app=source-kafka" &
pid1=$!
wait_for_pods "app=destination-kafka" &
pid2=$!
wait $pid1 $pid2 || { echo "FATAL: Kafka brokers failed to start, aborting setup"; exit 1; }

# --- Gateway ---
echo "Deploying Gateway..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/gateway-initial.yaml"
echo "Waiting for Gateway..."
wait_for_pods "app=migration-gateway"

# --- Test Topic ---
echo "Creating test topic on source..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/test-topic.yaml"

# Wait for topic to be created
echo "Waiting for topic to be ready..."
for i in $(seq 1 60); do
  if kubectl --context "${PROFILE}" -n "${NAMESPACE}" get kafkatopic e2e-test-topic -o jsonpath='{.status.state}' 2>/dev/null | grep -q "CREATED"; then
    echo "Topic is ready."
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "FATAL: Topic did not reach CREATED state within timeout"
    exit 1
  fi
  sleep 5
done

# --- Kafka REST Class ---
echo "Creating KafkaRestClass for destination..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/kafka-rest-class.yaml"

# --- Cluster Link ---
echo "Creating cluster link..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/cluster-link.yaml"

# Wait for mirror topic to appear
echo "Waiting for cluster link to sync..."
for i in $(seq 1 60); do
  if kubectl --context "${PROFILE}" -n "${NAMESPACE}" get clusterlink e2e-link -o jsonpath='{.status.state}' 2>/dev/null | grep -q "CREATED"; then
    echo "Cluster link is ready."
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "FATAL: Cluster link did not reach CREATED state within timeout"
    exit 1
  fi
  sleep 5
done

# --- Get connection details ---
DEST_CLUSTER_ID=$(kubectl --context "${PROFILE}" -n "${NAMESPACE}" get kafka destination-kafka -o jsonpath='{.status.clusterID}' 2>/dev/null || echo "")
if [ -z "${DEST_CLUSTER_ID}" ]; then
  echo "WARNING: Could not get destination cluster ID from CR status. Test may need to discover it."
fi

# --- Build kcp for Linux and deploy runner pod ---
# The kcp binary runs inside the cluster so it can reach services by K8s DNS
# and TLS SANs match without port-forwarding or /etc/hosts workarounds.
echo "Building kcp binary for Linux..."
CGO_ENABLED=0 GOOS=linux go build -ldflags "-X github.com/confluentinc/kcp/internal/build_info.Version=0.0.0-localdev -X github.com/confluentinc/kcp/internal/build_info.Commit=unknown -X github.com/confluentinc/kcp/internal/build_info.Date=unknown" -o "${SCRIPT_DIR}/.kcp-linux" "${REPO_ROOT}"

echo "Deploying kcp runner pod..."
kubectl --context "${PROFILE}" apply -f "${MANIFESTS_DIR}/kcp-runner.yaml"
wait_for_pods "app=kcp-runner"

KCP_POD="kcp-runner"

echo "Copying kcp binary and fixtures into runner pod..."
kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${SCRIPT_DIR}/.kcp-linux" "${KCP_POD}:/workspace/kcp"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${KCP_POD}" -- chmod +x /workspace/kcp
kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${MANIFESTS_DIR}/gateway-fenced.yaml" "${KCP_POD}:/workspace/gateway-fenced.yaml"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${MANIFESTS_DIR}/gateway-switchover.yaml" "${KCP_POD}:/workspace/gateway-switchover.yaml"

# Generate kubeconfig from service account token (kcp requires --kube-path)
echo "Generating in-cluster kubeconfig..."
kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${KCP_POD}" -- sh -c '
  TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
  cat > /workspace/kubeconfig <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    server: https://kubernetes.default.svc
  name: local
contexts:
- context:
    cluster: local
    namespace: confluent
    user: kcp
  name: local
current-context: local
users:
- name: kcp
  user:
    token: '"'"'${TOKEN}'"'"'
EOF'

rm -f "${SCRIPT_DIR}/.kcp-linux"

# --- Write .env file ---
ENV_FILE="${SCRIPT_DIR}/.env"
cat > "${ENV_FILE}" <<EOF
# Generated by setup.sh - do not edit
KCP_E2E_KUBECONFIG=${KUBECONFIG_PATH}
KCP_E2E_KUBE_CONTEXT=${PROFILE}
KCP_E2E_NAMESPACE=${NAMESPACE}
KCP_E2E_SOURCE_BOOTSTRAP=source-kafka.confluent.svc.cluster.local:9071
KCP_E2E_DEST_BOOTSTRAP=destination-kafka.confluent.svc.cluster.local:9071
KCP_E2E_REST_PROXY_ENDPOINT=http://destination-kafka.confluent.svc.cluster.local:8090
KCP_E2E_DEST_CLUSTER_ID=${DEST_CLUSTER_ID}
KCP_E2E_CLUSTER_LINK_NAME=e2e-link
KCP_E2E_CLUSTER_API_KEY=testuser
KCP_E2E_CLUSTER_API_SECRET=testpassword
KCP_E2E_GATEWAY_NAME=migration-gateway
KCP_E2E_KCP_POD=${KCP_POD}
KCP_E2E_KUBE_PATH=/workspace/kubeconfig
KCP_E2E_FENCED_CR=/workspace/gateway-fenced.yaml
KCP_E2E_SWITCHOVER_CR=/workspace/gateway-switchover.yaml
EOF

echo ""
echo "=== Setup Complete ==="
echo "Environment written to: ${ENV_FILE}"
echo ""
echo "Dest Cluster ID: ${DEST_CLUSTER_ID}"
echo "kcp runner pod:  ${KCP_POD}"
echo ""
echo "Run tests with: make ci-e2e-tests"
