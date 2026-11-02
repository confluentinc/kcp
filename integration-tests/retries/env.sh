# Environment steps for the retries suite: the version pins, minikube, images, the CFK
# operator, the CP credentials and CA, the KRaft controllers and Kafka brokers, the
# gateway licence and client-tls keystore, the destination REST credential secret and
# the KafkaRestClass. Sourced by setup.sh, never run on its own.
#
# This is a self-contained copy: it was derived from the shared environment script the
# idempotent-fsm and route-conversion suites use (integration-tests/lib/cp-e2e-env.sh,
# repo commit recorded in README.md), minus the static-route swap-auth step this suite
# does not need. It shares no file with another suite, so a change to their environment
# never changes this one; compare the pins against theirs when bumping.
#
# The caller sets PROFILE, NAMESPACE and RENDERED_DIR before calling any step, and
# GATEWAY_NAME before e2e_create_client_tls.

E2E_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_MANIFESTS_DIR="${E2E_LIB_DIR}/testdata/manifests"

# --- Version pins (spine from integration-tests/migration) ---
# The licence secret lives in us-east-1. The ambient AWS_REGION is deliberately ignored:
# `assume` exports the SSO session's own region (us-west-2), which would look the secret
# up in the wrong region. Override with KCP_E2E_AWS_REGION only.
AWS_REGION="${KCP_E2E_AWS_REGION:-us-east-1}"
export AWS_REGION
# CFK 3.4.0 is public: the chart comes from Confluent's Helm repo and its default
# operator image (confluentinc/confluent-operator:0.2038.1) is multi-arch.
CFK_CHART_REPO="${CFK_CHART_REPO:-https://packages.confluent.io/helm}"
CFK_CHART_VERSION="${CFK_CHART_VERSION:-0.2038.1}"
# The public gateway and init-container images are multi-arch, so the pods run
# natively on both a local (arm64) and a CI (amd64) node.
GATEWAY_TAG="${GATEWAY_TAG:-1.4.0}"
GATEWAY_IMAGE="${GATEWAY_IMAGE:-confluentinc/confluent-gateway-for-cloud:${GATEWAY_TAG}}"
INIT_IMAGE="${INIT_IMAGE:-confluentinc/confluent-init-container:3.4.0}"
CP_SERVER_TAG="${CP_SERVER_TAG:-8.1.2}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-900s}"

# Well-known throwaway test credential for the destination SASL/REST leg. Matches
# destination-credentials.yaml (testuser/testpassword). Delivered to a runner pod
# via the retries-rest-credentials Secret (pod-spec env), never on a kubectl exec argv.
DEST_SASL_USER="${DEST_SASL_USER:-testuser}"
DEST_SASL_PASSWORD="${DEST_SASL_PASSWORD:-testpassword}"

# --- Licence knobs (from integration-tests/migration-hot-reload) ---
LICENSE_SECRET_ID="${LICENSE_SECRET_ID:-kcp/e2e/gateway-license}"
GATEWAY_LICENSE_GCP_SECRET="${GATEWAY_LICENSE_GCP_SECRET:-}"

# Two CP clusters + gateway JVMs + operator need more headroom than migration's
# single-purpose profiles.
MINIKUBE_CPUS="${MINIKUBE_CPUS:-4}"
MINIKUBE_MEMORY="${MINIKUBE_MEMORY:-12288}"

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

# apply_secret — create-or-update a generic secret in NAMESPACE from kubectl
# create secret generic arguments.
apply_secret() {
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic "$@" \
    --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f - >/dev/null
}

# e2e_preflight — the host tools every setup needs, and a licence source.
e2e_preflight() {
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
}

# e2e_start_minikube — start (or reuse) the PROFILE minikube cluster and point
# docker at its daemon. Sets KUBECONFIG_PATH.
e2e_start_minikube() {
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
}

# e2e_pull_images — pre-pull the gateway and CP images into minikube's daemon.
e2e_pull_images() {
  echo "Pulling gateway image..."
  docker pull "${GATEWAY_IMAGE}" >/dev/null
  echo "Pre-pulling public images in background..."
  docker pull "docker.io/confluentinc/cp-server:${CP_SERVER_TAG}" &>/dev/null &
  docker pull "docker.io/${INIT_IMAGE}" &>/dev/null &
  docker pull "docker.io/alpine:3.20" &>/dev/null &
}

# e2e_install_operator — namespace, the CFK operator (chart from Confluent's
# public Helm repo), and the guard that the installed Gateway CRD can express
# hot reload.
e2e_install_operator() {
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/namespace.yaml"

  echo "Installing CFK operator (chart ${CFK_CHART_VERSION})..."
  if helm status confluent-operator --namespace "${NAMESPACE}" --kube-context "${PROFILE}" &>/dev/null; then
    echo "CFK operator already installed, skipping..."
  else
    helm install confluent-operator confluent-for-kubernetes \
      --repo "${CFK_CHART_REPO}" --version "${CFK_CHART_VERSION}" \
      --namespace "${NAMESPACE}" --kube-context "${PROFILE}" \
      --set namespaced=false \
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
}

