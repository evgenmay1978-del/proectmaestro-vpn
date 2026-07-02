#!/usr/bin/env bash
# PostToolUse(Bash) — token-saver, NARROW scope: shrink output ONLY for a small allow-list of
# known-NOISY commands (download/upload progress bars, unbounded journalctl). Every other command —
# all server diagnostics, live logs, config reads, greps — is left 100% untouched.
#
# SAFE BY DESIGN: the command has ALREADY executed (exit code, stderr, side effects unchanged); this
# rewrites only the TEXT the model sees. FAIL-SAFE: on any doubt (not a noisy command, wrong field,
# small output, jq missing) it prints NOTHING and exits 0 → the harness keeps the ORIGINAL output.
#
# What IS trimmed: aws s3 cp/sync/mv progress, wget/scp/rsync --progress, pip/apt/dnf/yum installs,
# docker pull/build, npm/yarn install, and `journalctl` with NO bound (-n/--lines/--since/… /pipe).
# What is NEVER trimmed: `journalctl -u x -n 30`, `journalctl … | grep`, ss, systemctl, cat, grep,
# curl API responses, or anything not on the noisy list — so live server logs are always full.

input=$(cat 2>/dev/null) || exit 0
command -v jq >/dev/null 2>&1 || exit 0

cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null) || exit 0
[ -z "$cmd" ] && exit 0

# ── Is this a known-noisy command whose output is pure ballast? ──
noisy=0
# download / upload progress + verbose installers (\b so it also matches inside ssh '…' quotes)
if printf '%s' "$cmd" | grep -qiE 'aws +s3 +(cp|sync|mv)|\b(wget|scp)\b|rsync[^|]*--progress|pip3? +install|apt(-get)? +(install|update|upgrade|full-upgrade)|dnf +install|yum +install|docker +(pull|build)|npm +(install|ci)\b|yarn +(install|add)'; then
  noisy=1
fi
# journalctl with NO bound (no -n/--lines/--since/--until/-p, no pipe to head|tail|grep|wc|awk|sed)
if printf '%s' "$cmd" | grep -qiE '\bjournalctl\b'; then
  # bound check is CASE-SENSITIVE on purpose: with -i, journalctl's `-u <unit>` falsely matches `-U`
  # and every `journalctl -u X` would look "bounded". Case-sensitive keeps -u (unit) ≠ -n/-p bounds.
  if ! printf '%s' "$cmd" | grep -qE '(-n[ =]|--lines|--since|--until|-p[ =]|--priority|\| *(head|tail|grep|wc|awk|sed))'; then
    noisy=1
  fi
fi
[ "$noisy" = 0 ] && exit 0   # not a noisy command → never touch it

# ── Only from here on do we consider trimming, and only if the output is genuinely large ──
out=$(printf '%s' "$input" | jq -r '
  (.tool_response.stdout // .tool_response.output //
   (.tool_response | if type=="string" then . else empty end) //
   .tool_output // empty)' 2>/dev/null) || exit 0
[ -z "$out" ] && exit 0

bytes=${#out}
lines=$(printf '%s\n' "$out" | wc -l | tr -d ' ')
if [ "${bytes:-0}" -le 16000 ] && [ "${lines:-0}" -le 250 ]; then
  exit 0   # small noisy output → keep as-is
fi

# Keep head + tail (the final "upload: …/done" line lives in the tail), byte-capped under the ~10 KB
# updatedToolOutput limit.
head_txt=$(printf '%s' "$out" | head -n 60 | head -c 3200)
tail_txt=$(printf '%s' "$out" | tail -n 60 | tail -c 3200)
err=$(printf '%s' "$input" | jq -r '.tool_response.stderr // empty' 2>/dev/null | head -c 1000)

repl=$(printf '%s\n\n…… [шумная команда: %s строк / %s байт свёрнуто хуком; head+tail] ……\n\n%s' \
  "$head_txt" "$lines" "$bytes" "$tail_txt")
[ -n "$err" ] && repl=$(printf '%s\n\n[stderr, обрезан]\n%s' "$repl" "$err")

jq -nc --arg o "$repl" \
  '{hookSpecificOutput:{hookEventName:"PostToolUse",updatedToolOutput:$o}}' 2>/dev/null || exit 0
