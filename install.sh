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
# release checksums.txt, and installs it onto your PATH. Unix only (macOS/Linux);
# Windows users should download kcp_windows_amd64.exe from the releases page.

set -eu

REPO="confluentinc/kcp"
INSTALL_DIR="${KCP_INSTALL_DIR:-/usr/local/bin}"
BINARY_NAME="kcp"

err() {
  echo "Error: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
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
  TAG=$($DOWNLOAD "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name":' \
    | sed -E 's/.*"([^"]+)".*/\1/')
  [ -n "$TAG" ] || err "could not determine the latest release tag from the GitHub API"
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
    if [ -n "$expected" ]; then
      actual=$($SHA_CMD "${TMP}/${ASSET}" | awk '{print $1}')
      [ "$expected" = "$actual" ] || err "checksum mismatch for ${ASSET} (expected ${expected}, got ${actual})"
      echo "Checksum verified."
    else
      echo "Warning: ${ASSET} not listed in ${CHECKSUMS}; skipping verification." >&2
    fi
  else
    echo "Warning: no sha256 tool found; skipping checksum verification." >&2
  fi
else
  echo "Warning: ${CHECKSUMS} not available for ${TAG}; skipping verification." >&2
fi

chmod +x "${TMP}/${ASSET}"

# --- install -----------------------------------------------------------------
TARGET="${INSTALL_DIR}/${BINARY_NAME}"
if [ -w "$INSTALL_DIR" ] 2>/dev/null; then
  mv "${TMP}/${ASSET}" "$TARGET"
elif command -v sudo >/dev/null 2>&1; then
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "${TMP}/${ASSET}" "$TARGET"
else
  err "cannot write to ${INSTALL_DIR} and sudo is unavailable; set KCP_INSTALL_DIR to a writable directory"
fi

echo "kcp installed to ${TARGET}"

# --- verify ------------------------------------------------------------------
if command -v "$BINARY_NAME" >/dev/null 2>&1 && [ "$(command -v "$BINARY_NAME")" = "$TARGET" ]; then
  "$BINARY_NAME" version
else
  echo "Note: ${INSTALL_DIR} is not on your PATH. Run '${TARGET} version' or add it to PATH."
fi
