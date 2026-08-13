#!/usr/bin/env bash
# Unit tests for run.sh worktree port isolation (no live server required).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail=0
passed=0

assert_eq() {
  local got="$1" want="$2" name="$3"
  if [[ "$got" == "$want" ]]; then
    passed=$((passed + 1))
    return 0
  fi
  echo "FAIL: $name"
  echo "  got:  [$got]"
  echo "  want: [$want]"
  fail=1
}

# Isolate this process from agent shells that export PORT=8080.
saved_port="${PORT-}"
port_was_set=0
if [[ -n "${PORT+x}" ]]; then
  port_was_set=1
fi
unset PORT

# Source run.sh to load helpers without dispatching a command.
# shellcheck source=../run.sh
source "$ROOT/run.sh"

assert_eq "$(is_valid_tcp_port 8080 && echo yes || echo no)" "yes" "8080 is a valid tcp port"
assert_eq "$(is_valid_tcp_port 0 && echo yes || echo no)" "no" "0 is not a valid tcp port"
assert_eq "$(is_valid_tcp_port 65536 && echo yes || echo no)" "no" "65536 is not a valid tcp port"
assert_eq "$(is_valid_tcp_port abc && echo yes || echo no)" "no" "non-numeric is not a valid tcp port"
assert_eq "$(is_primary_reserved_port 8080 && echo yes || echo no)" "yes" "8080 is reserved for primary"
assert_eq "$(is_primary_reserved_port 37307 && echo yes || echo no)" "no" "ephemeral port is not reserved"

assert_eq "$(select_worktree_listen_port '' '')" "0" "empty request + empty saved → OS-assigned"
assert_eq "$(select_worktree_listen_port 8080 '')" "0" "inherited PORT=8080 is ignored"
assert_eq "$(select_worktree_listen_port 8080 8080)" "0" "inherited + persisted 8080 are both ignored"
assert_eq "$(select_worktree_listen_port '' 8080)" "0" "poisoned PORT_FILE=8080 is ignored"
assert_eq "$(select_worktree_listen_port '' 38487)" "38487" "persisted non-reserved port is reused"
assert_eq "$(select_worktree_listen_port 38487 8080)" "38487" "explicit non-reserved PORT wins over poisoned file"
assert_eq "$(select_worktree_listen_port 9090 38487)" "9090" "explicit PORT wins over a different saved port"
assert_eq "$(select_worktree_listen_port 8080 38487)" "38487" "ignored 8080 falls back to saved worktree port"

tmpdir=$(mktemp -d)
trap 'rm -rf -- "$tmpdir"' EXIT

# persist_worktree_bound_port refuses 8080 and writes a real bound port.
PORT_FILE="$tmpdir/session-insight.port"
persist_worktree_bound_port "http://127.0.0.1:8080/"
if [[ -f "$PORT_FILE" ]]; then
  echo "FAIL: persist_worktree_bound_port wrote reserved port 8080"
  fail=1
else
  passed=$((passed + 1))
fi
persist_worktree_bound_port "http://127.0.0.1:37307/"
assert_eq "$(cat "$PORT_FILE")" "37307" "persist_worktree_bound_port writes actual bound port"

# resolve_instance_url_port prefers the listen log over a poisoned URL file.
url_file="$tmpdir/session-insight.url"
log_file="$tmpdir/session-insight.log"
printf '%s\n' "http://127.0.0.1:8080/" >"$url_file"
cat >"$log_file" <<'EOF'
2026/08/13 08:04:51 port 8080 is in use, falling back to an OS-assigned port
2026/08/13 08:04:51 SessionInsight listening on http://127.0.0.1:37307/
EOF
# PID 1 is not this binary's listener; ss should not match, so log wins.
resolve_instance_url_port 1 "$url_file" "$log_file"
assert_eq "$_url" "http://127.0.0.1:37307/" "status URL comes from listen log, not poisoned url file"
assert_eq "$_port" "37307" "status port comes from listen log, not poisoned url file"

# listening_url_from_file parses the last Ready line.
assert_eq "$(listening_url_from_file "$log_file")" "http://127.0.0.1:37307/" "listening_url_from_file reads bind line"
assert_eq "$(listening_url_from_file "$tmpdir/missing.log")" "" "listening_url_from_file missing file is empty"
assert_eq "$(port_from_url "http://127.0.0.1:37307/")" "37307" "port_from_url strips path slash"

if [[ "$port_was_set" -eq 1 ]]; then
  PORT="${saved_port-}"
  export PORT
fi

if [[ "$fail" -ne 0 ]]; then
  echo "run.sh port isolation tests FAILED ($passed passed)"
  exit 1
fi
echo "run.sh port isolation tests passed ($passed)"
