#!/usr/bin/env bash
# MaestroVPN control-plane backup → gpg-encrypted → Yandex Object Storage (PRIVATE bucket).
#
# Tars the irreplaceable S1 control-plane state, encrypts it to the backup gpg PUBLIC key
# (this box holds NO secret key — only Server 2 + the owner's offline copy can decrypt), then
# uploads to the private maestro-backups bucket and prunes old objects. So even though the
# blobs live in the cloud, they are useless ciphertext without the off-S1 private key.
#
# Recovery: see docs/runbook-s1-recovery.md (decrypt with the Server-2 private key).
set -euo pipefail
case "${MAESTRO_CONTROL_PLANE_MODE:-}" in
  rqlite)
    printf '%s\n' 'maestro-backup: disabled in rqlite mode'
    exit 0
    ;;
  legacy)
    ;;
  *)
    printf '%s\n' 'maestro-backup: invalid control-plane mode' >&2
    exit 64
    ;;
esac

AWS=/root/.local/bin/aws
EP=(--endpoint-url=https://storage.yandexcloud.net)
PROFILE=yc
BUCKET=maestro-backups
RECIP=backup@maestrovpn.local
KEEP=72                       # newest N objects kept per host (hourly ⇒ ~3 days)

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
TS=$(date -u +%Y%m%dT%H%M%SZ)
HOST=$(hostname)
tar="$WORK/maestro-cp-$TS.tar.gz"

# 1) Collect control-plane state (skip a missing file rather than fail the whole run).
#
# Keep this list in step with cmd/maestro-panel/main.go: every /var/lib/maestro file the panel
# OPENS as state belongs here. It drifted once already — trials.json, panel-pw.hash, wb.token
# and olcrtc.json were added to the panel long after this script was written and were silently
# missing from every backup, so an S1 rebuild would have restored customers but:
#   trials.json    — the free-trial anti-abuse ledger; losing it lets every device claim a
#                    fresh trial (straight revenue loss), and it cannot be reconstructed
#   panel-pw.hash  — bcrypt hash guarding the web admin panel
#   wb.token       — wbstream account token for the olcRTC carrier
#   olcrtc.json    — olcRTC carrier room/key
# olcrtc-health.json is deliberately NOT here: the exit-liveness probe rewrites it by itself.
# 1a) Pull the S3 node's 3x-ui database (added 2026-07-27 after an audit found it backed up NOWHERE).
#
# /etc/x-ui/x-ui.db in the list below is THIS box's (S1) panel db. S3 (46.30.42.151) runs its own 3x-ui
# holding ~89 paying clients of the VLESS-Reality inbound, and nothing was copying it anywhere: no cron on
# S3, no entry here. One corrupt file = the client base is gone.
#
# Two traps this code exists to avoid:
#  * NEVER `scp` a live SQLite file — a copy taken mid-write is torn and may not open. `.backup` takes a
#    consistent hot snapshot through SQLite itself, which is safe while the panel keeps writing traffic stats.
#  * NEVER let an unreachable S3 kill the whole run — S1's own state must keep being backed up regardless.
#    So the pull is best-effort, BUT it must not fail silently: on failure we shout to stderr (journal) and
#    report the age of the stale snapshot we're falling back on.
S3_HOST="${S3_HOST:-46.30.42.151}"
S3_SNAP=/var/lib/maestro/s3-x-ui.db          # stable path ⇒ lands in the archive next to the other state
_ssh=(ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
      -o LogLevel=ERROR -o ConnectTimeout=15)
if "${_ssh[@]}" "root@$S3_HOST" \
     'rm -f /tmp/.x-ui-hot.db && sqlite3 /etc/x-ui/x-ui.db ".backup /tmp/.x-ui-hot.db" && cat /tmp/.x-ui-hot.db && rm -f /tmp/.x-ui-hot.db' \
     > "$WORK/s3-x-ui.db" 2>/dev/null && [ -s "$WORK/s3-x-ui.db" ] \
   && sqlite3 "$WORK/s3-x-ui.db" 'pragma quick_check;' 2>/dev/null | grep -q '^ok$'; then
  install -m 600 "$WORK/s3-x-ui.db" "$S3_SNAP"
  echo "maestro-backup: S3 panel db snapshot OK ($(stat -c%s "$S3_SNAP") bytes, $(sqlite3 "$S3_SNAP" 'select count(*) from client_traffics;' 2>/dev/null) clients)"
else
  if [ -f "$S3_SNAP" ]; then
    echo "maestro-backup: WARNING S3 panel db pull FAILED — falling back on the stale snapshot from $(date -r "$S3_SNAP" -u +%Y-%m-%dT%H:%M:%SZ)" >&2
  else
    echo "maestro-backup: WARNING S3 panel db pull FAILED and there is NO previous snapshot — the S3 client base is UNPROTECTED this run" >&2
  fi
fi

files=()
for f in /var/lib/maestro/customers.json /var/lib/maestro/orders.json \
         /var/lib/maestro/trials.json /var/lib/maestro/panel-pw.hash \
         /var/lib/maestro/wb.token /var/lib/maestro/olcrtc.json \
         /etc/maestro-panel.env /etc/x-ui/x-ui.db "$S3_SNAP"; do
  [ -f "$f" ] && files+=("$f")
done
if [ ${#files[@]} -eq 0 ]; then
  echo "maestro-backup: no state files found, nothing to do" >&2
  exit 0
fi
tar -czf "$tar" "${files[@]}"

# 2) Encrypt to the backup public key (our own freshly-made key ⇒ trust-model always).
enc="$tar.gpg"
gpg --batch --yes --trust-model always --encrypt --recipient "$RECIP" --output "$enc" "$tar"

# 3) Upload to the private bucket.
key="cp/$HOST/maestro-cp-$TS.tar.gz.gpg"
"$AWS" --profile "$PROFILE" "${EP[@]}" s3 cp "$enc" "s3://$BUCKET/$key" >/dev/null
echo "maestro-backup: uploaded s3://$BUCKET/$key ($(stat -c%s "$enc") bytes, $(printf '%s' "${files[*]}" | wc -w) files)"

# 4) Prune: keep only the newest $KEEP objects under cp/$HOST/.
mapfile -t old < <("$AWS" --profile "$PROFILE" "${EP[@]}" s3 ls "s3://$BUCKET/cp/$HOST/" \
                    | sort | head -n -"$KEEP" | awk '{print $4}')
for o in "${old[@]:-}"; do
  [ -n "$o" ] || continue
  "$AWS" --profile "$PROFILE" "${EP[@]}" s3 rm "s3://$BUCKET/cp/$HOST/$o" >/dev/null && echo "maestro-backup: pruned $o"
done
