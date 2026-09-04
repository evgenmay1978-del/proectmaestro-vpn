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

cd "$REPO_ROOT/backend"
go test -mod=readonly -count=1 ./internal/whitelistbalance -run '^(TestAdvanceUsesHalfOpenBoundaryAndExpiresOnlyUnusedIncluded|TestApplyUsageDebitsOldPeriodBeforeBoundaryRollover)$' >&2
go test -mod=readonly -count=1 ./internal/controlplane -run '^(TestConfirmWhiteListTopUpPaymentCreditsOnceAndEnablesPublication|TestWhiteListTopUpConfirmationCommitsOnceAndReplaysAfterUnknownOutcome)$' >&2
go test -mod=readonly -count=1 ./internal/whitelistready -run '^TestIntegrationFixtureCompositionShadowMeteringKeysResetReplay$' >&2

printf '%s\n' '{"fixture":"whitelist-commercial-balance","harness_status":"PASS","proofs":5,"evidence_class":"OFFLINE_REPRO","release_readiness":"NO_GO"}'
