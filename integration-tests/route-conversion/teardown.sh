#!/usr/bin/env bash
# Deletes the route-conversion E2E cluster (Minikube profile
# kcp-e2e-route-conversion, see state.sh) and its generated files. The
# idempotent-fsm environments are left alone.
#
# Deletes the whole minikube profile rather than just the Confluent resources:
# promotion is irreversible, so a reused cluster would start from whatever the
# last run left behind.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"

echo "Tearing down route-conversion E2E profile '${PROFILE}'..."
minikube delete --profile "${PROFILE}" || true

rm -rf "${STATE_DIR}"
echo "Done."
