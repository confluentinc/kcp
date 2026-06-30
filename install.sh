#!/bin/sh
# install.sh — install the latest stable kcp release.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/confluentinc/kcp/main/install.sh | sh
#
# Environment overrides:
#   KCP_VERSION       Install a specific tag (e.g. v0.8.5) instead of the latest release.
#   KCP_INSTALL_DIR   Install directory (default: /usr/local/bin).
#
# Downloads the platform binary (kcp_<os>_<arch>), verifies it against the
# release kcp_checksums.txt, and installs it onto your PATH. Unix only (macOS/Linux);
# Windows users should download kcp_windows_amd64.exe from the releases page.

set -eu

REPO="confluentinc/kcp"
INSTALL_DIR="${KCP_INSTALL_DIR:-/usr/local/bin}"
BINARY_NAME="kcp"

err() {
  echo "Error: $*" >&2
  exit 1
}

# --- detect downloader -------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  DOWNLOAD="curl -fsSL"
  DOWNLOAD_TO="curl -fsSL -o"
elif command -v wget >/dev/null 2>&1; then
  DOWNLOAD="wget -qO-"
  DOWNLOAD_TO="wget -qO"
else
  err "need curl or wget to download kcp"
fi

# --- detect OS / arch --------------------------------------------------------
os=$(uname -s)
case "$os" in
  Darwin) OS="darwin" ;;
  Linux) OS="linux" ;;
  *) err "unsupported OS: $os (Windows users: download kcp_windows_amd64.exe from the releases page)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) ARCH="amd64" ;;
  arm64 | aarch64) ARCH="arm64" ;;
  *) err "unsupported architecture: $arch" ;;
esac

ASSET="kcp_${OS}_${ARCH}"

# --- resolve version ---------------------------------------------------------
if [ -n "${KCP_VERSION:-}" ]; then
  TAG="$KCP_VERSION"
else
  echo "Resolving latest kcp release..."
  # Use the releases/latest redirect instead of the API to avoid
  # unauthenticated rate limits (60/hr shared per IP).
  LATEST_URL="https://github.com/${REPO}/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    TAG=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$LATEST_URL" | sed 's|.*/tag/||')
  else
    TAG=$(wget --spider -S "$LATEST_URL" 2>&1 | grep -i 'Location:' | tail -1 | sed 's|.*/tag/||' | tr -d '[:space:]')
  fi
  [ -n "$TAG" ] || err "could not determine the latest release tag"
fi

BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
echo "Installing kcp ${TAG} (${OS}/${ARCH})..."

# --- download into a temp dir ------------------------------------------------
TMP=$(mktemp -d 2>/dev/null || mktemp -d -t kcp)
trap 'rm -rf "$TMP"' EXIT INT TERM

$DOWNLOAD_TO "${TMP}/${ASSET}" "${BASE_URL}/${ASSET}" \
  || err "failed to download ${BASE_URL}/${ASSET} (no binary published for ${OS}/${ARCH} in ${TAG}?)"

# --- verify checksum ---------------------------------------------------------
CHECKSUMS="kcp_checksums.txt"
if $DOWNLOAD_TO "${TMP}/${CHECKSUMS}" "${BASE_URL}/${CHECKSUMS}" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    SHA_CMD="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    SHA_CMD="shasum -a 256"
  else
    SHA_CMD=""
  fi

  if [ -n "$SHA_CMD" ]; then
    expected=$(grep " ${ASSET}\$" "${TMP}/${CHECKSUMS}" | awk '{print $1}' | head -n1)
    # A missing entry means the asset naming is wrong, not an old release — fail loudly.
    [ -n "$expected" ] || err "${ASSET} not listed in ${CHECKSUMS}; cannot verify download"
    actual=$($SHA_CMD "${TMP}/${ASSET}" | awk '{print $1}')
    [ "$expected" = "$actual" ] || err "checksum mismatch for ${ASSET} (expected ${expected}, got ${actual})"
    echo "Checksum verified."
  else
    echo "Warning: no sha256 tool found; skipping checksum verification." >&2
  fi
else
  echo "Warning: ${CHECKSUMS} not available for ${TAG}; skipping verification." >&2
fi

chmod +x "${TMP}/${ASSET}"

# --- install -----------------------------------------------------------------
TARGET="${INSTALL_DIR}/${BINARY_NAME}"
# Create the install dir if it doesn't exist (e.g. a custom KCP_INSTALL_DIR like
# ~/.local/bin). Only attempt this without sudo — we never create arbitrary system
# directories on the user's behalf.
if [ ! -d "$INSTALL_DIR" ]; then
  mkdir -p "$INSTALL_DIR" 2>/dev/null || true
fi

if [ -w "$INSTALL_DIR" ]; then
  mv "${TMP}/${ASSET}" "$TARGET"
elif [ -d "$INSTALL_DIR" ] && command -v sudo >/dev/null 2>&1; then
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "${TMP}/${ASSET}" "$TARGET"
else
  err "cannot write to ${INSTALL_DIR}; set KCP_INSTALL_DIR to a writable directory"
fi

echo "kcp installed to ${TARGET}"

# --- verify ------------------------------------------------------------------
if command -v "$BINARY_NAME" >/dev/null 2>&1 && [ "$(command -v "$BINARY_NAME")" = "$TARGET" ]; then
  "$BINARY_NAME" version
else
  echo "Note: ${INSTALL_DIR} is not on your PATH. Run '${TARGET} version' or add it to PATH."
fi
