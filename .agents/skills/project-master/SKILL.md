---
name: project-master
description: Use when working on any MaestroVPN Android, backend, API, OTA, WDTT, Telegram bot, server, recovery, deployment, Git, architecture, incident, audit, or project-handoff task, especially before production or network operations and after a failed attempt.
---

# Project Master

## Purpose

Continue the existing MaestroVPN project from verified evidence without losing context, damaging unrelated work, or silently diverging Android, API, backend, infrastructure, and documentation.

Treat this as a VPN product with security-, availability-, privacy-, and production-sensitive paths. Prefer the smallest complete change that preserves current architecture and rollback options.

## When to Use

Use this skill for MaestroVPN tasks involving one or more of:

- Android TV or Android phone/tablet behavior;
- VPN profile activation, refresh, storage, selector, `VpnService`, libbox, OTA, or release;
- public/admin API contracts or Go backend handlers, services, stores, and provisioning;
- WDTT investigation or implementation;
- server diagnostics, deployment, rollback, incident analysis, or environment drift;
- Git branches, unfinished commits, project handoff, ADR, backlog, or documentation refresh.

Do not use old chat text as the source of truth. Treat it only as a lead and revalidate it against the current handoff, source, Git, tests, and approved read-only server evidence.

## Project Context

Start by locating and reading `CONTEXT_HANDOFF.md` completely. Search the workspace if it is not in the current directory. Then read `AGENTS.md` files applicable to the selected worktree and the task-specific context documents named by the handoff.

Use this verified default model only for orientation; recheck it on every task:

- MaestroVPN is a VPN product with one universal Android APK serving TV and phone/tablet form factors.
- The client uses sing-box/libbox and an Android `VpnService`; TV/mobile behavior is gated at runtime.
- The Go backend exposes subscription, activation, trial, purchase, update, report, admin, and panel capabilities and may orchestrate external provisioning systems.
- Backend persistence may include root-owned file stores rather than a conventional SQL database; verify the current implementation before assuming transactions or migrations.
- WDTT means WireGuard over TURN Tunnel in the verified project context. Treat its status, branch, gates, upstream pin, test state, and rollout constraints as volatile data from `WDTT_STATUS.md`.
- Production state is not implied by source code, a branch, a green build, or an existing handler. Verify deployment, flags, service state, and health independently.

Separate every conclusion into:

- confirmed fact with a source;
- evidence-based inference;
- unverified hypothesis;
- open question.

For material claims, cite a file and symbol, commit/branch, API route, service/config name, test, or redacted log evidence.

## Relevant Repositories

Derive the authoritative repository list and current paths from `CONTEXT_HANDOFF.md`. Expect these logical sources, but do not assume their paths, cleanliness, branches, or deployment status:

| Logical source | Responsibility |
|---|---|
| `maestrovpn-tv` mainline | Universal Android client, embedded backend source, operations, OTA, and shared documentation |
| WDTT feature worktree | Unmerged/unfinished Android, backend, packaging, and runbook work for WDTT |
| WDTT upstream checkout | Pinned upstream WireGuard-over-TURN source |
| Router repository | Separate OpenWrt/router client and release lifecycle |
| Optional transport sources | Child transport sources such as olcRTC |
| Operational S2/S3 sources | Provisioning/bot/panel customizations; may be dirty and may contain secrets |

Before choosing a worktree:

1. Map each requested component to its owning repository.
2. Read the nearest applicable `AGENTS.md`.
3. Run `git status --short --branch` and record branch, HEAD, remotes, and local changes.
4. Compare the branch/commit with the latest values in `CONTEXT_HANDOFF.md`.
5. Preserve all unrelated tracked, untracked, staged, and dirty changes.
6. If the worktree does not match the requested task, select the correct existing worktree; do not switch, reset, rebase, merge, or clean blindly.

## Important Files

Read only the files relevant to the task, but use this order:

