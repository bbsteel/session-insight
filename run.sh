#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
BIN_PATH="${BIN_PATH:-$ROOT_DIR/session-insight}"

# A linked worktree is an isolated development instance. Keep its mutable
# application state inside the worktree. The first run picks a free OS-assigned
# loopback port and persists it to .runtime/session-insight.port so subsequent
# restarts in the same worktree reuse it — the port stays stable within a
# worktree across restarts, while different worktrees remain isolated.
# The primary checkout retains the historical 8080 + ~/.session-insight setup.
if [[ -f "$ROOT_DIR/.git" ]]; then
  RUNTIME_DIR="$ROOT_DIR/.runtime"
  mkdir -p "$RUNTIME_DIR"
  PORT_FILE="$RUNTIME_DIR/session-insight.port"
  # Reuse a previously-persisted port for this worktree; fall back to $PORT or
  # 0 (OS-assigned) on the first run. A non-empty PORT env var always wins.
  if [[ -z "${PORT:-}" && -f "$PORT_FILE" ]]; then
    SAVED_PORT="$(cat "$PORT_FILE" 2>/dev/null || true)"
    if [[ "$SAVED_PORT" =~ ^[0-9]+$ && "$SAVED_PORT" -gt 0 ]]; then
      PORT="$SAVED_PORT"
    fi
  fi
  PORT="${PORT:-0}"
  SI_DATA_DIR="${SI_DATA_DIR:-$RUNTIME_DIR/session-insight}"
else
  RUNTIME_DIR="$ROOT_DIR"
  PORT_FILE="$RUNTIME_DIR/session-insight.port"
  PORT="${PORT:-8080}"
  SI_DATA_DIR="${SI_DATA_DIR:-}"
fi

PID_FILE="$RUNTIME_DIR/session-insight.pid"
LOG_FILE="$RUNTIME_DIR/session-insight.log"
URL_FILE="$RUNTIME_DIR/session-insight.url"

pid_is_owned() {
  local pid="$1"
  if ! kill -0 "$pid" 2>/dev/null; then
    return 1
  fi

  if [[ -r "/proc/$pid/exe" ]]; then
    local exe
    exe=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
    [[ "$exe" == "$BIN_PATH" || "$exe" == "$BIN_PATH (deleted)" ]]
    return
  fi

  # Portable fallback for platforms without procfs. The binary is launched by
  # absolute path, so the first command token still identifies this worktree.
  local command
  command=$(ps -p "$pid" -o command= 2>/dev/null || true)
  [[ "$command" == "$BIN_PATH" || "$command" == "$BIN_PATH "* ]]
}

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

Linked worktrees automatically use an OS-assigned random loopback port on the
first run and reuse the same port on subsequent restarts (persisted to
.runtime/session-insight.port), with an isolated database under .runtime/. The
primary checkout continues to use port 8080 and ~/.session-insight. PORT and
SI_DATA_DIR may be set to override these defaults.
EOF
  exit 0
}

do_build() {
  cd "$ROOT_DIR"

  # Go toolchain 未在系统 PATH 时，自动查找 module cache 中的版本
  if ! command -v go &>/dev/null; then
    local go_toolchain go_path
    go_path="${GOPATH:-$HOME/go}"
    go_toolchain=$(find "$go_path/pkg/mod/golang.org" -maxdepth 3 -name "go" -path "*/bin/go" 2>/dev/null | sort -V | tail -1)
    if [[ -n "$go_toolchain" ]]; then
      export PATH="$(dirname "$go_toolchain"):$PATH"
    else
      echo "ERROR: go not found in PATH or ~/go/pkg/mod/golang.org toolchain cache"
      exit 1
    fi
  fi

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

  mkdir -p "$RUNTIME_DIR"

  if [[ ! -x "$BIN_PATH" ]]; then
    echo "ERROR: binary not found at $BIN_PATH, run 'build' first."
    exit 1
  fi

  do_stop

  rm -f "$URL_FILE"

  echo "==> Starting SessionInsight (background)"
  if [[ "$PORT" == "0" ]]; then
    echo "    URL: assigning a random loopback port"
  else
    echo "    URL: http://127.0.0.1:$PORT/"
  fi
  echo "    Binary: $BIN_PATH"
  if [[ -n "$SI_DATA_DIR" ]]; then
    echo "    Data directory: $SI_DATA_DIR"
  else
    echo "    Data directory: ~/.session-insight"
  fi
  echo "    PID file: $PID_FILE"
  echo "    Log file: $LOG_FILE"
  nohup setsid env PORT="$PORT" SI_DATA_DIR="$SI_DATA_DIR" "$BIN_PATH" >"$LOG_FILE" 2>&1 < /dev/null &
  echo $! >"$PID_FILE"
  local pid
  pid=$(cat "$PID_FILE")
  echo "    PID: $pid"

  # Listening starts after the bounded initial index pass. Wait for the
  # post-bind log line so a printed URL always belongs to this exact process.
  local url attempt
  for ((attempt = 0; attempt < 300; attempt++)); do
    url=$(sed -n 's/.*SessionInsight listening on \(http[^ ]*\).*/\1/p' "$LOG_FILE" 2>/dev/null | tail -1 || true)
    if [[ -n "$url" ]]; then
      printf '%s\n' "$url" >"$URL_FILE"
      # Persist the actual bound port so the next restart in this worktree
      # reuses it (PORT=0 → OS-assigned → extract from the ready URL).
      local bound_port
      bound_port=$(sed -n 's|.*://127\.0\.0\.1:\([0-9]\+\)/.*|\1|p' <<<"$url" | tail -1 || true)
      if [[ "$bound_port" =~ ^[0-9]+$ && "$bound_port" -gt 0 ]]; then
        printf '%s\n' "$bound_port" >"$PORT_FILE"
      fi
      echo "    Ready: $url"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "ERROR: SessionInsight exited before becoming ready"
      tail -20 "$LOG_FILE" 2>/dev/null || true
      rm -f "$PID_FILE"
      return 1
    fi
    sleep 0.1
  done

  echo "ERROR: SessionInsight did not become ready within 30 seconds"
  tail -20 "$LOG_FILE" 2>/dev/null || true
  do_stop
  return 1
}

# 只按 pid 文件精确 kill，禁止 pkill 模糊匹配（会误杀命令行含关键字的无关进程）
do_stop() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid=$(cat "$PID_FILE")
    if pid_is_owned "$pid"; then
      echo "==> Stopping SessionInsight (PID: $pid)"
      kill "$pid"
      sleep 0.5
      kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
    elif kill -0 "$pid" 2>/dev/null; then
      echo "WARNING: refusing to stop PID $pid because it is not owned by this worktree"
    fi
    rm -f "$PID_FILE"
  fi
  rm -f "$URL_FILE"
}

do_status() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid=$(cat "$PID_FILE")
    if pid_is_owned "$pid"; then
      echo "SessionInsight is running (PID: $pid)"
      if [[ -s "$URL_FILE" ]]; then
        echo "URL: $(cat "$URL_FILE")"
      elif [[ "$PORT" != "0" ]]; then
        echo "URL: http://127.0.0.1:$PORT/"
      else
        echo "URL: pending; inspect $LOG_FILE"
      fi
      if [[ -n "$SI_DATA_DIR" ]]; then
        echo "Data directory: $SI_DATA_DIR"
      else
        echo "Data directory: ~/.session-insight"
      fi
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
