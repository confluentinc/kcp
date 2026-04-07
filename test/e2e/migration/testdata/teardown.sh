#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="kcp-e2e"

echo "=== KCP E2E Test Teardown ==="

if minikube status --profile "${PROFILE}" &>/dev/null; then
  echo "Deleting Minikube profile '${PROFILE}'..."
  minikube delete --profile "${PROFILE}"
  echo "Minikube cluster deleted."
else
  echo "Minikube profile '${PROFILE}' does not exist, nothing to delete."
fi

# Clean up generated files
rm -f "${SCRIPT_DIR}/.env"
rm -rf "${SCRIPT_DIR}/.certs"

echo "=== Teardown Complete ==="