# e2e_create_credentials — the CP credential secrets and the CA keypair the
# destination's auto-generated certs are signed with (before destination Kafka).
e2e_create_credentials() {
  echo "Creating credentials..."
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/source-credentials.yaml"
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/destination-credentials.yaml"
  if ! kubectl --context "${PROFILE}" -n "${NAMESPACE}" get secret ca-pair-sslcerts &>/dev/null; then
    echo "Generating CA keypair for auto-generated TLS certs..."
    CA_DIR="$(mktemp -d)"
    openssl req -x509 -newkey rsa:2048 -keyout "${CA_DIR}/ca-key.pem" -out "${CA_DIR}/ca-cert.pem" \
      -days 365 -nodes -subj "/CN=KCPRetriesTestCA" 2>/dev/null
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret tls ca-pair-sslcerts \
      --cert="${CA_DIR}/ca-cert.pem" --key="${CA_DIR}/ca-key.pem"
    rm -rf "${CA_DIR}"
  fi
}

# e2e_deploy_kafka — KRaft controllers, then the source (PLAINTEXT) and
# destination (SASL_SSL) brokers, each pair in parallel.
e2e_deploy_kafka() {
  local p1 p2
  echo "Deploying KRaft controllers..."
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/source-kraftcontroller.yaml"
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/destination-kraftcontroller.yaml"
  wait_for_pods "app=source-kraftcontroller" & p1=$!
  wait_for_pods "app=destination-kraftcontroller" & p2=$!
  { wait "$p1" && wait "$p2"; } || { echo "FATAL: KRaft controllers failed to start"; exit 1; }

  echo "Deploying Kafka brokers..."
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/source-kafka.yaml"
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/destination-kafka.yaml"
  wait_for_pods "app=source-kafka" & p1=$!
  wait_for_pods "app=destination-kafka" & p2=$!
  { wait "$p1" && wait "$p2"; } || { echo "FATAL: Kafka brokers failed to start"; exit 1; }
}

# create_license_secret — the licence JWT reaches the cluster through a pipe —
# never argv, temp file, or echo (from hot-reload/setup.sh).
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

# e2e_install_licence — the gateway-license secret, contents never logged.
e2e_install_licence() {
  echo "Installing gateway licence..."
  create_license_secret
  echo "  ✓ secret/gateway-license created (contents not logged)"
}

# e2e_create_client_tls — self-signed server keystore for the gateway's
# client->gateway TLS. A dynamic route mandates host/SNI, which mandates client
# TLS, so the secret must exist and hold a valid keystore or the gateway
# crashloops. Written under RENDERED_DIR (gitignored), never committed. Needs
# GATEWAY_NAME.
e2e_create_client_tls() {
  mkdir -p "${RENDERED_DIR}"
  if ! kubectl --context "${PROFILE}" -n "${NAMESPACE}" get secret client-tls &>/dev/null; then
    echo "Generating gateway client-tls keystore..."
    KS="${RENDERED_DIR}/gateway-keystore.jks"
    rm -f "${KS}"
    keytool -genkeypair -alias gateway -keyalg RSA -keysize 2048 -validity 365 \
      -dname "CN=retries-gateway" \
      -ext "SAN=dns:bootstrap.gw.local,dns:*.gw.local,dns:${GATEWAY_NAME}.${NAMESPACE}.svc.cluster.local" \
      -keystore "${KS}" -storepass changeit -keypass changeit -storetype JKS >/dev/null 2>&1
    # CFK requires the key=value form, not a bare password.
    printf 'jksPassword=changeit' > "${RENDERED_DIR}/jksPassword.txt"
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic client-tls \
      --from-file=keystore.jks="${KS}" \
      --from-file=jksPassword.txt="${RENDERED_DIR}/jksPassword.txt"
  fi
}

# e2e_create_rest_credentials_secret — the destination REST/SASL credential for
# a runner pod, as the retries-rest-credentials Secret. Kept off every kubectl exec
# argv (apiserver audit-log leak): a runner consumes it via pod-spec env
# valueFrom this Secret, and rendered manifests carry it as in-pod files.
e2e_create_rest_credentials_secret() {
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic retries-rest-credentials \
    --from-literal=username="${DEST_SASL_USER}" \
    --from-literal=password="${DEST_SASL_PASSWORD}" \
    --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f - >/dev/null
}

# e2e_apply_rest_class — the KafkaRestClass pair the ClusterLink CR references.
e2e_apply_rest_class() {
  kubectl --context "${PROFILE}" apply -f "${E2E_MANIFESTS_DIR}/kafka-rest-class.yaml"
}