1. `AGENTS.md` — local rules and exact build/test commands.
2. `CONTEXT_HANDOFF.md` — primary self-contained project handoff.
3. `PROJECT_CONTEXT.md` and `ARCHITECTURE.md` — product and component boundaries.
4. `TRACEABILITY.md` and `API_CONTRACTS.md` — client/server flow and endpoint contracts.
5. `ANDROID_TV_CONTEXT.md` or `ANDROID_CONTEXT.md` — form-factor-specific constraints.
6. `WDTT_STATUS.md` — mandatory for any WDTT-adjacent change.
7. `SERVER_RUNBOOK.md` — mandatory before any server command or deployment plan.
8. `DECISIONS.md`, `KNOWN_ISSUES.md`, and `BACKLOG.md` — decisions, risks, and remaining work.
9. `GIT_ARCHAEOLOGY.md` — historical and obsolete approaches; never treat history as current without code evidence.

If a required document is missing, search with `rg --files`. If it cannot be found or read, report the gap and continue only where safe. Do not fabricate its content.

## Architecture Rules

- Preserve the universal APK model unless a verified ADR and explicit task require changing it.
- Do not invent separate TV/mobile apps, a player, pairing, or remote-control architecture. Prove these capabilities from current code before relying on them.
- Keep MaestroVPN as the sole Android `VpnService` unless an approved architecture decision says otherwise.
- Maintain TV/mobile gates explicitly. TV work must account for D-pad, focus, Back navigation, no touch, weak hardware, lifecycle, network loss, and state restoration.
- Mobile work must account for permissions, background restrictions, process death, deep links, synchronization, and API compatibility.
- Trace cross-component behavior through `Screen → ViewModel/manager → Use Case → Repository → API client → Endpoint → Handler → Service → Store/external system`. Mark absent layers rather than inventing them.
- Keep request and response models, method, path, authorization, error semantics, retry policy, and version gates synchronized across Android, API documentation, handlers, and tests.
- Never add automatic retry to a non-idempotent write until an end-to-end idempotency contract exists.
- Treat file storage as persistence with crash, concurrency, atomicity, migration, backup, and recovery requirements.
- Treat compiled backend source and the running binary as distinct. A source edit is not live until a controlled build/deploy/restart succeeds.
- Do not perform broad architecture rewrites to solve a local defect. Record a new durable architecture decision in `DECISIONS.md` when a justified boundary or contract changes.
- Preserve backward compatibility for installed sideload APKs unless the task explicitly authorizes a breaking change and supplies a fleet migration plan.
- For WDTT, preserve fail-closed platform/account/version gates, ordinary-protocol behavior, route-loop protections, explicit manual selection, and rollback containment.

## Mandatory Repetition Guard

Treat one unexplained failure as a hard block on that action. Run the repository
copy of `ops/maestro-repetition-guard.py` from the repository root; if the
repository copy is absent, run this skill's bundled
`scripts/maestro-repetition-guard.py`.

Before a server/network operation, GitHub write, file mutation, long scan/build,
or any retry:

```text
python <guard-script> check --action <semantic-action> --family <semantic-method>
```

Use only lowercase semantic IDs. Never pass raw commands, credentials, keys,
tokens, customer data, subscription URLs, or sensitive paths.

After the first unexpected failure, timeout, tool error, or owner correction that
the method is wrong:

1. Record `fail` and stop the action immediately.
2. Determine the root cause without repeating the blocked operation.
3. Register `correct` with a genuinely different family and a safe root-cause code.
4. Run `check` for the corrected family; make one attempt only.
5. Record `success` only after evidence proves the intended result.

Exit `42` or `43`, an unavailable guard, or an invalid ledger means STOP. Do not
replace the script with a remembered command or a chat note.

| Rationalization | Required response |
|---|---|
| "It was only quoting, sandbox, or a timeout" | Record `fail`; diagnose before another attempt. |
| "Different flags/client make this a new try" | Same action remains blocked until `correct`. |
| "It is read-only, so repetition is harmless" | Guard all server/network and long-running work. |
| "The handoff already says what worked" | Handoff informs; the executable ledger blocks. |
| "The owner needs speed" | One verified attempt is faster than repeated damage. |

### Repetition red flags — STOP

- Reissuing a failed command or command family.
- Switching tools before writing the root cause.
- Treating an owner correction as style feedback instead of a failed method.
- Bypassing the guard because the ledger is missing or damaged.
- Putting a command or secret into an action, family, reason, or evidence code.

All red flags require stopping and entering the failure/correction cycle.

## Required Workflow

### 1. Inventory before editing

