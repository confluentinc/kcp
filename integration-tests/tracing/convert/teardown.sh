#!/usr/bin/env bash
# Deletes the tracing suite's dynamic-to-static cluster (Minikube profile
# kcp-e2e-tracing-convert, see state.sh) and its generated files. Every other
# environment is left alone.
#
# Deletes the whole minikube profile rather than just the Confluent resources:
# promotion is irreversible, so a reused cluster would start from whatever the
# last run left behind.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"

echo "Tearing down tracing dynamic-to-static E2E profile '${PROFILE}'..."
minikube delete --profile "${PROFILE}" || true

rm -rf "${STATE_DIR}"
echo "Done."
