#!/usr/bin/env bash
set -euo pipefail

readonly RQLITE_VERSION=10.1.0
readonly RQLITE_SHA256=9dca2fc957ee9445bdb94c08ca0ccd1b761d33c7e6fd729c224d1066594a3375
readonly ARCHIVE_NAME="rqlite-v${RQLITE_VERSION}-linux-amd64.tar.gz"
readonly DOWNLOAD_URL="https://github.com/rqlite/rqlite/releases/download/v${RQLITE_VERSION}/${ARCHIVE_NAME}"
readonly MARKER_NAME="maestro-rqlite-ci-root"

fail() {
  printf 'ci-rqlite: %s\n' "$*" >&2
  return 1
}

runner_base() {
  local candidate resolved
  candidate="${RUNNER_TEMP:-/tmp}"
  [[ -d "$candidate" ]] || fail "runner temp is not a directory"
  resolved="$(realpath -e -- "$candidate")" || fail "runner temp cannot be resolved"
  [[ -d "$resolved" ]] || fail "resolved runner temp is not a directory"
  printf '%s\n' "$resolved"
}

marker_path() {
  local base
  base="$(runner_base)" || return
  printf '%s/%s\n' "$base" "$MARKER_NAME"
}

validated_root() {
  local base marker root_from_marker resolved
  base="$(runner_base)" || return
  marker="$base/$MARKER_NAME"
  [[ -f "$marker" && ! -L "$marker" ]] || fail "cluster marker is missing or unsafe"

  mapfile -t marker_lines <"$marker"
  [[ "${#marker_lines[@]}" -eq 1 && -n "${marker_lines[0]}" ]] ||
    fail "cluster marker must contain one path"
  root_from_marker="${marker_lines[0]}"
  resolved="$(realpath -e -- "$root_from_marker")" || fail "cluster root cannot be resolved"
  [[ "$root_from_marker" == "$resolved" && -d "$resolved" && ! -L "$resolved" ]] ||
    fail "cluster root is not a resolved directory"
  case "$resolved" in
    "$base"/maestro-rqlite-ci.*) ;;
    *) fail "cluster root escaped runner temp" ;;
  esac
  printf '%s\n' "$resolved"
}

require_commands() {
  local command_name
  for command_name in curl sha256sum tar mktemp realpath python3 readlink tail; do
    command -v "$command_name" >/dev/null 2>&1 || fail "missing command: $command_name"
  done
}

assert_ports_available() {
  python3 - <<'PY'
import socket

sockets = []
try:
    for port in (4401, 4402, 4403, 4404, 4405, 4406):
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 0)
        sock.bind(("127.0.0.1", port))
        sockets.append(sock)
finally:
    for sock in sockets:
        sock.close()
PY
}

write_marker() {
  local marker root
  marker="$(marker_path)" || return
  root="$1"
  if [[ -e "$marker" || -L "$marker" ]]; then
    fail "cluster marker already exists"
    return
  fi
  (set -o noclobber; printf '%s\n' "$root" >"$marker") 2>/dev/null ||
    fail "cluster marker could not be created atomically"
}

write_pid() {
  local root node pid path
  root="$1"
  node="$2"
  pid="$3"
  path="$root/node${node}.pid"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || fail "invalid PID for node${node}"
  (set -o noclobber; printf '%s\n' "$pid" >"$path") 2>/dev/null ||
    fail "PID file for node${node} already exists"
}

wait_ready() {
  local port pid attempt
  port="$1"
  pid="$2"
  for ((attempt = 1; attempt <= 120; attempt++)); do
    if curl --fail --silent --show-error --max-time 2 \
      "http://127.0.0.1:${port}/readyz" >/dev/null 2>&1; then
      return 0
    fi
    kill -0 "$pid" 2>/dev/null || fail "node on HTTP port ${port} exited before ready"
    sleep 0.25
  done
  fail "node on HTTP port ${port} did not become ready"
}

