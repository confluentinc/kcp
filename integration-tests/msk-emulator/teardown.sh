#!/bin/bash
# Stop the AWS Emulator and remove its generated credentials and state.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Tearing down AWS Emulator..."
docker rm -f kcp-test-msk-emulator >/dev/null 2>&1 || true
rm -rf "$SCRIPT_DIR/.emulator-home" || true

echo "Teardown complete."
