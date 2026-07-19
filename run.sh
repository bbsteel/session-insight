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
  # Worktree-only: primary never sets or writes PORT_FILE (avoids a root-level
  # untracked session-insight.port that is not gitignored).
  PORT_FILE="$RUNTIME_DIR/session-insight.port"
  # Reuse a previously-persisted port for this worktree; fall back to $PORT or
  # 0 (OS-assigned) on the first run. A non-empty PORT env var always wins.
  if [[ -z "${PORT:-}" && -f "$PORT_FILE" ]]; then
    SAVED_PORT="$(cat "$PORT_FILE" 2>/dev/null || true)"
    if [[ "$SAVED_PORT" =~ ^[0-9]+$ && "$SAVED_PORT" -ge 1 && "$SAVED_PORT" -le 65535 ]]; then
      PORT="$SAVED_PORT"
    fi
  fi
  PORT="${PORT:-0}"
  SI_DATA_DIR="${SI_DATA_DIR:-$RUNTIME_DIR/session-insight}"
else
  RUNTIME_DIR="$ROOT_DIR"
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
  build       Build only (frontend + Go binary), do not run
  start       Start the built binary in the background, do not rebuild
  stop        Stop the background process for this worktree
  restart     Restart this worktree (stop + start)
  status      Show this worktree status and list all instances (# / PID / port / start time)
  kill <n…>   Stop instances by their status list numbers (numbers are recalculated each run; run status before kill; e.g. kill 1 3)
  all         Build + run
  log         View background log

Linked worktrees automatically use an OS-assigned random loopback port on the
first run and reuse the same port on subsequent restarts (persisted to
.runtime/session-insight.port), with an isolated database under .runtime/. The
primary checkout continues to use port 8080 and ~/.session-insight. PORT and
SI_DATA_DIR may be set to override these defaults.
Instance numbers in status/kill are rebuilt each run and may change; always run
status immediately before kill. Only related checkouts (this repo's worktrees)
are killable; same-named binaries elsewhere are listed as non-killable.
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
  # 开发构建注入 describe 版本与 commit（dirty 时标记），关于页据此展示开发信息；
  # release 构建由 .github/workflows/release.yml 只注入 tag 版本号。
  local version commit dirty=""
  version=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
  git diff --quiet 2>/dev/null && git diff --cached --quiet 2>/dev/null || dirty="-dirty"
  commit="$(git rev-parse --short HEAD 2>/dev/null || echo "")${dirty}"
  go build -tags sqlite_fts5 -ldflags "-X main.version=${version} -X main.commit=${commit}" -o "$BIN_PATH" .
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
      # Persist the bound port for linked worktrees only (PORT_FILE unset on primary).
      if [[ -n "${PORT_FILE:-}" ]]; then
        local bound_port
        bound_port=$(port_from_url "$url")
        if [[ "$bound_port" =~ ^[0-9]+$ && "$bound_port" -ge 1 && "$bound_port" -le 65535 ]]; then
          printf '%s\n' "$bound_port" >"$PORT_FILE"
        fi
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

# Resolve pid/url/log paths for a checkout (primary vs linked worktree).
runtime_files_for_checkout() {
  local checkout="$1"
  if [[ -f "$checkout/.git" ]]; then
    printf '%s\n' \
      "$checkout/.runtime/session-insight.pid" \
      "$checkout/.runtime/session-insight.url" \
      "$checkout/.runtime/session-insight.log"
  else
    printf '%s\n' \
      "$checkout/session-insight.pid" \
      "$checkout/session-insight.url" \
      "$checkout/session-insight.log"
  fi
}

pid_matches_bin() {
  local pid="$1"
  local bin="$2"
  if ! kill -0 "$pid" 2>/dev/null; then
    return 1
  fi
  if [[ -r "/proc/$pid/exe" ]]; then
    local exe
    exe=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
    [[ "$exe" == "$bin" || "$exe" == "$bin (deleted)" ]]
    return
  fi
  local command
  command=$(ps -p "$pid" -o command= 2>/dev/null || true)
  [[ "$command" == "$bin" || "$command" == "$bin "* ]]
}

process_start_time() {
  local pid="$1"
  LC_ALL=C ps -p "$pid" -o lstart= 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true
}

process_port_from_ss() {
  local pid="$1"
  # ss local address is host:port (IPv4) or [host]:port (IPv6).
  ss -ltnp 2>/dev/null | awk -v needle="pid=$pid," '
    index($0, needle) {
      addr = $4
      sub(/.*:/, "", addr)
      print addr
      exit
    }
  ' || true
}

# True when we can observe this process's environment (not just that it is alive).
process_env_readable() {
  local pid="$1"
  if [[ -r "/proc/$pid/environ" ]]; then
    return 0
  fi
  # ps "e" may embed env after argv (Linux/BSD variants; not always available).
  local line
  line=$(ps eww -w -w -p "$pid" -o command= 2>/dev/null || true)
  if [[ -z "$line" ]]; then
    line=$(ps -E -w -w -p "$pid" -o command= 2>/dev/null || true)
  fi
  [[ -n "$line" && "$line" == *"="* ]]
}

# Print NAME=value lines from a process environment, if readable.
process_env_lines() {
  local pid="$1"
  if [[ -r "/proc/$pid/environ" ]]; then
    tr '\0' '\n' <"/proc/$pid/environ" 2>/dev/null || true
    return 0
  fi
  local line
  line=$(ps eww -w -w -p "$pid" -o command= 2>/dev/null || true)
  if [[ -z "$line" ]]; then
    line=$(ps -E -w -w -p "$pid" -o command= 2>/dev/null || true)
  fi
  if [[ -z "$line" ]]; then
    return 1
  fi
  # Heuristic extract of KEY=value tokens (enough for PORT / SI_DATA_DIR).
  printf '%s\n' "$line" | tr ' ' '\n' | grep -E '^[A-Za-z_][A-Za-z0-9_]*=' || true
  return 0
}

process_env_var() {
  local pid="$1"
  local name="$2"
  process_env_lines "$pid" 2>/dev/null | sed -n "s/^${name}=//p" | head -1 || true
}

process_port_from_environ() {
  process_env_var "$1" "PORT"
}

process_data_dir() {
  # Report the *running process* data dir only. Never invent from this shell's
  # SI_DATA_DIR when the process environment cannot be read.
  local pid="$1"
  local data_dir

  if ! process_env_readable "$pid"; then
    printf '%s\n' "-"
    return
  fi

  data_dir=$(process_env_var "$pid" "SI_DATA_DIR")
  if [[ -n "$data_dir" ]]; then
    printf '%s\n' "$data_dir"
  else
    # Process env is readable and SI_DATA_DIR is empty → app default.
    printf '%s\n' "~/.session-insight"
  fi
}

port_from_url() {
  local url="$1"
  if [[ "$url" =~ :([0-9]+)/?$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
  fi
}

resolve_instance_url_port() {
  # Sets globals _url and _port for the caller.
  local pid="$1"
  local url_file="$2"
  local log_file="${3:-}"
  _url=""
  _port=""

  if [[ -n "$url_file" && -s "$url_file" ]]; then
    _url=$(tr -d '\r\n' <"$url_file")
    _port=$(port_from_url "$_url")
  fi

  if [[ -z "$_port" || "$_port" == "0" ]]; then
    local env_port
    env_port=$(process_port_from_environ "$pid")
    if [[ -n "$env_port" && "$env_port" != "0" ]]; then
      _port="$env_port"
    fi
  fi

  if [[ -z "$_port" || "$_port" == "0" ]]; then
    local ss_port
    ss_port=$(process_port_from_ss "$pid")
    if [[ -n "$ss_port" ]]; then
      _port="$ss_port"
    fi
  fi

  if [[ -z "$_url" && -n "$log_file" && -f "$log_file" ]]; then
    _url=$(sed -n 's/.*SessionInsight listening on \(http[^ ]*\).*/\1/p' "$log_file" 2>/dev/null | tail -1 || true)
    if [[ -z "$_port" || "$_port" == "0" ]]; then
      _port=$(port_from_url "$_url")
    fi
  fi

  if [[ -z "$_url" && -n "$_port" && "$_port" != "0" ]]; then
    _url="http://127.0.0.1:$_port/"
  fi

  if [[ -z "$_port" ]]; then
    _port="-"
  fi
  if [[ -z "$_url" ]]; then
    _url="-"
  fi
}

list_related_checkouts() {
  # Emit absolute checkout paths (primary + linked worktrees), unique, current first.
  local -A seen=()
  local path

  printf '%s\n' "$ROOT_DIR"
  seen["$ROOT_DIR"]=1

  if command -v git &>/dev/null; then
    while IFS= read -r path; do
      [[ -z "$path" ]] && continue
      path=$(cd "$path" 2>/dev/null && pwd || true)
      [[ -z "$path" || -n "${seen[$path]+x}" ]] && continue
      seen["$path"]=1
      printf '%s\n' "$path"
    done < <(git -C "$ROOT_DIR" worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p' || true)
  fi
}

# Populated by collect_instances (parallel arrays, 0-based).
INSTANCE_PIDS=()
INSTANCE_BINS=()
INSTANCE_CHECKOUTS=()
INSTANCE_PID_FILES=()
INSTANCE_URL_FILES=()
INSTANCE_PORTS=()
INSTANCE_URLS=()
INSTANCE_STARTEDS=()
INSTANCE_NOTES=()
INSTANCE_KILLABLE=() # "1" if ./run.sh kill may stop it; "0" for external same-named binaries
INSTANCE_STALE_LINES=()

push_instance() {
  local pid="$1"
  local bin="$2"
  local checkout="$3"
  local pid_file="$4"
  local url_file="$5"
  local port="$6"
  local url="$7"
  local started="$8"
  local note="$9"
  local killable="${10:-1}"
  INSTANCE_PIDS+=("$pid")
  INSTANCE_BINS+=("$bin")
  INSTANCE_CHECKOUTS+=("$checkout")
  INSTANCE_PID_FILES+=("$pid_file")
  INSTANCE_URL_FILES+=("$url_file")
  INSTANCE_PORTS+=("$port")
  INSTANCE_URLS+=("$url")
  INSTANCE_STARTEDS+=("$started")
  INSTANCE_NOTES+=("$note")
  INSTANCE_KILLABLE+=("$killable")
}

checkout_note() {
  local checkout="$1"
  local note=""
  if [[ "$checkout" == "$ROOT_DIR" ]]; then
    note="current"
  fi
  if [[ -f "$checkout/.git" ]]; then
    if [[ -n "$note" ]]; then
      note="$note, worktree"
    else
      note="worktree"
    fi
  else
    if [[ -n "$note" ]]; then
      note="$note, primary"
    else
      note="primary"
    fi
  fi
  printf '%s\n' "$note"
}

# Fill INSTANCE_* arrays with live instances (stable order for status / kill).
collect_instances() {
  INSTANCE_PIDS=()
  INSTANCE_BINS=()
  INSTANCE_CHECKOUTS=()
  INSTANCE_PID_FILES=()
  INSTANCE_URL_FILES=()
  INSTANCE_PORTS=()
  INSTANCE_URLS=()
  INSTANCE_STARTEDS=()
  INSTANCE_NOTES=()
  INSTANCE_KILLABLE=()
  INSTANCE_STALE_LINES=()

  local -A listed_pids=()
  local -A related_checkouts=()
  local checkout pid_file url_file log_file pid bin started note
  local _url _port

  while IFS= read -r checkout; do
    [[ -z "$checkout" || ! -d "$checkout" ]] && continue
    related_checkouts["$checkout"]=1
    {
      read -r pid_file
      read -r url_file
      read -r log_file
    } < <(runtime_files_for_checkout "$checkout")

    bin="$checkout/session-insight"
    note=$(checkout_note "$checkout")

    if [[ ! -f "$pid_file" ]]; then
      continue
    fi

    pid=$(tr -d '[:space:]' <"$pid_file" 2>/dev/null || true)
    if [[ -z "$pid" || ! "$pid" =~ ^[0-9]+$ ]]; then
      INSTANCE_STALE_LINES+=("invalid PID file: $pid_file")
      continue
    fi

    if ! pid_matches_bin "$pid" "$bin"; then
      if kill -0 "$pid" 2>/dev/null; then
        INSTANCE_STALE_LINES+=("stale/unowned PID $pid in $pid_file (process alive but not this checkout binary)")
      else
        INSTANCE_STALE_LINES+=("stale PID file: $pid_file (pid $pid not running)")
      fi
      continue
    fi

    resolve_instance_url_port "$pid" "$url_file" "$log_file"
    started=$(process_start_time "$pid")
    [[ -z "$started" ]] && started="-"
    push_instance "$pid" "$bin" "$checkout" "$pid_file" "$url_file" "$_port" "$_url" "$started" "$note" "1"
    listed_pids["$pid"]=1
  done < <(list_related_checkouts)

  # Surface other live session-insight binaries. Only related-checkout orphans
  # are killable; same-named processes outside this repo's worktrees are listed
  # for visibility but marked non-killable.
  # Linux: /proc exe links. Portable fallback: ps argv (first token = binary path).
  local orphan_pid orphan_exe orphan_checkout orphan_bin killable
  local proc exe cmd first
  if [[ -d /proc ]]; then
    for proc in /proc/[0-9]*; do
      orphan_pid=${proc#/proc/}
      [[ -n "${listed_pids[$orphan_pid]+x}" ]] && continue
      exe=$(readlink "$proc/exe" 2>/dev/null || true)
      case "$exe" in
        */session-insight|*/session-insight\ \(deleted\)) ;;
        *) continue ;;
      esac
      orphan_exe="${exe% (deleted)}"
      register_discovered_binary "$orphan_pid" "$orphan_exe"
      listed_pids["$orphan_pid"]=1
    done
  else
    while read -r orphan_pid cmd; do
      [[ -z "$orphan_pid" || ! "$orphan_pid" =~ ^[0-9]+$ ]] && continue
      [[ -n "${listed_pids[$orphan_pid]+x}" ]] && continue
      first=${cmd%% *}
      case "$first" in
        */session-insight) ;;
        *) continue ;;
      esac
      register_discovered_binary "$orphan_pid" "$first"
      listed_pids["$orphan_pid"]=1
    done < <(ps -eo pid=,args= 2>/dev/null || true)
  fi
}

# Register a live binary discovered outside the pid-file walk.
# Uses related_checkouts associative array from collect_instances.
register_discovered_binary() {
  local orphan_pid="$1"
  local orphan_exe="$2"
  local orphan_checkout orphan_bin killable note
  local pid_file="" url_file="" log_file=""
  local started _url _port

  orphan_checkout=$(dirname "$orphan_exe")
  orphan_bin="$orphan_checkout/session-insight"

  if [[ -n "${related_checkouts[$orphan_checkout]+x}" ]]; then
    killable="1"
    note=$(checkout_note "$orphan_checkout")
    if [[ -n "$note" ]]; then
      note="$note, no pid file"
    else
      note="no pid file"
    fi
    {
      read -r pid_file
      read -r url_file
      read -r log_file
    } < <(runtime_files_for_checkout "$orphan_checkout")
    # Only trust runtime files when they still name this live process.
    local recorded_pid
    recorded_pid=$(tr -d '[:space:]' <"$pid_file" 2>/dev/null || true)
    if [[ "$recorded_pid" != "$orphan_pid" ]]; then
      pid_file=""
      url_file=""
      log_file=""
    fi
  else
    killable="0"
    note="external, non-killable"
  fi

  resolve_instance_url_port "$orphan_pid" "$url_file" "$log_file"
  started=$(process_start_time "$orphan_pid")
  [[ -z "$started" ]] && started="-"
  push_instance "$orphan_pid" "$orphan_bin" "$orphan_checkout" "$pid_file" "$url_file" "$_port" "$_url" "$started" "$note" "$killable"
}

print_instances_table_header() {
  printf '%-4s %-8s %-6s %-28s %-30s %s\n' "#" "PID" "PORT" "STARTED" "URL" "CHECKOUT"
  printf '%-4s %-8s %-6s %-28s %-30s %s\n' "----" "--------" "------" "----------------------------" "------------------------------" "--------"
}

print_instance_row() {
  local num="$1"
  local pid="$2"
  local port="$3"
  local started="$4"
  local url="$5"
  local checkout="$6"
  local note="${7:-}"
  if [[ -n "$note" ]]; then
    printf '%-4s %-8s %-6s %-28s %-30s %s  (%s)\n' "$num" "$pid" "$port" "$started" "$url" "$checkout" "$note"
  else
    printf '%-4s %-8s %-6s %-28s %-30s %s\n' "$num" "$pid" "$port" "$started" "$url" "$checkout"
  fi
}

do_instances() {
  collect_instances
  local count="${#INSTANCE_PIDS[@]}"
  local i

  print_instances_table_header
  if [[ "$count" -eq 0 ]]; then
    echo "(no running SessionInsight instances found)"
  else
    for ((i = 0; i < count; i++)); do
      print_instance_row "$((i + 1))" \
        "${INSTANCE_PIDS[$i]}" \
        "${INSTANCE_PORTS[$i]}" \
        "${INSTANCE_STARTEDS[$i]}" \
        "${INSTANCE_URLS[$i]}" \
        "${INSTANCE_CHECKOUTS[$i]}" \
        "${INSTANCE_NOTES[$i]}"
    done
    echo
    echo "Total: $count instance(s)"
    echo "Stop by number: $0 kill <n> [n...]  (numbers are ephemeral — run status first)"
    echo "Only related-checkout rows are killable; external same-named binaries are listed only."
  fi

  if [[ "${#INSTANCE_STALE_LINES[@]}" -gt 0 ]]; then
    echo
    echo "Stale / unmatched PID files:"
    local line
    for line in "${INSTANCE_STALE_LINES[@]}"; do
      echo "  - $line"
    done
  fi
}

# Remove pid/url runtime files only when the pid file still names this pid.
cleanup_runtime_files_for_pid() {
  local pid="$1"
  local pid_file="$2"
  local url_file="$3"

  if [[ -z "$pid_file" || ! -f "$pid_file" ]]; then
    return 0
  fi

  local file_pid
  file_pid=$(tr -d '[:space:]' <"$pid_file" 2>/dev/null || true)
  if [[ "$file_pid" != "$pid" ]]; then
    # Replacement process (or empty/unrelated file) — leave records alone.
    return 0
  fi

  rm -f "$pid_file"
  if [[ -n "$url_file" && -f "$url_file" ]]; then
    rm -f "$url_file"
  fi
}

# Stop one collected instance by 0-based index. Re-validates ownership first.
stop_listed_instance() {
  local idx="$1"
  local num=$((idx + 1))
  local pid="${INSTANCE_PIDS[$idx]}"
  local bin="${INSTANCE_BINS[$idx]}"
  local checkout="${INSTANCE_CHECKOUTS[$idx]}"
  local pid_file="${INSTANCE_PID_FILES[$idx]}"
  local url_file="${INSTANCE_URL_FILES[$idx]}"
  local port="${INSTANCE_PORTS[$idx]}"
  local url="${INSTANCE_URLS[$idx]}"
  local killable="${INSTANCE_KILLABLE[$idx]:-0}"

  echo "==> Stopping #$num  PID=$pid  port=$port  $url"
  echo "    Checkout: $checkout"

  if [[ "$killable" != "1" ]]; then
    echo "ERROR: instance #$num is external/non-killable (not a related checkout of this repo)"
    return 1
  fi

  if ! kill -0 "$pid" 2>/dev/null; then
    echo "    Process already exited"
    cleanup_runtime_files_for_pid "$pid" "$pid_file" "$url_file"
    return 0
  fi

  # Require the process still to be this checkout's binary — no basename-only fallback.
  if [[ -z "$bin" ]] || ! pid_matches_bin "$pid" "$bin"; then
    echo "ERROR: refusing to stop PID $pid — not the session-insight binary for $checkout"
    return 1
  fi

  kill "$pid" 2>/dev/null || true
  sleep 0.5
  if kill -0 "$pid" 2>/dev/null; then
    kill -9 "$pid" 2>/dev/null || true
  fi
  if kill -0 "$pid" 2>/dev/null; then
    echo "ERROR: PID $pid is still running"
    return 1
  fi

  cleanup_runtime_files_for_pid "$pid" "$pid_file" "$url_file"
  echo "    Stopped"
  return 0
}

do_kill() {
  if [[ "$#" -eq 0 ]]; then
    echo "Usage: $0 kill <n> [n...]"
    echo "  n is the # column from \`$0 status\` (All instances table)."
    echo "  Numbers are rebuilt each run and may change — run status immediately before kill."
    echo "  External same-named binaries are listed but cannot be killed."
    echo
    do_instances
    return 1
  fi

  collect_instances
  local count="${#INSTANCE_PIDS[@]}"
  if [[ "$count" -eq 0 ]]; then
    echo "No running SessionInsight instances to kill"
    return 1
  fi

  local -A seen_idx=()
  local -a targets=()
  local arg num idx
  for arg in "$@"; do
    if [[ ! "$arg" =~ ^[1-9][0-9]*$ ]]; then
      echo "ERROR: invalid instance number '$arg' (use positive integers from status)"
      return 1
    fi
    num=$arg
    # Length + equal-length lexical compare: overflow-safe vs bash $((num-1)) wrap
    # (e.g. 18446744073709551616 must not become idx=-1 and select the last row).
    if [[ ${#num} -gt ${#count} ]] || { [[ ${#num} -eq ${#count} ]] && [[ "$num" > "$count" ]]; }; then
      echo "ERROR: instance #$num out of range (1-$count); run \`$0 status\` again"
      return 1
    fi
    idx=$((num - 1))
    if [[ -n "${seen_idx[$idx]+x}" ]]; then
      continue
    fi
    seen_idx["$idx"]=1
    targets+=("$idx")
  done

  local failed=0
  for idx in "${targets[@]}"; do
    if ! stop_listed_instance "$idx"; then
      failed=1
    fi
  done
  return "$failed"
}

do_status() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid=$(tr -d '[:space:]' <"$PID_FILE" 2>/dev/null || true)
    if [[ -n "$pid" ]] && pid_is_owned "$pid"; then
      local started _url _port
      started=$(process_start_time "$pid")
      [[ -z "$started" ]] && started="-"
      resolve_instance_url_port "$pid" "$URL_FILE" "$LOG_FILE"
      # Do not invent URL/port from this shell's PORT when process metadata is unknown.
      echo "SessionInsight is running"
      echo "  PID:     $pid"
      echo "  Port:    $_port"
      echo "  Started: $started"
      echo "  URL:     $_url"
      echo "  Data:    $(process_data_dir "$pid")"
      echo "  Checkout: $ROOT_DIR"
    else
      echo "SessionInsight is NOT running (stale PID file: ${pid:-empty})"
    fi
  else
    echo "SessionInsight is NOT running in this checkout"
  fi

  echo
  echo "All instances:"
  do_instances
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
  kill)
    shift
    do_kill "$@"
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