verify_cluster() {
  python3 - <<'PY'
import json
import time
import urllib.error
import urllib.request

deadline = time.monotonic() + 30
last_error = "cluster did not converge"
last_nodes = {}
while time.monotonic() < deadline:
    try:
        with urllib.request.urlopen(
            "http://127.0.0.1:4401/nodes?ver=2&timeout=2s", timeout=5
        ) as response:
            payload = json.load(response)
        if isinstance(payload, dict) and isinstance(payload.get("nodes"), list):
            nodes = {node["id"]: node for node in payload["nodes"]}
        elif isinstance(payload, dict):
            nodes = payload
        else:
            raise RuntimeError("nodes response is not an object")
        last_nodes = nodes
        if len(nodes) != 3:
            raise RuntimeError(f"node count is {len(nodes)}")
        if sum(bool(node.get("leader")) for node in nodes.values()) != 1:
            raise RuntimeError("leader count is not 1")
        if not all(node.get("voter") is True for node in nodes.values()):
            raise RuntimeError("not every node is a voter")
        if not all(node.get("reachable") is True for node in nodes.values()):
            raise RuntimeError("not every node is reachable")

        for port in (4401, 4403, 4405):
            request = urllib.request.Request(
                f"http://127.0.0.1:{port}/db/request?associative=true&level=linearizable",
                data=json.dumps([["PRAGMA foreign_keys"]]).encode("utf-8"),
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with urllib.request.urlopen(request, timeout=5) as response:
                payload = json.load(response)
            value = payload["results"][0]["rows"][0]["foreign_keys"]
            if value != 1:
                raise RuntimeError(f"foreign_keys={value!r} on {port}")
    except (
        KeyError,
        IndexError,
        TypeError,
        ValueError,
        TimeoutError,
        RuntimeError,
        urllib.error.URLError,
    ) as exc:
        last_error = str(exc)
        time.sleep(0.25)
        continue
    print("leaders=1 voters=3")
    print("foreign_keys=1 endpoints=3")
    raise SystemExit(0)
raise SystemExit(f"{last_error}; nodes={json.dumps(last_nodes, sort_keys=True)}")
PY
}

read_recorded_pid() {
  local root node binary pid_file pid executable
  root="$1"
  node="$2"
  binary="$root/bin/rqlited"
  pid_file="$root/node${node}.pid"
  [[ -f "$pid_file" && ! -L "$pid_file" ]] || fail "unsafe PID file for node${node}"
  mapfile -t pid_lines <"$pid_file"
  [[ "${#pid_lines[@]}" -eq 1 && "${pid_lines[0]}" =~ ^[1-9][0-9]*$ ]] ||
    fail "invalid PID file for node${node}"
  pid="${pid_lines[0]}"
  if ! kill -0 "$pid" 2>/dev/null; then
    printf '\n'
    return 0
  fi
  executable="$(readlink -f -- "/proc/${pid}/exe")" || fail "cannot resolve node${node} executable"
  [[ "$executable" == "$binary" ]] || fail "recorded PID for node${node} is not rqlited from cluster root"
  printf '%s\n' "$pid"
}

start_cluster() {
  local base marker root archive expected_member binary pid join_targets
  local -a started_pids=()
  umask 077
  require_commands
  assert_ports_available
  base="$(runner_base)" || return
  marker="$base/$MARKER_NAME"
  [[ ! -e "$marker" && ! -L "$marker" ]] || fail "cluster is already started or marker is stale"

  root="$(mktemp -d "$base/maestro-rqlite-ci.XXXXXX")" || fail "cannot create cluster temp root"
  root="$(realpath -e -- "$root")" || fail "cannot resolve cluster temp root"
  case "$root" in
    "$base"/maestro-rqlite-ci.*) ;;
    *) fail "mktemp root escaped runner temp" ;;
  esac
  write_marker "$root" || {
    rmdir -- "$root" 2>/dev/null || true
    return 1
  }

  cleanup_failed_start() {
    local status="$1" cleanup_base="$2" cleanup_marker="$3" cleanup_root="$4"
    local cleanup_pid attempt any_running resolved_cleanup_root executable node log_file
    shift 4
    local -a cleanup_pids=("$@")
    local -a cleanup_marker_lines=()
    trap - EXIT
    if [[ "$status" -ne 0 ]]; then
      for node in 1 2 3; do
        log_file="$cleanup_root/node${node}.log"
        if [[ -f "$log_file" && ! -L "$log_file" ]]; then
          printf 'ci-rqlite: node%s startup log (last 80 lines)\n' "$node" >&2
          tail -n 80 -- "$log_file" >&2 || true
        fi
      done
      executable="$cleanup_root/bin/rqlited"
      for cleanup_pid in "${cleanup_pids[@]}"; do
        if kill -0 "$cleanup_pid" 2>/dev/null &&
          [[ -f "$executable" && ! -L "$executable" ]] &&
          [[ "$(readlink -f -- "/proc/${cleanup_pid}/exe" 2>/dev/null)" == "$executable" ]]; then
          kill -TERM "$cleanup_pid" 2>/dev/null || true
        fi
      done
      for ((attempt = 1; attempt <= 40; attempt++)); do
        any_running=0
        for cleanup_pid in "${cleanup_pids[@]}"; do
          if kill -0 "$cleanup_pid" 2>/dev/null; then
            any_running=1
          fi
        done
        [[ "$any_running" -eq 0 ]] && break
        sleep 0.25
      done
      for cleanup_pid in "${cleanup_pids[@]}"; do
        if kill -0 "$cleanup_pid" 2>/dev/null &&
          [[ -f "$executable" && ! -L "$executable" ]] &&
          [[ "$(readlink -f -- "/proc/${cleanup_pid}/exe" 2>/dev/null)" == "$executable" ]]; then
          kill -KILL "$cleanup_pid" 2>/dev/null || true
        fi
        wait "$cleanup_pid" 2>/dev/null || true
      done
      any_running=0
      for cleanup_pid in "${cleanup_pids[@]}"; do
        if kill -0 "$cleanup_pid" 2>/dev/null; then
          any_running=1
        fi
      done
      if [[ "$any_running" -eq 0 ]]; then
        resolved_cleanup_root="$(realpath -e -- "$cleanup_root" 2>/dev/null || true)"
        if [[ -f "$cleanup_marker" && ! -L "$cleanup_marker" ]]; then
          mapfile -t cleanup_marker_lines <"$cleanup_marker" || true
        fi
        if [[ "$resolved_cleanup_root" == "$cleanup_root" && -d "$cleanup_root" && ! -L "$cleanup_root" &&
          "${#cleanup_marker_lines[@]}" -eq 1 && "${cleanup_marker_lines[0]}" == "$cleanup_root" ]]; then
          case "$cleanup_root" in
            "$cleanup_base"/maestro-rqlite-ci.*)
              rm -rf -- "$cleanup_root"
              rm -f -- "$cleanup_marker"
              ;;
          esac
        fi
      fi
    fi
    exit "$status"
  }

  install_cleanup_trap() {
    local trap_command trap_pid
    printf -v trap_command 'cleanup_failed_start "$?" %q %q %q' "$base" "$marker" "$root"
    for trap_pid in "${started_pids[@]}"; do
      printf -v trap_command '%s %q' "$trap_command" "$trap_pid"
    done
    trap "$trap_command" EXIT
  }

  install_cleanup_trap

  mkdir -p -- "$root/bin" "$root/node1" "$root/node2" "$root/node3"
  archive="$root/$ARCHIVE_NAME"
  curl --fail --silent --show-error --location \
    --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --output "$archive" "$DOWNLOAD_URL"
  printf '%s  %s\n' "$RQLITE_SHA256" "$archive" | sha256sum -c - >/dev/null

  expected_member="rqlite-v${RQLITE_VERSION}-linux-amd64/rqlited"
  [[ "$(tar -tzf "$archive" | grep -cFx "$expected_member" || true)" == 1 ]] ||
    fail "archive does not contain exactly one expected rqlited binary"
  [[ "$(tar -tvzf "$archive" "$expected_member" | cut -c1)" == "-" ]] ||
    fail "rqlited archive member is not a regular file"
  binary="$root/bin/rqlited"
  tar -xOzf "$archive" "$expected_member" >"$binary"
  chmod 0700 "$binary"
  [[ -x "$binary" ]] || fail "rqlited binary is not executable"
  join_targets="127.0.0.1:4402,127.0.0.1:4404,127.0.0.1:4406"

  "$binary" \
    -node-id ci-rqlite-1 \
    -http-addr 127.0.0.1:4401 \
    -raft-addr 127.0.0.1:4402 \
    -bootstrap-expect 3 \
    -bootstrap-expect-timeout 30s \
    -join "$join_targets" \
    -join-attempts 120 \
    -join-interval 250ms \
    -fk \
    "$root/node1" >"$root/node1.log" 2>&1 &
  pid="$!"
  started_pids+=("$pid")
  install_cleanup_trap
  write_pid "$root" 1 "$pid"

  "$binary" \
    -node-id ci-rqlite-2 \
    -http-addr 127.0.0.1:4403 \
    -raft-addr 127.0.0.1:4404 \
    -bootstrap-expect 3 \
    -bootstrap-expect-timeout 30s \
    -join "$join_targets" \
    -join-attempts 120 \
    -join-interval 250ms \
    -fk \
    "$root/node2" >"$root/node2.log" 2>&1 &
  pid="$!"
  started_pids+=("$pid")
  install_cleanup_trap
  write_pid "$root" 2 "$pid"

  "$binary" \
    -node-id ci-rqlite-3 \
    -http-addr 127.0.0.1:4405 \
    -raft-addr 127.0.0.1:4406 \
    -bootstrap-expect 3 \
    -bootstrap-expect-timeout 30s \
    -join "$join_targets" \
    -join-attempts 120 \
    -join-interval 250ms \
    -fk \
    "$root/node3" >"$root/node3.log" 2>&1 &
  pid="$!"
  started_pids+=("$pid")
  install_cleanup_trap
  write_pid "$root" 3 "$pid"

  wait_ready 4401 "$(<"$root/node1.pid")"
  wait_ready 4403 "$(<"$root/node2.pid")"
  wait_ready 4405 "$(<"$root/node3.pid")"
  verify_cluster
  trap - EXIT
  printf 'ci-rqlite cluster started\n'
}

