#!/bin/sh
# Is the RUNNING maestro-panel built from git HEAD?  Closes the "fix committed but never
# deployed" gap that let the 2026-07-02 Hy2 crash-loop recur for ~22h (the sanitizer fix was
# in git the whole time while the pre-fix binary kept re-rendering the poisoned config).
#
# It asks the panel itself — /healthz returns "ok <short-sha>" (api.BuildCommit, injected by
# ops/deploy-panel.sh -ldflags) — so it reflects the ACTUAL binary, not a stamp that can drift.
#
#   exit 0 = deployed == HEAD (clean)
#   exit 1 = DRIFT: HEAD has commits the running binary does not (deploy pending)
#   exit 2 = panel unreachable, or built without the stamp (older binary) → redeploy to enable
set -eu
# Resolve the repository from this script instead of relying on the historical
# /root/maestrovpn-tv checkout.  The production assistant checkout lives in
# /srv/maestrovpn-tv, and a stale hard-coded path turns HEAD into "unknown",
# producing a false deploy-drift alarm.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO=${MAESTRO_REPO:-$(dirname "$SCRIPT_DIR")}
PANEL=${MAESTRO_PANEL:-http://127.0.0.1:8910}

HEAD=$(git -C "$REPO" rev-parse --short HEAD 2>/dev/null || echo unknown)
BODY=$(curl -s --max-time 5 "$PANEL/healthz" || true)

if [ -z "$BODY" ]; then
  echo "⚠️  panel unreachable at $PANEL/healthz — cannot verify deploy freshness"
  exit 2
fi
RUN=$(printf '%s' "$BODY" | awk '{print $2}')   # "ok <sha>"; empty on a pre-stamp binary that returns just "ok"
if [ -z "$RUN" ] || [ "$RUN" = "dev" ] || [ "$RUN" = "unknown" ]; then
  echo "⚠️  running panel has no build-commit (reports '$BODY') — redeploy with ops/deploy-panel.sh to enable drift detection"
  exit 2
fi
# strip a "-dirty" suffix for the ancestry compare (still reported verbatim above)
RUNC=${RUN%-dirty}
if [ "$RUNC" = "$HEAD" ]; then
  echo "✅ panel deployed = HEAD ($RUN)"
  exit 0
fi
if git -C "$REPO" merge-base --is-ancestor "$RUNC" HEAD 2>/dev/null; then
  # Дрейф = только коммиты, задевающие ВХОДЫ бинаря панели (backend/, cmd/, go.mod|sum).
  # app/ops/deploy-коммиты панель не меняют — раньше они давали ЛОЖНЫЙ drift-алярм
  # каждую ориентацию (2026-07-11), и настоящий дрейф рисковал утонуть в шуме.
  TOUCH=$(git -C "$REPO" log --oneline "${RUNC}..HEAD" -- backend/ cmd/ go.mod go.sum 2>/dev/null || true)
  if [ -z "$TOUCH" ]; then
    echo "✅ panel deployed = HEAD по backend-путям (running=$RUN, HEAD=$HEAD — сверху только app/ops-коммиты)"
    exit 0
  fi
  echo "⚠️  DEPLOY DRIFT — running=$RUN  HEAD=$HEAD"
  echo "   недеплоенные BACKEND-коммиты (в HEAD, но не в работающем бинаре):"
  printf '%s\n' "$TOUCH" | sed 's/^/     /'
else
  echo "⚠️  DEPLOY DRIFT — running=$RUN  HEAD=$HEAD"
  echo "   (running commit $RUNC is not an ancestor of HEAD — diverged/rebased; redeploy to reconcile)"
fi
exit 1
