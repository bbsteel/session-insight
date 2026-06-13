#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
BIN_PATH="${BIN_PATH:-$ROOT_DIR/session-insight}"
PORT="${PORT:-8080}"

cd "$ROOT_DIR"

echo "==> Building frontend"
if [[ ! -d "$FRONTEND_DIR/node_modules" ]]; then
  echo "==> Installing frontend dependencies"
  (cd "$FRONTEND_DIR" && npm ci)
fi
(cd "$FRONTEND_DIR" && npm run build)

echo "==> Building Go binary"
export GOCACHE="${GOCACHE:-/tmp/session-insight-go-build}"
go build -o "$BIN_PATH" .

echo "==> Starting SessionInsight"
echo "    URL: http://127.0.0.1:$PORT/"
echo "    Binary: $BIN_PATH"
exec env PORT="$PORT" "$BIN_PATH"
