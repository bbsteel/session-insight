#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
BIN_PATH="${BIN_PATH:-$ROOT_DIR/session-insight}"
PORT="${PORT:-8080}"

usage() {
  cat <<EOF
Usage: $0 <command>

Commands:
  build   只构建（前端 + Go 二进制），不运行
  run     只运行已构建好的二进制，不重新构建
  all     构建 + 运行（默认行为）
EOF
  exit 0
}

do_build() {
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
  echo "==> Build complete: $BIN_PATH"
}

do_run() {
  cd "$ROOT_DIR"

  if [[ ! -x "$BIN_PATH" ]]; then
    echo "ERROR: binary not found at $BIN_PATH, run 'build' first."
    exit 1
  fi

  echo "==> Killing old processes"
  pkill -f "session-insight" 2>/dev/null || true
  pkill -f "$FRONTEND_DIR/node_modules/.bin/vite" 2>/dev/null || true
  pkill -f "session-insight-cdp" 2>/dev/null || true
  pkill -f "session-insight-minimap-cdp" 2>/dev/null || true
  sleep 0.5

  echo "==> Starting SessionInsight"
  echo "    URL: http://127.0.0.1:$PORT/"
  echo "    Binary: $BIN_PATH"
  exec env PORT="$PORT" "$BIN_PATH"
}

CMD="${1:-}"
case "$CMD" in
  build)
    do_build
    ;;
  run)
    do_run
    ;;
  "")
    usage
    ;;
  all)
    do_build
    do_run
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    echo "ERROR: unknown command '$CMD'"
    echo
    usage
    ;;
esac
