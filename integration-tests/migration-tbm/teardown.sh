#!/usr/bin/env bash
# Deletes the TBM E2E cluster and the per-run artefacts.
#
# Deletes the whole minikube profile rather than just the Confluent resources: the
# suite's premise is a clean dynamic gateway, and a half-cleaned cluster is how a
# stale CRD or a stale licence secret silently changes what the next run tests.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="${PROFILE:-kcp-e2e-tbm}"

echo "Tearing down TBM E2E profile '${PROFILE}'..."
minikube delete --profile "${PROFILE}" || true

rm -rf "${SCRIPT_DIR}/.rendered" "${SCRIPT_DIR}/.env" \
       "${SCRIPT_DIR}/.tbm-e2e.test" "${SCRIPT_DIR}/.kcp-linux"
echo "Done."
