#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HARNESS="$ROOT/ops/ha/ci-rqlite-cluster.sh"

fail() {
  printf 'ci-rqlite contract failed: %s\n' "$*" >&2
  exit 1
}

[[ -f "$HARNESS" ]] || fail "missing ops/ha/ci-rqlite-cluster.sh (expected RED)"
bash -n "$HARNESS"

version_count="$(grep -cF 'RQLITE_VERSION=10.1.0' "$HARNESS" || true)"
checksum_count="$(grep -cF '9dca2fc957ee9445bdb94c08ca0ccd1b761d33c7e6fd729c224d1066594a3375' "$HARNESS" || true)"
[[ "$version_count" == 1 ]] || fail "rqlite version pin must appear exactly once"
[[ "$checksum_count" == 1 ]] || fail "rqlite checksum pin must appear exactly once"

if grep -Eq 'curl[^|]*\|[[:space:]]*(sh|bash)' "$HARNESS"; then
  fail "download-and-execute pipeline is forbidden"
fi
grep -qF 'mktemp -d' "$HARNESS" || fail "mktemp -d is required"
grep -qF 'realpath' "$HARNESS" || fail "resolved-path validation is required"
grep -qF '127.0.0.1:4401' "$HARNESS" || fail "first loopback HTTP endpoint is missing"
grep -qF '127.0.0.1:4403' "$HARNESS" || fail "second loopback HTTP endpoint is missing"
grep -qF '127.0.0.1:4405' "$HARNESS" || fail "third loopback HTTP endpoint is missing"

grep -qF 'start-mtls) start_cluster mtls' "$HARNESS" ||
  fail "start-mtls command is missing"
grep -qF -- '-http-verify-client' "$HARNESS" ||
  fail "mTLS client verification flag is missing"
grep -qF 'subjectAltName=IP:127.0.0.1' "$HARNESS" ||
  fail "loopback server certificate SAN is missing"

for node_id in ci-rqlite-1 ci-rqlite-2 ci-rqlite-3; do
  count="$(grep -cF -- "-node-id $node_id" "$HARNESS" || true)"
  [[ "$count" == 1 ]] || fail "node ID $node_id must appear exactly once"
done

bootstrap_count="$(grep -cF -- '-bootstrap-expect 3' "$HARNESS" || true)"
join_flag_count="$(grep -cF -- '-join "$join_targets"' "$HARNESS" || true)"
join_target_count="$(grep -cF 'join_targets="127.0.0.1:4402,127.0.0.1:4404,127.0.0.1:4406"' "$HARNESS" || true)"
[[ "$bootstrap_count" == 3 ]] || fail "all three nodes must use bootstrap-expect 3"
[[ "$join_flag_count" == 3 ]] || fail "all three nodes must use the shared join target list"
[[ "$join_target_count" == 1 ]] ||
  fail "the complete loopback join target list must be declared exactly once"

executable_lines="$(grep -Ev '^[[:space:]]*(#|$)' "$HARNESS")"
if grep -Eq '(^|[^[:alnum:]_])(ssh|scp|rsync)([^[:alnum:]_]|$)|/etc/|/var/lib/|0\.0\.0\.0' <<<"$executable_lines"; then
  fail "host access or non-loopback bind is forbidden"
fi

parent_temp="$(realpath -e "${RUNNER_TEMP:-/tmp}")"
test_temp="$(mktemp -d "$parent_temp/maestro-rqlite-contract.XXXXXX")"
test_temp="$(realpath -e "$test_temp")"
case "$test_temp" in
  "$parent_temp"/maestro-rqlite-contract.*) ;;
  *) fail "test temp escaped runner temp" ;;
esac
export RUNNER_TEMP="$test_temp"

unrelated_pid=""
outside_root=""

cleanup() {
  bash "$HARNESS" stop >/dev/null 2>&1 || true
  if [[ -n "$unrelated_pid" ]] && kill -0 "$unrelated_pid" 2>/dev/null; then
    kill "$unrelated_pid" 2>/dev/null || true
    wait "$unrelated_pid" 2>/dev/null || true
  fi
  if [[ -n "$outside_root" && -d "$outside_root" ]]; then
    outside_resolved="$(realpath -e "$outside_root")"
    case "$outside_resolved" in
      "$parent_temp"/maestro-rqlite-outside.*) rm -rf -- "$outside_resolved" ;;
    esac
  fi
  if [[ -d "$test_temp" ]]; then
    resolved="$(realpath -e "$test_temp")"
    case "$resolved" in
      "$parent_temp"/maestro-rqlite-contract.*) rm -rf -- "$resolved" ;;
    esac
  fi
}
trap cleanup EXIT

fake_bin="$test_temp/fake-bin"
mkdir -p -- "$fake_bin"
cat >"$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 22
EOF
chmod 0700 "$fake_bin/curl"
failed_start_output=""
if failed_start_output="$(PATH="$fake_bin:$PATH" bash "$HARNESS" start 2>&1)"; then
  fail "start unexpectedly succeeded when the pinned download failed"
fi
[[ ! -e "$test_temp/maestro-rqlite-ci-root" ]] ||
  fail "failed start left a stale cluster marker; output: ${failed_start_output:-<none>}"
if compgen -G "$test_temp/maestro-rqlite-ci.*" >/dev/null; then
  fail "failed start left a stale cluster root; output: ${failed_start_output:-<none>}"
fi
rm -rf -- "$fake_bin"

bash "$HARNESS" start
status_output="$(bash "$HARNESS" status)"
printf '%s\n' "$status_output"

