#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  printf '%s\n' '{"error":"arguments_invalid"}' >&2
  exit 2
fi

suite="$1"
case "$suite" in
  yandex_get_body)
    ;;
  yandex_active_stream)
    ;;
  yandex_idle_cutoff)
    ;;
  yandex_literal_edge)
    ;;
  xray_counter_reset)
    ;;
  billing_idempotency)
    ;;
  duplicate_event_replay)
    ;;
  subscription_escaping)
    ;;
  edge_rotation)
    ;;
  *)
    printf '%s\n' '{"error":"arguments_invalid"}' >&2
    exit 2
    ;;
esac

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
binary="$(mktemp "${TMPDIR:-/tmp}/maestro-whitelist-ready.XXXXXX")"

cleanup() {
  rm -f -- "$binary"
}
trap cleanup EXIT INT TERM

(
  cd "$REPO_ROOT/backend"
  GOPROXY=off go build -o "$binary" ./cmd/maestro-whitelist-ready
)

cd "$REPO_ROOT"
output="$(
  "$binary" replay \
    --suite "$suite" \
    --catalog scripts/repro/fixtures/acceptance-catalog.v1.json \
    --evidence scripts/repro/fixtures/acceptance-evidence.v1.json \
    --matrix scripts/repro/fixtures/client-compatibility-matrix.v1.json
)"
expected="$(printf '{"harness_status":"PASS","release_readiness":"NO_GO","evidence_class":"FIXTURE_REPLAY","selected_suite":"%s"}' "$suite")"
if [ "$output" != "$expected" ]; then
  printf '%s\n' '{"error":"unexpected_harness_output"}' >&2
  exit 1
fi
printf '%s\n' "$output"
