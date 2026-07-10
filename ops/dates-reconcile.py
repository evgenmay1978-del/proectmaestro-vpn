#!/usr/bin/env python3
"""Unified-expiry reconciler — the "single organism" enforcer (runs on S1).

Pulls subscription expiry dates from EVERY store where the owner can renew a client:
  panel  — /var/lib/maestro/customers.json (the app's 402 gate; source the app obeys)
  s1xui  — 3x-ui on S1 (VLESS inbounds 2,4), where the owner edits dates by hand
  s3xui  — 3x-ui on S3 (VLESS-Reality inbound 1)
  s2bot  — the S2 telegram bot's sqlite (payments confirmed there extend ONLY its
           local naive panel — the designed /admin/set-expiry call was never wired)

Rule: per panel client, target = max(dates seen anywhere, matched case-insensitively).
If any store lags the target by > TOL, POST /admin/set-expiry (absolute mirror) — the
panel raises its date and fans out to S1/S3 x-ui (re-enabling), Hy2, AnyTLS, mtv-naive.
The date can only ever be RAISED to what some system already granted — a client can
gain time, never lose it, so a bad source can't cut anyone off. Clients expired in
every store are left expired. Logins unknown to the panel are reported, not touched.

Usage: dates-reconcile.py [--apply]   (report-only without --apply)
"""
import json
import re
import sqlite3
import subprocess
import sys
import urllib.request
from datetime import datetime, timedelta, timezone

APPLY = "--apply" in sys.argv
STORE = "/var/lib/maestro/customers.json"
ENV = "/etc/maestro-panel.env"
PANEL = "http://127.0.0.1:8910"
S3 = "root@46.30.42.151"
S2 = "root@85.137.166.237"
SSH = ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new"]
TOL = timedelta(hours=25)  # x-ui UI rounds to local midnight; ignore sub-day skew
NOW = datetime.now(timezone.utc)


def parse_dt(s):
    """RFC3339-ish, with/without Z/fraction/tz; naive means UTC."""
    s = s.strip()
    s = re.sub(r"\.(\d{6})\d+", r".\1", s)  # >6 fractional digits break fromisoformat
    try:
        d = datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        return None
    return d if d.tzinfo else d.replace(tzinfo=timezone.utc)


def load_panel():
    return {c["login"]: parse_dt(c["expires"]) for c in json.load(open(STORE))}


def xui_clients(settings_json):
    out = {}
    for cl in json.loads(settings_json).get("clients", []):
        et = cl.get("expiryTime", 0)
        if isinstance(et, (int, float)) and et > 0:  # 0/negative = unlimited/special: not a date claim
            out[cl["email"]] = datetime.fromtimestamp(et / 1000, timezone.utc)
    return out


def load_s1xui():
    out = {}
    db = sqlite3.connect("file:/etc/x-ui/x-ui.db?mode=ro", uri=True)
    for (settings,) in db.execute("SELECT settings FROM inbounds WHERE id IN (2,4)"):
        out.update(xui_clients(settings))
    return out


def run(cmd):
    return subprocess.run(cmd, capture_output=True, text=True, timeout=30).stdout


def load_s3xui():
    raw = run(SSH + [S3, 'sqlite3 "file:/etc/x-ui/x-ui.db?mode=ro" "SELECT settings FROM inbounds WHERE id=1;"'])
    return xui_clients(raw) if raw.strip() else {}


def load_s2bot():
    raw = run(SSH + [S2, 'sqlite3 "file:/opt/vpn_bot/bot_minimal.db?mode=ro" "SELECT proxy_user, expires_at FROM subscriptions;"'])
    out = {}
    for line in raw.splitlines():
        user, _, exp = line.partition("|")
        d = parse_dt(exp) if exp else None
        if user and d:
            out[user] = d
    return out


def load_naive():
    """Owner-added users in the S2 naive panel (raw usernames; mtv_* are panel-owned mirrors)."""
    snippet = (
        "set -a; . /opt/vpn_bot/.env; set +a; C=$(mktemp); "
        'curl -sk -m 15 -c "$C" -X POST "$PANEL_URL/api/login" -H "Content-Type: application/json" '
        '-d "{\\"username\\":\\"$PANEL_USER\\",\\"password\\":\\"$PANEL_PASS\\"}" >/dev/null; '
        'curl -sk -m 15 -b "$C" "$PANEL_URL/api/naive/users"; rm -f "$C"'
    )
    raw = run(SSH + [S2, snippet])
    out = {}
    for u in json.loads(raw).get("users", []):
        name = u.get("username", "")
        d = parse_dt(str(u.get("expiresAt") or ""))
        if name and not name.startswith("mtv_") and d:
            out[name] = d
    return out