1. Read applicable `AGENTS.md`.
2. Locate and read `CONTEXT_HANDOFF.md` completely.
3. Read the task-specific documents listed above.
4. Identify available repositories, current directory, branch, HEAD, remotes, and dirty state.
5. Restate the requested outcome and boundaries in concrete terms.
6. Find related screens, symbols, routes, handlers, stores, tests, flags, configuration, deployment code, and historical commits.
7. Identify what is confirmed, inferred, unknown, production-sensitive, or blocked.

If repositories are already available, discover them yourself. Ask for missing input only when it cannot be found and a safe assumption would materially change the result.

### Production discovery and deployment contract

Every production action begins by proving the live layout. Never infer a unit,
directory, database, reverse-proxy file, or feature state from an old server or
repository name.

1. Use `systemctl show <unit> -p FragmentPath -p ExecStart -p WorkingDirectory
   -p EnvironmentFiles`, then `is-active` and `is-enabled`. Product **3x-ui**
   normally runs as `x-ui.service`; never install or substitute S-ui.
2. Resolve the state path and format from live configuration such as
   `MAESTRO_STORE` or `DB_PATH`. Do not query an assumed SQLite path when the
   actual store is JSON, and do not dump secret values.
3. Resolve the active nginx symlink/config and bound port. Do not assume the
   enabled filename from a previous host.
4. Prove capabilities with the real disposable operation: for example, create
   a staging venv rather than trusting `venv --help` when `ensurepip` may be
   absent.
5. On PowerShell, isolate native checks and inspect `$LASTEXITCODE` immediately.
   A later success must never mask an earlier compiler, copy, or hash failure.
6. Back up WAL databases with the **SQLite backup API**, verify `quick_check`
   and invariants, and overlay code separately from state. Code-only deploys
   exclude `.env`, databases, logs, virtualenvs and backups; secret files stay
   mode 600.
7. Telegram verification includes `getMe`, `getWebhookInfo`, `getUpdates`,
   pending count and process count. Preserve pending updates and enforce
   **one poller** per token. Never confirm a real payment merely as a smoke test.
8. After deploy prove exact commit/hash, active + enabled units, one process,
   database integrity/counts, health/API, ports and recent logs. Compare
   customer expiry invariants before and after.
9. Special protocols retain the production allowlist contract: WDTT and olcRTC
   are never exposed to ordinary customers. Read the exact aliases from live
   protected configuration, not Git.
10. Do not claim the planned S2/S3/S4 `rqlite` organism is deployed until live
    quorum, one-node failure, leader change, stale-node rejoin, reconciliation
    and duplicate/idempotency audits all pass.

When a checked command fails, record guard `fail`, establish a root cause,
record `correct` with a materially different family, and only then retry. Do
not repeat the same guessed command with altered quoting.

### 2. Plan the smallest complete change

1. Map every affected component and consumer.
2. Compare Android request/response handling with the server endpoint and persistence behavior.
3. Check backward compatibility, idempotency, retry, concurrency, lifecycle, migration, logging, authorization, privacy, rollout, and rollback.
4. Prefer fixing the first proven failure boundary over speculative refactoring.
5. Do not include deployment, data mutation, service restart, migration, merge, push, or OTA publication unless explicitly authorized.

### 3. Implement narrowly

1. Modify only files required by the task.
2. Preserve user changes and existing dirty worktree state.
3. Follow local style and architecture.
4. Update both sides of a contract when the task authorizes a contract change.
5. Add or update tests at the lowest useful layer and at contract boundaries.
6. Avoid secrets, customer identifiers, full subscription URLs, raw credentials, and sensitive logs in code, fixtures, documentation, and command output.

### 4. Validate proportionally

1. Run targeted tests first, then broader relevant checks.
2. Validate Android and backend independently for a cross-component change.
3. Validate TV and mobile separately where runtime gates differ.
4. Distinguish static validation, local execution, emulator/device testing, staging, canary, and production observation.
5. Compare final `git diff` and `git status` with the initial inventory.
6. Explicitly state every check that was not run and why.

### 5. Refresh project context after material changes

Use the final diff to decide which documents require updates:

- architecture or ownership boundary → `ARCHITECTURE.md`, `PROJECT_CONTEXT.md`, `DECISIONS.md`;
- method/path/model/error/auth change → `API_CONTRACTS.md`, `TRACEABILITY.md`;
- TV/mobile behavior → corresponding Android context document;
- WDTT behavior/status/gate/test/rollout → `WDTT_STATUS.md`;
- new/resolved risk → `KNOWN_ISSUES.md`;
- new/completed/reordered work → `BACKLOG.md`;
- branch, commit, live state, priority, or next-step change → `CONTEXT_HANDOFF.md`;
- operational procedure → `SERVER_RUNBOOK.md`.

