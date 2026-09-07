#!/usr/bin/env bash
# Deletes the hot-reload E2E cluster and the rendered manifests.
#
# Deliberately deletes the whole minikube profile rather than just the Confluent
# resources: the suite's premise is a gateway with nothing left over from a
# previous run, and a half-cleaned cluster is how a stale CRD or a stale licence
# secret silently changes what the next run tests.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="${PROFILE:-kcp-e2e-hotreload}"

echo "Tearing down hot-reload E2E profile '${PROFILE}'..."
minikube delete --profile "${PROFILE}" || true

rm -rf "${SCRIPT_DIR}/.rendered" "${SCRIPT_DIR}/.env"
echo "Done."