cluster_root="$(awk -F= '$1 == "root" { print $2 }' <<<"$status_output")"
[[ -n "$cluster_root" ]] || fail "status did not report the cluster root"
cluster_root="$(realpath -e "$cluster_root")"
case "$cluster_root" in
  "$test_temp"/maestro-rqlite-ci.*) ;;
  *) fail "cluster root escaped the isolated runner temp" ;;
esac

for node in 1 2 3; do
  [[ -f "$cluster_root/node${node}.pid" ]] || fail "node${node} PID file is missing"
done

python3 - <<'PY'
import json
import urllib.request

expected_ids = {"ci-rqlite-1", "ci-rqlite-2", "ci-rqlite-3"}
with urllib.request.urlopen(
    "http://127.0.0.1:4401/nodes?ver=2&timeout=5s", timeout=10
) as response:
    payload = json.load(response)
if isinstance(payload, dict) and isinstance(payload.get("nodes"), list):
    nodes = {node["id"]: node for node in payload["nodes"]}
elif isinstance(payload, dict):
    nodes = payload
else:
    raise SystemExit("nodes response is not an object")

if set(nodes) != expected_ids:
    raise SystemExit(f"unexpected node IDs: {sorted(nodes)}")
if sum(bool(node.get("leader")) for node in nodes.values()) != 1:
    raise SystemExit("cluster must have exactly one leader")
if not all(node.get("voter") is True for node in nodes.values()):
    raise SystemExit("all three CI nodes must be voters")
if not all(node.get("reachable") is True for node in nodes.values()):
    raise SystemExit("all three CI nodes must be reachable")

for port in (4401, 4403, 4405):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}/readyz", timeout=10) as response:
        if response.status != 200:
            raise SystemExit(f"node on {port} is not ready")
    request = urllib.request.Request(
        f"http://127.0.0.1:{port}/db/request?associative=true&level=linearizable",
        data=json.dumps([["PRAGMA foreign_keys"]]).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        payload = json.load(response)
    try:
        foreign_keys = payload["results"][0]["rows"][0]["foreign_keys"]
    except (KeyError, IndexError, TypeError) as exc:
        raise SystemExit(f"malformed PRAGMA response on {port}: {payload!r}") from exc
    if foreign_keys != 1:
        raise SystemExit(f"foreign_keys={foreign_keys!r} on {port}, want 1")
PY

sleep 120 &
unrelated_pid="$!"
bash "$HARNESS" stop
if ! kill -0 "$unrelated_pid" 2>/dev/null; then
  fail "stop killed a process whose PID was not recorded by the harness"
fi
kill "$unrelated_pid"
wait "$unrelated_pid" 2>/dev/null || true
unrelated_pid=""

[[ ! -e "$cluster_root" ]] || fail "stop did not remove the validated cluster root"
bash "$HARNESS" stop

outside_root="$(mktemp -d "$parent_temp/maestro-rqlite-outside.XXXXXX")"
outside_root="$(realpath -e "$outside_root")"
touch "$outside_root/must-survive"
printf '%s\n' "$outside_root" >"$test_temp/maestro-rqlite-ci-root"
if bash "$HARNESS" stop >/dev/null 2>&1; then
  fail "stop accepted an out-of-scope root marker"
fi
[[ -f "$outside_root/must-survive" ]] || fail "out-of-scope sentinel was modified"
rm -f -- "$test_temp/maestro-rqlite-ci-root"
rm -rf -- "$outside_root"
outside_root=""

bash "$HARNESS" start-mtls
mtls_status="$(bash "$HARNESS" status)"
mtls_root="$(awk -F= '$1 == "root" { print $2 }' <<<"$mtls_status")"
[[ -n "$mtls_root" ]] || fail "mTLS status did not report cluster root"
mtls_root="$(realpath -e "$mtls_root")"
case "$mtls_root" in
  "$test_temp"/maestro-rqlite-ci.*) ;;
  *) fail "mTLS cluster root escaped runner temp" ;;
esac
[[ "$(awk -F= '$1 == "mode" { print $2 }' <<<"$mtls_status")" == "mtls" ]] ||
  fail "mTLS status did not report mode=mtls"

for private_file in ca.key server.key client.key; do
  [[ -f "$mtls_root/tls/$private_file" && ! -L "$mtls_root/tls/$private_file" ]] ||
    fail "mTLS private file $private_file is missing or unsafe"
  [[ "$(stat -c '%a' "$mtls_root/tls/$private_file")" == "600" ]] ||
    fail "mTLS private file $private_file is not mode 0600"
done
for public_file in ca.crt server.crt client.crt; do
  [[ -f "$mtls_root/tls/$public_file" && ! -L "$mtls_root/tls/$public_file" ]] ||
    fail "mTLS certificate $public_file is missing or unsafe"
  [[ "$(stat -c '%a' "$mtls_root/tls/$public_file")" == "600" ]] ||
    fail "mTLS certificate $public_file is not mode 0600"
done

if curl --fail --silent --show-error --max-time 3 \
  --cacert "$mtls_root/tls/ca.crt" \
  "https://127.0.0.1:4401/readyz" >/dev/null 2>&1; then
  fail "mTLS endpoint accepted a client without a certificate"
fi
curl --fail --silent --show-error --max-time 3 \
  --cacert "$mtls_root/tls/ca.crt" \
  --cert "$mtls_root/tls/client.crt" \
  --key "$mtls_root/tls/client.key" \
  "https://127.0.0.1:4401/readyz" >/dev/null
bash "$HARNESS" stop
[[ ! -e "$mtls_root" ]] || fail "mTLS stop did not remove validated cluster root"

printf 'ci-rqlite contract passed\n'
