#!/bin/bash
# Start a pinned AWS Emulator in Docker for the MSK discovery suite. Its HOME is a
# directory inside this suite, so the generated credentials, config and CA never
# touch the developer's ~/.aws.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINER=kcp-test-msk-emulator
EMU_HOME="$SCRIPT_DIR/.emulator-home"
EMU="$EMU_HOME/.aws/emulator/9297"

# Upgrade deliberately: published versions are listed in
# https://downloads.aws.com/aws-emulator/packages/aws-emulator/LTS/versions.json and
# per-service fidelity scores ship as scores.json.gz on the matching aws/aws-emulator
# GitHub release.
AWS_EMULATOR_VERSION=1.0.322

case "$(uname -m)" in
  x86_64) ARCH=x86_64 ;;
  arm64|aarch64) ARCH=aarch64 ;;
  *) echo "unsupported host architecture: $(uname -m)" >&2; exit 1 ;;
esac

docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
rm -rf "$EMU_HOME"
mkdir -p "$EMU_HOME/.aws/emulator"

echo "Building AWS Emulator $AWS_EMULATOR_VERSION ($ARCH) image..."
docker build \
  --build-arg "AWS_EMULATOR_VERSION=$AWS_EMULATOR_VERSION" \
  --build-arg "ARCH=$ARCH" \
  -t "kcp-test-aws-emulator:$AWS_EMULATOR_VERSION" "$SCRIPT_DIR"

user_args=()
if [[ "$(uname -s)" == "Linux" ]]; then
  user_args=(--user "$(id -u):$(id -g)")
fi

echo "Starting AWS Emulator..."
docker run -d --name "$CONTAINER" \
  -p 127.0.0.1:9297:9297 \
  -e EMULATOR_TELEMETRY=off \
  -e HOME="$EMU_HOME" \
  -v "$EMU_HOME:$EMU_HOME" \
  --memory=2g --memory-swap=2g \
  ${user_args[@]+"${user_args[@]}"} \
  "kcp-test-aws-emulator:$AWS_EMULATOR_VERSION" -p 9297 >/dev/null

for _ in $(seq 1 60); do
  if [[ -f "$EMU/ca.pem" ]] &&
     [[ "$(curl -sf --cacert "$EMU/ca.pem" https://127.0.0.1:9297/__admin/health 2>/dev/null)" == "ok" ]]; then
    echo "AWS Emulator is healthy."
    exit 0
  fi
  sleep 1
done

echo "AWS Emulator did not become healthy within 60s" >&2
docker logs "$CONTAINER" >&2 || true
exit 1
