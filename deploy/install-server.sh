#!/usr/bin/env bash
# Install socratex server binary + systemd unit on Linux.
# Usage (as root):
#   ./deploy/install-server.sh /path/to/socratex-linux-amd64
set -euo pipefail

BIN_SRC="${1:-}"
if [[ -z "$BIN_SRC" ]]; then
  if [[ -f dist/socratex-linux-amd64 ]]; then
    BIN_SRC=dist/socratex-linux-amd64
  elif [[ -f socratex ]]; then
    BIN_SRC=socratex
  else
    echo "usage: $0 <path-to-socratex-binary>" >&2
    exit 1
  fi
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_DIR=/opt/socratex

echo "==> install to ${INSTALL_DIR}"
mkdir -p "$INSTALL_DIR"
cp -f "$BIN_SRC" "$INSTALL_DIR/socratex"
chmod 755 "$INSTALL_DIR/socratex"

if [[ ! -f "$INSTALL_DIR/config.json" ]]; then
  if [[ -f "$ROOT/config.server.example.json" ]]; then
    cp "$ROOT/config.server.example.json" "$INSTALL_DIR/config.json"
    echo "    wrote default config.json (edit domain/auth before start)"
  fi
fi

cp -f "$ROOT/deploy/socratex.service" /etc/systemd/system/socratex.service
systemctl daemon-reload
systemctl enable socratex.service

echo ""
echo "Installed. Next steps:"
echo "  1. edit  ${INSTALL_DIR}/config.json"
echo "  2. systemctl start socratex"
echo "  3. systemctl status socratex"
echo "  4. journalctl -u socratex -f"
