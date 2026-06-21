#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
BIN_PATH="${BIN_PATH:-$ROOT_DIR/session-insight}"
PORT="${PORT:-8080}"

PID_FILE="$ROOT_DIR/session-insight.pid"
LOG_FILE="$ROOT_DIR/session-insight.log"

usage() {
  cat <<EOF
Usage: $0 <command>

Commands:
  build    只构建（前端 + Go 二进制），不运行
  start    后台启动已构建好的二进制，不重新构建
  stop     停止后台运行的进程
  restart  重启（stop + start）
  status   查看运行状态
  all      构建 + 运行
  log      查看后台日志
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
  go build -tags sqlite_fts5 -o "$BIN_PATH" .
  echo "==> Build complete: $BIN_PATH"
}

do_start() {
  cd "$ROOT_DIR"

  if [[ ! -x "$BIN_PATH" ]]; then
    echo "ERROR: binary not found at $BIN_PATH, run 'build' first."
    exit 1
  fi

  do_stop

  echo "==> Starting SessionInsight (background)"
  echo "    URL: http://127.0.0.1:$PORT/"
  echo "    Binary: $BIN_PATH"
  echo "    PID file: $PID_FILE"
  echo "    Log file: $LOG_FILE"
  nohup env PORT="$PORT" "$BIN_PATH" >"$LOG_FILE" 2>&1 &
  echo $! >"$PID_FILE"
  echo "    PID: $(cat "$PID_FILE")"
}

do_stop() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid=$(cat "$PID_FILE")
    if kill -0 "$pid" 2>/dev/null; then
      echo "==> Stopping SessionInsight (PID: $pid)"
      kill "$pid"
      sleep 0.5
      kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$PID_FILE"
  fi
  pkill -f "session-insight" 2>/dev/null || true
  pkill -f "$FRONTEND_DIR/node_modules/.bin/vite" 2>/dev/null || true
  pkill -f "session-insight-cdp" 2>/dev/null || true
  pkill -f "session-insight-minimap-cdp" 2>/dev/null || true
}

do_status() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid=$(cat "$PID_FILE")
    if kill -0 "$pid" 2>/dev/null; then
      echo "SessionInsight is running (PID: $pid)"
      echo "URL: http://127.0.0.1:$PORT/"
    else
      echo "SessionInsight is NOT running (stale PID file: $pid)"
    fi
  else
    echo "SessionInsight is NOT running"
  fi
}

do_log() {
  if [[ -f "$LOG_FILE" ]]; then
    tail -f "$LOG_FILE"
  else
    echo "No log file found at $LOG_FILE"
  fi
}

CMD="${1:-}"
case "$CMD" in
  build)
    do_build
    ;;
  start)
    do_start
    ;;
  stop)
    do_stop
    ;;
  restart)
    do_stop
    do_start
    ;;
  status)
    do_status
    ;;
  log)
    do_log
    ;;
  "")
    usage
    ;;
  all)
    do_build
    do_start
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
