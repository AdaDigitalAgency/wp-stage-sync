#!/usr/bin/env bash
set -euo pipefail

REPO="AdaDigitalAgency/wp-stage-sync"
BIN="wp-stage-sync"
INSTALL_DIR="/usr/local/bin"

print_step() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
print_ok()   { printf '\033[1;32m OK\033[0m %s\n' "$1"; }
print_err()  { printf '\033[1;31mERR\033[0m %s\n' "$1" >&2; }

# Detect OS
OS="$(uname -s)"
if [[ "$OS" != "Linux" ]]; then
  print_err "Unsupported OS: $OS. Only Linux is supported."
  exit 1
fi

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *)
    print_err "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# Resolve latest release tag
print_step "Fetching latest release..."
TAG=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

if [[ -z "$TAG" ]]; then
  print_err "Could not determine latest release tag."
  exit 1
fi

URL="https://github.com/${REPO}/releases/download/${TAG}/${BIN}_linux_${ARCH}"
TMP="$(mktemp)"

print_step "Downloading ${BIN} ${TAG} (linux/${ARCH})..."
if ! curl -sSfL "$URL" -o "$TMP"; then
  print_err "Download failed: $URL"
  rm -f "$TMP"
  exit 1
fi

chmod +x "$TMP"

# Install — root: system-wide, non-root with sudo: system-wide, otherwise: user-local
print_step "Installing to ${INSTALL_DIR}/${BIN}..."
if [[ "$EUID" -eq 0 ]] || [[ -w "$INSTALL_DIR" ]]; then
  mv "$TMP" "${INSTALL_DIR}/${BIN}"
elif command -v sudo &>/dev/null && sudo -n true 2>/dev/null; then
  sudo mv "$TMP" "${INSTALL_DIR}/${BIN}"
else
  LOCAL_DIR="$HOME/.local/bin"
  mkdir -p "$LOCAL_DIR"
  mv "$TMP" "${LOCAL_DIR}/${BIN}"
  INSTALL_DIR="$LOCAL_DIR"
  printf '\033[1;33mWRN\033[0m Installed to %s (no root/sudo access).\n' "$LOCAL_DIR"
  if [[ ":$PATH:" != *":$LOCAL_DIR:"* ]]; then
    printf '\033[1;33mWRN\033[0m %s is not in your PATH.\n' "$LOCAL_DIR"
    printf '    Add this to your ~/.bashrc or ~/.profile:\n'
    printf '    export PATH="%s:$PATH"\n' "$LOCAL_DIR"
  fi
fi

print_ok "${BIN} ${TAG} installed successfully."
echo
echo "  Run:  wp-stage-sync        (interactive TUI)"
echo "  Run:  wp-stage-sync -u     (unattended / cron mode)"
echo "  Run:  wp-stage-sync --help"
echo
