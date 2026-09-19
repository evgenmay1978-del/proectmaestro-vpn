# Ordinary x-ui runtime guard

Scope: ordinary local VLESS clients on S1/S3/S4. No CDN, S2, DB writes,
credentials, billing, quota changes, or client creation/deletion.

The effective list follows `GetXrayConfig` in exact 3x-ui 3.2.8 and 3.4.0:
enabled local inbounds; clients linked through `client_inbounds`; client enabled;
if traffic stats exist for the exact email, stats enabled. Own inbound stats
take precedence; missing stats are backfilled by emails in `settings.clients`
across all inbounds, with native lowercase deduplication and exact final keys.
Ambiguous sibling stats safely skip repair. Expiry/traffic jobs maintain the
stats flag. `flow_override` replaces client flow even when empty; the `-udp443`
flow suffix is removed. UUIDs use the canonical form returned by Xray's user
API. The ordinary inbound IDs are explicit.

`bin/config.json` is not live truth. Native AddUser/RemoveUser and v3.4 hot-apply
update the running core without rewriting this file. The guard uses the local
read-only `xray api inbounduser` call to compare UUID/email/flow in memory. The
file is used only for the API listener and unchanged inbound definition.

- [3.2.8 renderer](https://github.com/MHSanaei/3x-ui/blob/v3.2.8/web/service/xray.go)
- [3.4.0 renderer/hot-apply](https://github.com/MHSanaei/3x-ui/blob/v3.4.0/internal/web/service/xray.go)
- [3.4.0 in-memory SetConfig](https://github.com/MHSanaei/3x-ui/blob/v3.4.0/internal/xray/process.go)
- [3.4.0 shared traffic stats](https://github.com/MHSanaei/3x-ui/blob/v3.4.0/internal/web/service/inbound_clients.go)
- [Xray 26.6.1 read-only user API](https://github.com/XTLS/Xray-core/blob/v26.6.1/main/commands/all/api/inbound_user.go)
- [Xray 26.6.22 read-only user API](https://github.com/XTLS/Xray-core/blob/v26.6.22/main/commands/all/api/inbound_user.go)

The observed panel/core binary hashes are pinned. Unknown builds, schema,
scope, unavailable API, ambiguous data or changed processes safely skip repair.
No CLI output or client identifier enters logs or persistent state. State holds
only aggregate counts/times and digests. The rotation flock and a guard flock
are held throughout observation and any repair; lock files are never deleted.

Two identical observations at least 120 seconds apart plus one immediate reread
are required for a restart. The timer runs every five minutes. A one-hour
cooldown and a persisted pre-restart attempt latch allow only one repair per
expected client generation, even if restart fails or is killed. An unresolved
generation requires operator investigation; do not clear its latch blindly.

## Installed 2026-09-19, 17:08–17:10 UTC

Installed after source review on S1/S3/S4. Each timer is active/enabled; each
first scheduled oneshot returned `consistent`, missing=0, extra=0. Ordinary
panel/core PIDs were unchanged across installation:

| Host | x-ui PID | Xray PID | Private backup directory under `/var/backups/` |
| --- | --- | --- | --- |
| S1 | 29645 | 29654 | `maestro-xui-guard-20260919T170853Z` |
| S3 | 1333251 | 1333259 | `maestro-xui-guard-20260919T170928Z` |
| S4 | 38313 | 38328 | `maestro-xui-guard-20260919T171023Z` |

Installed Python SHA-256 on all three:
`1bbb5715a3dda8022feac811b17f67be1ada093fa9379c6d6e81b96beef9bad0`.
No x-ui/core restart, traffic probe, test, purchase, or DB mutation was performed.
This confirms deployment and current DB/runtime agreement; the repair branch
has not been exercised against an artificial production mismatch.

## Installation procedure

Install the Python script as `/usr/local/lib/maestro-xui-runtime-guard.py` (0755),
service/timer as `/etc/systemd/system/maestro-xui-runtime-guard.{service,timer}`
(0644). Set `/etc/maestro-xui-guard.env` (0600) with only one line:

| Host | Environment line | Read-only baseline, 2026-09-19 |
| --- | --- | --- |
| S1 | `INBOUND_IDS=2,4` | expected/live 40+2, zero missing/extra |
| S3 | `INBOUND_IDS=1` | expected/live 41, zero missing/extra |
| S4 | `INBOUND_IDS=1,2` | expected/live 42+42, zero missing/extra |

Run the installed script once with the host's `--inbound-ids` and without
`--repair` to observe the live result. Then daemon-reload and enable/start only
the timer. Installing/enabling the timer does not restart x-ui. Keep the first
timer result and live PIDs as deployment evidence; this is not a traffic test.

Rollback: disable/stop `maestro-xui-runtime-guard.timer`. Do not remove the state
or shared rotation lock. If the oneshot is running, observe its result before
maintenance. No x-ui DB/config rollback is needed because the guard never
modifies either file. A panel/core upgrade safely pauses this guard until its
new renderer/API contract and binary hashes are reviewed.
