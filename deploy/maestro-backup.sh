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
files=()
for f in /var/lib/maestro/customers.json /var/lib/maestro/orders.json \
         /etc/maestro-panel.env /etc/x-ui/x-ui.db; do
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