def set_expiry(token, login, target):
    body = json.dumps({"login": login, "expires": target.strftime("%Y-%m-%dT%H:%M:%SZ")}).encode()
    req = urllib.request.Request(PANEL + "/admin/set-expiry", data=body, method="POST",
                                 headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.status == 200


def raise_s2bot(proxy_user, target):
    """Raise (never lower) the S2 bot's own ledger; its 6h sync job then restores the
    client's raw naive from that date — we never touch the bot's naive panel directly."""
    iso = target.strftime("%Y-%m-%dT%H:%M:%SZ")
    safe = proxy_user.replace("'", "")
    q = ("UPDATE subscriptions SET expires_at='{0}' "
         "WHERE proxy_user='{1}' COLLATE NOCASE AND expires_at < '{0}';").format(iso, safe)
    run(SSH + [S2, "sqlite3 /opt/vpn_bot/bot_minimal.db \"{}\"".format(q)])
    check = run(SSH + [S2, "sqlite3 /opt/vpn_bot/bot_minimal.db \"SELECT expires_at FROM subscriptions WHERE proxy_user='{}' COLLATE NOCASE;\"".format(safe)])
    return check.strip().startswith(iso[:10])


IMPORT_SKIP = {"awgtest", "moy2", "test", "diag", "audit", "admin"}


def importable(login):
    return (len(login) >= 3 and re.fullmatch(r"[A-Za-z0-9._-]+", login)
            and login.lower() not in IMPORT_SKIP
            and not login.lower().startswith(("test", "diag", "tmp", "trial-")))


def main():
    panel = load_panel()
    sources = {}
    for name, loader in (("s1xui", load_s1xui), ("s3xui", load_s3xui), ("s2bot", load_s2bot), ("naive", load_naive)):
        try:
            sources[name] = loader()
        except Exception as e:  # a dead source must not block reconciling the rest
            print(f"WARN: source {name} unavailable: {e}", file=sys.stderr)
            sources[name] = {}

    by_lower = {name: {k.lower(): v for k, v in vals.items()} for name, vals in sources.items()}
    token = ""
    for line in open(ENV):
        if line.startswith("MAESTRO_ADMIN_TOKEN="):
            token = line.split("=", 1)[1].strip()

    drift, expired, failures = 0, 0, 0
    fmt = "%Y-%m-%d %H:%M"
    for login, pexp in sorted(panel.items()):
        vals = {"panel": pexp}
        for name in sources:
            v = by_lower[name].get(login.lower())
            if v:
                vals[name] = v
        target = max(vals.values())
        if target <= NOW:
            expired += 1
            continue
        lag = [n for n, v in vals.items() if target - v > TOL]
        if not lag:
            continue
        if set(lag) == {"naive"}:  # naive is bot/panel-capped (max 3650d) and not ours to write —
            continue               # a naive-only lag (e.g. 2036 vs unlimited-2099) is not drift
        drift += 1
        detail = " ".join(f"{n}={v.strftime(fmt)}" for n, v in vals.items())
        print(f"DRIFT {login}: target={target.strftime(fmt)} lagging={','.join(lag)} [{detail}]")
        if not APPLY:
            continue
        if any(n not in ("s2bot", "naive") for n in lag):  # panel/x-ui lag → panel mirror + fan-out
            try:
                ok = set_expiry(token, login, target)
            except Exception as e:
                ok = False
                print(f"  APPLY FAILED {login}: {e}", file=sys.stderr)
            if ok:
                print(f"  APPLIED {login} -> {target.strftime(fmt)}")
            else:
                failures += 1
        if "s2bot" in lag:  # bot ledger lags → raise it; its own sync restores naive
            try:
                ok = raise_s2bot(login, target)
            except Exception as e:
                ok = False
                print(f"  S2BOT RAISE FAILED {login}: {e}", file=sys.stderr)
            print(f"  S2BOT {'RAISED' if ok else 'RAISE FAILED'} {login} -> {target.strftime(fmt)}")
            if not ok:
                failures += 1

    # Clients created OUTSIDE the panel (owner adds someone in x-ui / the naive panel / the
    # bot sells one): import them — /admin/set-expiry backfills via ActivateExisting, which
    # pulls existing creds and mints the missing protocols. Skip test artifacts and the dead.
    known = {l.lower() for l in panel}
    strays = {}  # lower → (original_name, max_date, [sources])
    for name, vals in sources.items():
        for k, v in vals.items():
            if k.lower() in known:
                continue
            cur = strays.get(k.lower())
            strays[k.lower()] = (cur[0] if cur else k, max(v, cur[1]) if cur else v,
                                 (cur[2] if cur else []) + [name])
    imported = 0
    for orig, target, srcs in sorted(strays.values()):
        if target <= NOW or not importable(orig):
            print(f"INFO: stray {orig} ({','.join(srcs)}, exp {target.strftime(fmt)}) — skipped")
            continue
        print(f"IMPORT {orig}: found in {','.join(srcs)}, exp {target.strftime(fmt)} -> panel")
        if APPLY:
            try:
                if set_expiry(token, orig, target):
                    imported += 1
                    print(f"  IMPORTED {orig}")
                else:
                    failures += 1
            except Exception as e:
                failures += 1
                print(f"  IMPORT FAILED {orig}: {e}", file=sys.stderr)

    print(f"SUMMARY: {len(panel)} panel clients, {drift} drifted, {imported} imported"
          f"{' (applied)' if APPLY else ' (report-only)'}, {expired} expired everywhere, {failures} apply-failures")
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