status_cluster() {
  local root node pid
  root="$(validated_root)" || return
  printf 'root=%s\n' "$root"
  for node in 1 2 3; do
    pid="$(read_recorded_pid "$root" "$node")" || return
    [[ -n "$pid" ]] || fail "node${node} is not running"
    printf 'node%s_pid=%s\n' "$node" "$pid"
  done
  verify_cluster
}

stop_cluster() {
  local base marker root node pid attempt any_running executable
  local -a pids=()
  base="$(runner_base)" || return
  marker="$base/$MARKER_NAME"
  if [[ ! -e "$marker" && ! -L "$marker" ]]; then
    printf 'ci-rqlite cluster already stopped\n'
    return 0
  fi
  root="$(validated_root)" || return
  executable="$root/bin/rqlited"
  [[ -f "$executable" && ! -L "$executable" ]] || fail "cluster binary is missing or unsafe"

  for node in 1 2 3; do
    pid="$(read_recorded_pid "$root" "$node")" || return
    if [[ -n "$pid" ]]; then
      pids+=("$pid")
    fi
  done
  if [[ "${#pids[@]}" -gt 0 ]]; then
    kill -TERM "${pids[@]}"
  fi
  for ((attempt = 1; attempt <= 40; attempt++)); do
    any_running=0
    for pid in "${pids[@]}"; do
      if kill -0 "$pid" 2>/dev/null; then
        any_running=1
      fi
    done
    [[ "$any_running" -eq 0 ]] && break
    sleep 0.25
  done
  for pid in "${pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      [[ "$(readlink -f -- "/proc/${pid}/exe")" == "$executable" ]] ||
        fail "refusing to kill a reused PID"
      kill -KILL "$pid"
    fi
  done

  rm -rf -- "$root"
  rm -f -- "$marker"
  printf 'ci-rqlite cluster stopped\n'
}

usage() {
  printf 'usage: %s start|status|stop\n' "${0##*/}" >&2
  return 2
}

case "${1:-}" in
  start) start_cluster ;;
  status) status_cluster ;;
  stop) stop_cluster ;;
  *) usage ;;
esac
