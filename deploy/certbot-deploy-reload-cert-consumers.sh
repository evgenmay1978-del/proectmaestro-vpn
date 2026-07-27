#!/bin/sh
# certbot deploy hook — hand the freshly renewed cert to every service that holds a COPY in memory.
# Runs only after a successful renewal ($RENEWED_LINEAGE = /etc/letsencrypt/live/<domain>).
#
# WHY THIS EXISTS (found 2026-07-27)
# ---------------------------------
# certbot renewed wapmixx.ru on 25.07 (valid to 23.10) and nothing broke visibly — but the x-ui panel
# on :2053 kept serving the MAY certificate, due to expire 20.08, because it had been running since
# 26.06 and reads the cert files once at start-up. Renewal that nobody re-reads is not renewal.
#
# ⚠️ THE TRAP — DO NOT "fix" this to `systemctl reload x-ui`:
# x-ui.service ships ExecReload=/bin/kill -USR1 $MAINPID, and the binary's own strings say
#     "Received USR1 signal, restarting xray-core..."
#     "Received SIGHUP signal. Restarting servers..."
# i.e. USR1 restarts XRAY (dropping every live client connection — there were 206 on :443 when this
# was written), while SIGHUP restarts only the panel's web servers. So we send SIGHUP, to the MAIN pid
# only. `systemctl kill` without --kill-whom=main would hit xray too.
# Verified on 2026-07-27: after SIGHUP the panel served the new cert, x-ui pid and xray pid unchanged.
set -u

log() { echo "cert-deploy-hook: $*"; logger -t certbot-deploy "$*" 2>/dev/null || true; }

# 1) nginx — terminates TLS for :8911 (router /sub + panel) and :9443. Config-test first: a reload with a
#    broken config would drop the listener that serves the routers their subscriptions.
if systemctl is-active --quiet nginx; then
  if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx && log "nginx reloaded"
  else
    log "WARNING nginx config test FAILED — NOT reloading (it would take :8911 down)"
  fi
fi

# 2) x-ui panel (:2053) — SIGHUP to the main pid only. See the trap note above.
if systemctl is-active --quiet x-ui; then
  mp=$(systemctl show x-ui -p MainPID --value 2>/dev/null)
  case "$mp" in
    ''|0) log "WARNING x-ui active but MainPID unknown — panel keeps the OLD cert until restarted" ;;
    *)    kill -HUP "$mp" 2>/dev/null && log "x-ui web servers reloaded (SIGHUP to pid $mp, xray untouched)" \
                                      || log "WARNING SIGHUP to x-ui pid $mp failed — panel keeps the OLD cert" ;;
  esac
fi

exit 0
