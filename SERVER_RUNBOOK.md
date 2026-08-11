# MaestroVPN production runbook

Проверено: **2026-08-11**. Это redacted runbook: пароли, токены, private panel
suffixes, `.env`, customer identifiers и полные subscription URL в Git не
хранятся. Volatile-факты перед каждой операцией перепроверяются read-only.

## Fleet snapshot

| Узел | Адрес | Подтверждённое состояние |
|---|---|---|
| S1 | `193.17.183.48` | 3x-ui (`x-ui.service`), `maestro-panel.service`, `vpn_bot.service`, nginx и Hysteria active/enabled; Maestro store 77 клиентов |
| S2 | `85.137.166.237` | `vpn_bot.service`, caddy и Hysteria active; bot SQLite `quick_check` PASS, pending updates 0 |
| S3 | `46.30.42.151` | saved 3x-ui management API с S1: HTTP 200, один inbound |
| S4 | `89.125.19.95` | `x-ui.service` active; SQLite `quick_check` PASS, один inbound, 77 client traffic records |

S1 Maestro backend live commit:
`71b7f0c59e0c8fa6be36d1bc78f66b4753324a26`.
S1 bot live commit: `c52855556f23d078e0a16ac5454a2a35aa78185d`.
S2 bot live commit: `aa672d7`.

S1 public host is `wapmixx.ru`. Exact Maestro/3x-ui private URL suffixes are
not committed; derive them read-only from live nginx/3x-ui settings. S4 admin
port `16173` is intentionally reachable only from S1 management IP, not from a
phone/public Internet. Do not open it globally without explicit approval.

Recovery checkpoints created during the incident:

- S1 bot: `/root/maestro-recovery-20260811T054859Z/s1-bot-deploy-6c6bc6c`;
- S2 bot: `/root/maestro-s2-recovery-20260811T1345Z`;
- S4 firewall: `/root/maestro-s4-recovery-20260811-new-s1-ufw`.

## Mandatory preflight

Before every server/network/production mutation or retry:

1. Read `AGENTS.md`, `CONTEXT_HANDOFF.md`, this file and the project-master skill.
2. Run `python ops/maestro-repetition-guard.py check --action <semantic-id>
   --family <method-family>`. Never place commands, URLs, users or secrets in
   guard arguments.
3. Record local branch/HEAD/status and exact remote target. Use pinned SSH host
   keys; never disable host-key or TLS verification.
4. Discover the service rather than guessing it:

   ```bash
   systemctl show <unit> -p Id -p FragmentPath -p ExecStart -p WorkingDirectory -p EnvironmentFiles
   systemctl is-active <unit>
   systemctl is-enabled <unit>
   ```

5. Inspect only required env key names/paths. Never dump or blindly source
   `.env`; values can contain spaces and secrets.
6. Resolve the live nginx symlink/config and bound ports; do not assume a
   filename from an old server.
7. Resolve data path and format from actual config (`MAESTRO_STORE`, `DB_PATH`,
   unit environment) before deciding whether the state is JSON or SQLite.
8. Prove runtime capabilities by the real disposable action. For example,
   create a staging venv; `python -m venv --help` does not prove `ensurepip`
   exists.

Product name is **3x-ui**. Its installed unit is `x-ui.service`. Never replace
it with or install S-ui.

On Windows/PowerShell run native verification commands separately and check
`$LASTEXITCODE`; a later successful command must not mask an earlier failure.

## Backup and deploy contract

- SQLite in WAL mode is backed up with the SQLite backup API, not by copying
  only the main database file. Verify the backup with `PRAGMA quick_check` and
  row/count invariants before mutation.
- JSON/state directories are copied to a timestamped recovery directory with
  ownership and mode recorded. Secrets/state are overlaid separately from
  code and remain mode 600 where applicable.
- Build/deploy a code-only manifest/archive: exclude `.env`, databases, WAL/SHM,
  logs, virtualenvs and historical backups. Never overwrite live state with a
  repository copy.
- Stage the exact artifact, run compile/import/unit checks there, record its
  hash/commit, then make the smallest atomic switch.
- After an approved restart verify: active + enabled, exactly one expected
  process, `quick_check`/counts, local health/API, bound ports, recent logs and
  external read-only health. Compare expiries before/after; no accidental
  shortening or extension is acceptable.
- On failure record guard `fail`, establish the root cause, record `correct`
  with a materially different method family, and only then retry. On success
  record `success` with non-sensitive evidence.

## Telegram bot contract

- S1 and S2 use different bot tokens. Never run two long-pollers for one token.
- Before/after deployment check `getMe`, `getWebhookInfo`, `getUpdates` pending
  count and process count. Preserve queued updates; do not delete a webhook or
  discard pending updates merely to make startup green.
- Enforce **one poller** per token and a durable single-flight/idempotency key
  for payment confirmation, renewal and customer creation. Repeated «Я
  оплатил» or admin confirmation must not create duplicate days/orders.
- Test payment behavior with unit/static/read-only evidence. Do not create or
  confirm a real production payment solely as a smoke test.
- Subscription and QR must use the canonical Maestro URL without client-app
  suffixes such as `app=karing`; TLS verification stays enabled.

S1 bot repository/branch:
`evgenmay1978-del/maestro-vpn-bot`, `codex/s1-restore-maestro-links`.
S2 bot repository/branch:
`evgenmay1978-del/maestro-s2-vpn-bot`,
`codex/s2-canonical-subscription`.

The S2 repository history contains an invalid Windows trailing-space filename;
ordinary Windows clone can fail. Use the preserved checkout or clean that
history only as a separately approved migration. Never stage its unrelated
tracked `.env`/backup files.

## Protocol gates

- olcRTC is restricted to exactly three owner-authorized aliases held in the
  production environment. Do not commit those identifiers.
- WDTT must use the same three-alias restriction and ordinary customers must
  never receive it. S1 WDTT is currently `enabled=false`; do not enable it
  before the 1.0.153/1.0.154 payload/runtime investigation and end-to-end gate.
- S3/S4 ordinary selector health must remain green when special protocols are
  changed.

## Firewall rule contract

Cross-server admin ports accept only the current S1 management IP. After an S1
replacement: add the new exact source rule, verify S1 → target API, remove only
the exact obsolete source rule, then reverify. Never rebuild or broadly flush
the firewall for this change.

## Current resilience limit

The restored production is **not high availability**. S1 is still the Maestro
control-plane single point of failure. A 3-node `rqlite` design on S2/S3/S4,
quorum/failover tests, replicated idempotency ledger, durable per-server outbox
and bot leader/failover are backlog work, not live facts. Never claim HA until
a node-loss test, reconciliation and duplicate audit have passed.

## Security debt

Historical Git commits in both bot repositories contain previously tracked
secret/state files. Active source no longer has token/password defaults, but
history cleanup and coordinated credential rotation remain required. Do not
rotate credentials or rewrite history implicitly; prepare a separate rollback-
aware operation.
