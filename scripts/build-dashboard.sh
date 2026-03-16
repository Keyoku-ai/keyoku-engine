#!/usr/bin/env bash
# Builds the keyoku-dashboard and embeds it into the keyoku-engine binary.
# Run from the keyoku-engine root directory.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENGINE_DIR="$(dirname "$SCRIPT_DIR")"
DASHBOARD_DIR="${ENGINE_DIR}/../keyoku-dashboard"
EMBED_DIR="${ENGINE_DIR}/cmd/keyoku-server/dashboard"

echo "==> Building keyoku-dashboard..."
cd "$DASHBOARD_DIR"
npm run build

echo "==> Copying dist to keyoku-engine embed directory..."
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -r "$DASHBOARD_DIR/dist/"* "$EMBED_DIR/"

echo "==> Building keyoku-engine with embedded dashboard..."
cd "$ENGINE_DIR"
go build -tags embed_dashboard ./cmd/keyoku-server/

echo "==> Done! Dashboard embedded at /dashboard/"