Record the verification date and relevant commit when refreshing volatile context. Do not update documentation with assumptions presented as facts.

## Commands

Run commands from the owning repository after reading `AGENTS.md`. Prefer discovery and read-only checks first.

### Inventory and Git

```bash
rg --files -g 'AGENTS.md' -g 'CONTEXT_HANDOFF.md'
git status --short --branch
git rev-parse --show-toplevel
git rev-parse HEAD
git branch --show-current
git remote -v
git diff --stat
git diff --check
git log --oneline --decorate -n 30
rg -n "WDTT|wdtt|TODO|FIXME|HACK|NotImplemented|UnsupportedOperation|stub|mock|temporary|deprecated"
```

Do not expose authenticated remote URLs. Redact credentials if a remote embeds them.

### Android

Confirm the exact Gradle tasks in `AGENTS.md`, CI, and `./gradlew tasks` before using examples such as:

```bash
./gradlew :app:testDebugUnitTest
./gradlew :app:lintDebug
./gradlew :app:assembleDebug
```

Use the project wrapper. Do not sign or publish a release unless explicitly requested and authorized.

### Go backend

Run from the backend module after checking `go.mod` and local instructions:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Use project-provided dry-run/status scripts when documented. Do not infer that successful compilation proves the deployed binary or external provisioning.

### Server

Read `SERVER_RUNBOOK.md` before connecting. Start with an adapted, redacted read-only set:

```bash
whoami
uname -a
git status --short --branch
systemctl status <service> --no-pager
systemctl list-units --failed --no-pager
journalctl -u <service> --since "30 min ago" --no-pager
ss -lntup
curl -fsS http://127.0.0.1:<port>/healthz
df -h
free -h
```

Avoid broad environment dumps, `cat .env`, customer store contents, full process arguments, or logs likely to reveal tokens. Use the runbook's exact service, port, and script names rather than guessing.

## Safety Constraints

- Treat production as read-only unless the user explicitly authorizes the exact mutation and its scope.
- Before an approved mutation, state the environment, target, expected impact, backup/preconditions, validation, and rollback.
- Never restart/reload services, deploy binaries, change nginx/systemd/firewall/ports, enable flags, publish OTA, run migrations/backfills, provision/renew/confirm orders, or edit production data by implication.
- Never run destructive Git or filesystem operations such as hard reset, clean, forced checkout, recursive deletion, or history rewrite without explicit authorization and verified paths.
- Never overwrite, discard, or reformat unrelated user changes.
- Never print, store, commit, or document passwords, private keys, tokens, cookies, `.env` contents, customer identifiers, private certificates, full connection strings, full subscription URLs, or transport credentials.
- Use placeholders such as `API_TOKEN=<SECRET>` and redact sensitive log fields.
- Do not weaken authorization, TLS verification, host-key verification, feature gates, device restrictions, or VPN routing safeguards to make a test pass.
- Do not test destructive failure paths on production. Use unit tests, fault injection, local fixtures, staging, or a specifically approved canary.
- Do not claim a server function is active merely because code exists. Verify unit/container/flag/port/health state.
- Stop and request direction when safe completion requires a production mutation, access not already authorized, a breaking API decision, data migration, or a materially different architecture.

## Validation

Build a task-specific matrix and report evidence for each applicable row:

| Area | Required validation |
|---|---|
| Git scope | Initial/final branch, HEAD, status, diff, unrelated changes preserved |
| Android common | Compile, unit tests, config/profile validation, lifecycle and error paths |
| Android TV | D-pad, focus order/restore, Back, no touch, weak-device behavior, network recovery, TV release gates |
| Android mobile | Permissions, background/process-death behavior, deep links, synchronization, API-version compatibility |
| API | Method, path, authorization, request, response, errors, old-client compatibility, timeout/retry semantics |
| Backend | Unit/integration tests, `go vet`, race/concurrency behavior, persistence failure and migration paths |
| Data | Atomicity, idempotency, repeat requests, backup, rollback, partial failure, possible loss |
| WDTT | Exact build/profile/upstream hashes, libbox validation, normal-selector non-regression, mobile gate, TV absence, child lifecycle, route-loop evidence, rollback |
| Operations | Build provenance, health, logs, service state, ports, drift, staging/canary boundary |
| Documentation | Updated only where behavior/status/decision changed; date and source recorded |

