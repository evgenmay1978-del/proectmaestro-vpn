#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 0 ]; then
  printf '%s\n' '{"error":"arguments_invalid"}' >&2
  exit 2
fi

script_source="${BASH_SOURCE[0]}"
case "$script_source" in
  */*) script_dir="${script_source%/*}" ;;
  *) script_dir=. ;;
esac
SCRIPT_DIR="$(CDPATH= cd -- "$script_dir" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"

export GOPROXY=off
export GOSUMDB=off
export GONOSUMDB='*'

run_required_tests() {
  local package="$1"
  local pattern="$2"
  local result_file
  local status
  shift 2

  result_file="$(mktemp)"
  if go test -json -mod=readonly -count=1 "$package" -run "$pattern" >"$result_file"; then
    :
  else
    status=$?
    cat "$result_file" >&2
    rm -f "$result_file"
    return "$status"
  fi

  if python - "$result_file" "$@" <<'PY'
from __future__ import annotations

import collections
import json
import sys

result_path, *required = sys.argv[1:]
pass_counts: collections.Counter[str] = collections.Counter()
skipped: set[str] = set()

with open(result_path, encoding="utf-8") as result:
    for line in result:
        event = json.loads(line)
        test_name = event.get("Test")
        if test_name not in required:
            continue
        if event.get("Action") == "pass":
            pass_counts[test_name] += 1
        elif event.get("Action") == "skip":
            skipped.add(test_name)

invalid = [
    test_name
    for test_name in required
    if pass_counts[test_name] != 1 or test_name in skipped
]
if invalid:
    print("required_test_event_invalid:" + ",".join(invalid), file=sys.stderr)
    raise SystemExit(1)
PY
  then
    rm -f "$result_file"
  else
    status=$?
    rm -f "$result_file"
    return "$status"
  fi
}

cd "$REPO_ROOT/backend"
run_required_tests ./internal/whitelistbalance \
  '^(TestAdvanceUsesHalfOpenBoundaryAndExpiresOnlyUnusedIncluded|TestApplyUsageDebitsOldPeriodBeforeBoundaryRollover)$' \
  TestAdvanceUsesHalfOpenBoundaryAndExpiresOnlyUnusedIncluded \
  TestApplyUsageDebitsOldPeriodBeforeBoundaryRollover
run_required_tests ./internal/controlplane \
  '^TestConfirmWhiteListTopUpPaymentCreditsOnceAndEnablesPublication$' \
  TestConfirmWhiteListTopUpPaymentCreditsOnceAndEnablesPublication
run_required_tests ./internal/whitelistready \
  '^TestIntegrationFixtureCompositionShadowMeteringKeysResetReplay$' \
  TestIntegrationFixtureCompositionShadowMeteringKeysResetReplay

printf '%s\n' '{"fixture":"whitelist-commercial-balance","harness_status":"PASS","proofs":4,"evidence_class":"OFFLINE_REPRO","release_readiness":"NO_GO"}'
