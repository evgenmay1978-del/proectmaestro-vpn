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

# Update outcomes (kind:"update", added 2026-07-30). WHY this block exists: the APK bytes come
# from the Yandex mirror, not our nginx, so before these events «постоянно загрузка» on a TV was
# unfalsifiable from S1 — slow link, no storage and un-pressed «Установить?» all looked the same.
# .msg is the stage, .stack the key=value detail. See memory/tv-update-stuck-loading-2026-07-30.
UPD=$(cat "$D"/*.jsonl 2>/dev/null | grep -c '"kind":"update"')
echo "update outcomes: $UPD"
if [ "$UPD" -gt 0 ]; then
  cat "$D"/*.jsonl 2>/dev/null | jq -r 'select(.kind=="update") | .msg' 2>/dev/null |
    sort | uniq -c | sort -rn | sed 's/^/  /'
  echo "  latest failures:"
  cat "$D"/*.jsonl 2>/dev/null |
    jq -r 'select(.kind=="update" and (.msg|test("fail|confirm|verdict"))) |
           "  \(.at) api=\(.api) vc=\(.vc) \(.device) :: \(.msg) \(.stack)"' 2>/dev/null |
    tail -8
fi

echo "by message/kind:"
cat "$D"/*.jsonl 2>/dev/null | jq -r '(.msg // .kind // "?")' 2>/dev/null | sort | uniq -c | sort -rn | head -8 | sed 's/^/  /'

echo "by app version:"
cat "$D"/*.jsonl 2>/dev/null | jq -r '(.v // .version // "?")' 2>/dev/null | sort | uniq -c | sort -rn | head -6 | sed 's/^/  /'

echo "recent real reports (newest last):"
cat "$D"/*.jsonl 2>/dev/null | jq -c 'select((.device // "") != "audit" and (.kind // "") != "probe") | {at, v, device, msg: ((.msg // "")|.[0:60])}' 2>/dev/null | tail -6 | sed 's/^/  /'