For a diagnostic task, do not implement an unrequested fix. For an implementation task, do not stop after code changes while safe relevant validation remains.

## Common Failure Modes

- Starting from an old prompt or chat instead of the current `CONTEXT_HANDOFF.md`.
- Editing the wrong checkout or branch because the worktree was not inventoried.
- Treating a dirty repository as equal to its HEAD or cleaning changes that belong to someone else.
- Assuming Android TV and mobile are separate apps when the current product uses a universal APK.
- Inventing a player, TV pairing, or phone remote-control flow from naming alone.
- Changing only Android or only backend while silently breaking an API contract.
- Adding retry to order, trial, provisioning, or profile writes without idempotency and crash recovery.
- Treating mutex-only, memory-only, or temp-file logic as a durable transaction.
- Treating a green build, existing endpoint, feature branch, or draft PR as production enablement.
- Editing Go source and assuming the running compiled service changed.
- Enabling WDTT before preserving exact failure evidence and proving ordinary selectors still work.
- Running broad production logs or environment commands that disclose secrets or customer data.
- Performing a broad architecture refactor before locating the first proven failure boundary.
- Updating `DECISIONS.md`, issues, backlog, or handoff with guesses instead of sourced facts.
- Reporting success without listing skipped tests, unavailable devices, inaccessible environments, or unverified server state.
- Guessing `ExecStart`, `WorkingDirectory`, environment file, nginx symlink, or state format instead of reading the running unit/configuration.
- Treating a successful native command later in a PowerShell line as proof that every earlier command succeeded; check `$LASTEXITCODE` per command.
- Copying an SQLite main file while WAL/SHM are live instead of using the SQLite backup API.
- Shipping a repository/archive that contains `.env`, a database, logs, virtualenv or backups, thereby overwriting production state.
- Starting a second Telegram poller for the same token, discarding pending updates, or using a real payment to test idempotency.
- Describing a planned `rqlite` topology as HA before quorum, failover, rejoin and duplicate audits are demonstrated on live-shaped data.
- Retrying direct `apply_patch` after a Windows `deny-read ACL` failure. Create the patch outside the affected worktree, run `git apply --check --recount`, then `git apply --recount` exactly once.

## Definition of Done

Consider the task complete only when all applicable conditions hold:

- The requested outcome is implemented or the diagnostic question is answered with evidence.
- The correct repository, branch, HEAD, and initial dirty state were recorded.
- Only necessary files changed; unrelated changes remain untouched.
- Android/API/backend/server effects were traced where relevant.
- Backward compatibility, authorization, error handling, idempotency, retry, concurrency, migration, data loss, rollout, and rollback were considered proportionally.
- Relevant automated checks pass, and manual/staging/production checks are clearly distinguished.
- Every unperformed or inconclusive validation is listed explicitly.
- New architectural decisions are recorded in `DECISIONS.md` when applicable.
- `KNOWN_ISSUES.md`, `BACKLOG.md`, `WDTT_STATUS.md`, API/architecture documents, and `CONTEXT_HANDOFF.md` are refreshed when their facts materially changed.
- Final `git diff --check` and `git status --short --branch` were reviewed.
- The report lists changed files, results, risks, next step, and whether project context needs another refresh.
- No secrets or sensitive customer data appear in source, fixtures, logs, documentation, or the report.

If any required condition cannot be met, name the gap and do not describe the work or project context as complete.

## Required Output Format

Use this structure for a completed change or diagnosis:

```markdown
## Что было сделано

## Какие файлы изменены

## Почему выбран такой подход

## Какие проверки выполнены

## Результаты проверок

## Что не удалось проверить

## Риски

## Следующий шаг

## Нужно ли обновить проектный контекст
```

For a multi-stage audit, use this concise checkpoint after each stage:

```markdown
## Изучено

## Подтверждённые факты

## Найденные проблемы

## Неизвестные данные

## Созданные или обновлённые файлы

## Следующий шаг
```

Do not repeat the entire accumulated context in every update. Persist durable facts in the project documents and keep the conversational summary short.
