#!/bin/sh
# sync-anytls-cert — give sing-box-anytls the certificate caddy has already renewed.
#
# WHY THIS EXISTS (audit 2026-07-27)
# ----------------------------------
# /etc/sing-box-anytls/cert.pem was a MANUAL copy of caddy's certificate for wapmix.duckdns.org
# (fingerprints matched byte for byte, file dated 21.06). caddy renews its own copy reliably
# (10 renewal events in 30 days), but nothing ever carried the new file across and nothing
# restarted sing-box — acme.sh's Le_PreHook/Le_PostHook/Le_RenewHook are all empty and there was
# no timer. Left alone, AnyTLS clients would have hit an expired certificate on 18.09.2026.
#
# Design rules (do not "simplify" these away):
#  * RESTART ONLY ON REAL CHANGE. Comparing fingerprints, not dates, keeps this a no-op on the ~364
#    days a year the cert did not move — each restart drops live AnyTLS connections.
#  * NEVER install an unusable file. The candidate must parse as x509 AND still be valid, and the key
#    must match the cert; otherwise the service would come back up unable to serve TLS at all.
#  * PICK THE NEWEST cert across CA directories. caddy's storage holds both a stale ZeroSSL copy
#    (expired 20.07) and the live Let's Encrypt one — taking "the first match" would install the dead one.
#  * Everything is overridable by env so the logic can be exercised without touching the live service.
set -u

DOMAIN="${DOMAIN:-wapmix.duckdns.org}"
CADDY_ROOTS="${CADDY_ROOTS:-/var/lib/caddy/.local/share/caddy/certificates /root/.local/share/caddy/certificates}"
DST_DIR="${DST_DIR:-/etc/sing-box-anytls}"
SERVICE="${SERVICE:-sing-box-anytls}"
RELOAD_CMD="${RELOAD_CMD:-systemctl restart $SERVICE}"

log() { echo "sync-anytls-cert: $*"; logger -t sync-anytls-cert "$*" 2>/dev/null || true; }
fp() { openssl x509 -in "$1" -noout -fingerprint -sha256 2>/dev/null | cut -d= -f2; }
enddate() { openssl x509 -in "$1" -noout -enddate 2>/dev/null | cut -d= -f2; }

# --- 1) find the newest still-valid cert caddy holds for $DOMAIN ---
best=""; best_end=0
for root in $CADDY_ROOTS; do
  [ -d "$root" ] || continue
  for c in $(find "$root" -type f -name "$DOMAIN.crt" 2>/dev/null); do
    openssl x509 -in "$c" -noout >/dev/null 2>&1 || continue
    end_epoch=$(date -d "$(enddate "$c")" +%s 2>/dev/null) || continue
    [ "$end_epoch" -gt "$best_end" ] && { best="$c"; best_end="$end_epoch"; }
  done
done
[ -n "$best" ] || { log "ERROR no certificate for $DOMAIN found under: $CADDY_ROOTS"; exit 1; }
key="${best%.crt}.key"
[ -f "$key" ] || { log "ERROR key missing next to $best"; exit 1; }

# --- 2) sanity: not expired, and the key really belongs to this cert ---
now=$(date +%s)
[ "$best_end" -gt "$now" ] || { log "ERROR newest candidate $best is ALREADY EXPIRED ($(enddate "$best")) — refusing"; exit 1; }
c_pub=$(openssl x509 -in "$best" -noout -pubkey 2>/dev/null | openssl md5)
k_pub=$(openssl pkey -in "$key" -pubout 2>/dev/null | openssl md5)
[ -n "$c_pub" ] && [ "$c_pub" = "$k_pub" ] || { log "ERROR key does not match cert ($best) — refusing to install"; exit 1; }

# --- 3) no-op when nothing moved ---
if [ -f "$DST_DIR/cert.pem" ] && [ "$(fp "$best")" = "$(fp "$DST_DIR/cert.pem")" ]; then
  log "cert unchanged (valid until $(enddate "$best")) — nothing to do"
  exit 0
fi

# --- 4) install atomically, then reload ---
log "NEW cert found: $best (valid until $(enddate "$best")); installed copy was $( [ -f "$DST_DIR/cert.pem" ] && enddate "$DST_DIR/cert.pem" || echo 'absent')"
install -m 644 "$best" "$DST_DIR/.cert.pem.new" && install -m 600 "$key" "$DST_DIR/.key.pem.new" || {
  log "ERROR could not stage new files in $DST_DIR"; rm -f "$DST_DIR/.cert.pem.new" "$DST_DIR/.key.pem.new"; exit 1; }
[ -f "$DST_DIR/cert.pem" ] && cp -a "$DST_DIR/cert.pem" "$DST_DIR/cert.pem.prev" 2>/dev/null
[ -f "$DST_DIR/key.pem" ] && cp -a "$DST_DIR/key.pem" "$DST_DIR/key.pem.prev" 2>/dev/null
mv "$DST_DIR/.cert.pem.new" "$DST_DIR/cert.pem" && mv "$DST_DIR/.key.pem.new" "$DST_DIR/key.pem"

if $RELOAD_CMD; then
  sleep 2
  if [ "$SERVICE" = "-" ] || systemctl is-active --quiet "$SERVICE"; then
    log "installed and reloaded $SERVICE — now serving cert valid until $(enddate "$DST_DIR/cert.pem")"
  else
    log "ERROR $SERVICE did NOT come back after reload — rolling back to previous cert"
    [ -f "$DST_DIR/cert.pem.prev" ] && mv "$DST_DIR/cert.pem.prev" "$DST_DIR/cert.pem"
    [ -f "$DST_DIR/key.pem.prev" ] && mv "$DST_DIR/key.pem.prev" "$DST_DIR/key.pem"
    $RELOAD_CMD || true
    exit 1
  fi
else
  log "ERROR reload command failed: $RELOAD_CMD"
  exit 1
fi
exit 0
