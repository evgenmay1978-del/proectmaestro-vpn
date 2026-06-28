#!/bin/sh
# Summarize the fleet crash/diagnostic telemetry the app uploads to S1 (POST /report →
# /var/lib/maestro/reports/*.jsonl). Read-only — READ THIS instead of waiting for «клиенты говорят».
#
# Usage: ops/crash-reports.sh
set -u
D=/var/lib/maestro/reports
TODAY=$(date -u +%Y-%m-%d)

ALL=$(cat "$D"/*.jsonl 2>/dev/null | wc -l)
REAL=$(cat "$D"/*.jsonl 2>/dev/null | grep -cv 'audit-probe\|"kind":"probe"')
TDY=$(cat "$D"/*.jsonl 2>/dev/null | grep -c "$TODAY")
echo "fleet reports: $ALL total · $REAL real (excl. probes) · $TDY today ($TODAY UTC)"

echo "by message/kind:"
cat "$D"/*.jsonl 2>/dev/null | jq -r '(.msg // .kind // "?")' 2>/dev/null | sort | uniq -c | sort -rn | head -8 | sed 's/^/  /'

echo "by app version:"
cat "$D"/*.jsonl 2>/dev/null | jq -r '(.v // .version // "?")' 2>/dev/null | sort | uniq -c | sort -rn | head -6 | sed 's/^/  /'

echo "recent real reports (newest last):"
cat "$D"/*.jsonl 2>/dev/null | jq -c 'select((.device // "") != "audit" and (.kind // "") != "probe") | {at, v, device, msg: ((.msg // "")|.[0:60])}' 2>/dev/null | tail -6 | sed 's/^/  /'
