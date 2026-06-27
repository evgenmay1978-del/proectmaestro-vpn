#!/bin/bash
# Maestro session-start orientation — injected into EVERY new chat (and on resume/compact)
# by the SessionStart hook so the assistant is NEVER blind/forgetful. Pulls LIVE state so it
# can't go stale like a hand-written memory claim. Must be fast + never fail.
set +e
cd /root/maestrovpn-tv 2>/dev/null
INFRA=/root/.claude/maestro-infra.md

# self-heal: if the infra map is missing or >45 min old, refresh it in the background (non-blocking)
AGE=$(( $(date +%s) - $(stat -c %Y "$INFRA" 2>/dev/null || echo 0) ))
[ "$AGE" -gt 2700 ] && nohup bash /root/.claude/maestro-infra.sh >/dev/null 2>&1 &
AGEMIN=$(( AGE / 60 ))

OTA=$(curl -fsS --max-time 4 https://wapmixx.ru:8911/update/update.json 2>/dev/null \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d.get("version_name"),"(code",str(d.get("version_code"))+")")' 2>/dev/null)
REPDAY=/var/lib/maestro/reports/reports-$(date +%Y-%m-%d).jsonl
REPN=$(wc -l < "$REPDAY" 2>/dev/null || echo 0)
REPTOT=$(cat /var/lib/maestro/reports/*.jsonl 2>/dev/null | wc -l)

cat <<EOF
===== MAESTRO ORIENTATION (auto-injected, $(date -u +%Y-%m-%dT%H:%MZ)) =====
You already OWN this project. Do NOT make the owner re-explain it. Orient from memory+graph, then act.

STANDING RULES (non-negotiable):
 1. HARD RULE #1 — live paying clients on S1/S2/S3; never harm a connected client.
 2. VERIFY END-TO-END before "done/LIVE" — prove the USER-FACING result (public edge, real OTA install,
    actual traffic), not just "parts compile". (Telemetry sat at 404 for days while memory said "LIVE".)
 3. DISTRIBUTE LOAD across S1/S2/S3 as ONE organism — run heavy builds on the LEAST-loaded node with RAM
    headroom (see infra map), NOT reflexively on S1. Know WHERE everything is installed (the map below).
 4. Work from MEMORY + the GRAPH; persist durable facts the SAME turn. Act decisively; don't over-ask.
 5. Reply in Russian.

LIVE STATE (pulled just now — trust over stale memory):
 - git: $(git rev-parse --abbrev-ref HEAD 2>/dev/null) @ $(git rev-parse --short HEAD 2>/dev/null) | uncommitted: $(git status --porcelain 2>/dev/null | wc -l) file(s)
 - fleet OTA live: ${OTA:-"(unreachable)"}
 - fleet crash reports: ${REPN} today / ${REPTOT} total  → READ them: tail /var/lib/maestro/reports/*.jsonl

INFRA MAP — where everything runs + how loaded (auto-probed ${AGEMIN}min ago; full: $INFRA):
$(grep -E '^### |  host:|  load:' "$INFRA" 2>/dev/null | sed -E 's/ — / · /; s/^/   /' | head -12)

IN-FLIGHT WORK (handoff — keep /root/.claude/maestro-state.md current as you work):
$(sed 's/^/   /' /root/.claude/maestro-state.md 2>/dev/null | head -30 || echo "   (none recorded)")

FIRST MOVES: (1) read MEMORY.md index + the project memory file for the task; (2) query the graph
(/graphify or graphify-out/GRAPH_REPORT.md) scoped to the code path; (3) THEN act. Never restart from zero.
==========================================================================
EOF
exit 0
