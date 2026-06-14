#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
BIN_PATH="${BIN_PATH:-$ROOT_DIR/session-insight}"
PORT="${PORT:-8080}"

cd "$ROOT_DIR"

# ---- 清理旧进程 ----
echo "==> Killing old processes"

# 杀旧 Go 二进制
pkill -f "session-insight" 2>/dev/null || true

# 杀旧 vite dev server (本项目)
pkill -f "$FRONTEND_DIR/node_modules/.bin/vite" 2>/dev/null || true

# 杀关联的 headless 浏览器 (本项目专用 CDP)
pkill -f "session-insight-cdp" 2>/dev/null || true
pkill -f "session-insight-minimap-cdp" 2>/dev/null || true

# 等端口释放
sleep 0.5

# ---- 构建 ----
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
