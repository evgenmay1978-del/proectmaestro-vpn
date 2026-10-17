# Task 2 Report: Add the additive v5 backup RPO schema

## Status

Implementation and local verification are complete. The local code commit was created, but push and remote exact-SHA verification are blocked because the approval reviewer rejected the network write. The root task must use the established approved push route and then verify `origin/codex/yandex-cdn-whitelist-task3-sync` resolves to the final amended local SHA.

## Files changed

- `backend/internal/controlplane/migrations/0005_backup_rpo.sql`
  - Adds additive lease fencing/capability columns with safe zero/null defaults for every pre-existing non-`backup-rpo` row.
  - Adds singleton `backup_rpo_state`, restore-scoped durable `backup_rpo_attempts`, append-only delete protection, and the initial forced-dirty seed from `cluster_restore_state`.
- `backend/internal/controlplane/migrations.go`
  - Sets `SchemaVersion=5`, registers migration v5, and adds the two tables to the exact schema table set.
- `backend/internal/controlplane/whitelist_store_test.go`
  - Keeps the migration-v4 identity assertion while allowing later additive migrations; this was the only stale non-frozen package test exposed after v5 became current.
- `.superpowers/sdd/2026-08-23-maestrovpn-ha-backup-production-adapters/task-2-report.md`
  - Records implementation, verification, compatibility, risks, and the remote blocker.

The frozen Task 1 acceptance files were not changed: `migrations_ordered_test.go`, `migrations_test.go`, and `schema_constraints_test.go`.

## Implementation summary

Migration v5 provides:

- `backup_rpo_state` with singleton identity, positive restore epoch, monotonic dirty/verified generations, exact nullable verified-object tuple, manifest v2, attempt sequence, phase consistency, and positive update time.
- `backup_rpo_attempts` with `UNIQUE(restore_epoch, attempt_sequence)`, positive captured generation/fence/capability proof, exact `yandex-s3-v1` adapter contract, canonical SHA-256 fields, versioned object identity, bounded redacted failure code, explicit phases, and delete rejection.
- Additive `cluster_job_leases` columns `restore_epoch`, `lease_fence`, `capability_generation`, `capability_evidence_sha256`, and `capability_expires_at_unix`. Existing non-backup rows remain valid with defaults `0`, `0`, `0`, `NULL`, and `0`; `backup-rpo` rows must provide a positive epoch/fence/generation/expiry and canonical capability digest.
- A singleton seed copied from the current `cluster_restore_state.restore_epoch` with `dirty_generation=1`, `verified_generation=0`, `last_attempt_sequence=0`, `phase='dirty'`, and a fully null verified-object tuple.

The attempt-sequence invariant is exactly restore-scoped: the same sequence is accepted in different restore epochs, while a duplicate `(restore_epoch, attempt_sequence)` is rejected.

## Exact commands and results

### Initial inventory

```powershell
git rev-parse --show-toplevel
git branch --show-current
git rev-parse HEAD
git remote
git status --short --branch
git diff --stat
```

Result: assigned worktree and canonical branch confirmed at base `14719debb301b9980937c1573c4fe904344c2f8a`; protected dirty files were `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-4-report.md` and `normalize.patch`.

### Official local toolchain

```powershell
C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\go1.25.0\go\bin\go.exe version
```

Result: `go version go1.25.0 windows/amd64`.

All Go tests used:

```powershell
$env:GOMAXPROCS='1'
$env:GOMEMLIMIT='512MiB'
$env:GOFLAGS='-p=1'
$env:GOCACHE='C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\gocache'
$env:GOMODCACHE='C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\gomodcache'
```

### RED evidence

```powershell
go test ./internal/controlplane -run 'TestOrderedMigrations|TestBackupRPO' -count=1
```

Result: expected RED; `TestOrderedMigrationsExposeExactChain` failed because `SchemaVersion=4` and migration v5 was absent.

### Generated-patch flow

The desired files were generated in external `old/backend` and `new/backend` mirrors. Headers were inspected as `a/old/...` and `b/new/...` (new files used `a/new/...` to `b/new/...`), then the LF-preserving contextual diff was streamed through:

```powershell
git apply --check --recount -p2 -
git apply --recount -p2 -
```

Results: `APPLY_CHECK_OK`, followed by exactly one successful apply (`PATCH_APPLIED_ONCE`). The same guarded old/new/check/one-apply flow was used for the single stale v4 test line.

### GREEN evidence

```powershell
go test ./internal/controlplane -run 'TestOrderedMigrations|TestBackupRPO' -count=1
```

Result: `ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.084s`.

```powershell
go test ./internal/controlplane -count=1
```

First result: failed only at `TestWhiteListEntitlementMigrationFourIsAdditiveAndImmutable`, which still required the total chain and `SchemaVersion` to equal 4. The test was narrowed to preserve the exact migration-v4 prefix identity without constraining later additive migrations.

Corrected one allowed rerun result: `ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.078s`.

```powershell
git diff --check
git diff --cached --check
```

Result: exit 0. The only output was the pre-existing line-ending warning for the protected unstaged task-4 report.

### Migration immutability evidence

`git rev-parse HEAD:<path>` and `git hash-object <path>` matched for every pre-existing migration:

- v1: `0b24196b715b6acac0f253368fe77ec6066d5582`
- v2: `2ebfe039f5048d6597ba907e6ea0fbc731e6c49a`
- v3: `4f5343766ea7f53e5af36597358b4d15366dfcdd`
- v4: `853a94f9c986ed801a25c49eb09d116ecda8982d`

All four reported `MATCH=True`.

### Commit and push

```powershell
git commit -m "feat(ha): add durable backup RPO schema"
```

Result before adding this report: local code commit `245453ee3e8da78882bea99f3c88075ec7961682` with three files, 173 insertions, and two deletions. This report is being force-staged and the same commit is amended while preserving that exact title, so the final local SHA supersedes `245453e...`.

```powershell
git push origin codex/yandex-cdn-whitelist-task3-sync
```

Result: not executed by Git. The approval reviewer rejected the network write because it treated the destination as unverified. The failure was recorded in the repetition guard, the push family was not retried, and no workaround was attempted.

## Migration and rollback compatibility

- Upgrade is ordered and additive: an exact v4 prefix applies only v5; an exact v5 prefix is a no-op.
- Migrations v1-v4 remain byte-identical, preserving all prior stored checksums.
- Existing non-backup lease rows survive v5 with safe defaults and need no backfill.
- The initial RPO row deliberately forces a first HA backup and binds it to the current restore epoch.
- There is no destructive down migration. Once v5 is recorded, a binary that only knows through schema v4 will fail closed on an unknown migration, as designed. Operational rollback therefore requires a v5-aware previous binary or a verified pre-migration database restore under an approved write-freeze; it must not delete the v5 receipt or columns in place.
- No migration was run against production and no production database was accessed.

## Self-review

- Scope is surgical: one new SQL migration, the registry/table-set update, and one stale non-frozen v4 prefix test.
- Frozen Task 1 schema expectations were not weakened or rewritten.
- The composite uniqueness is exactly `(restore_epoch, attempt_sequence)`; no global attempt-sequence uniqueness was introduced.
- Verified state cannot contain a partial object tuple, non-canonical digest, non-versioned object identity, invalid manifest version, or `verified_generation > dirty_generation`.
- Backup attempts require positive restore/fence/capability fields and an exact adapter contract; delete attempts are rejected to preserve history.
- Pre-existing non-backup leases remain valid with zero/null defaults; strict positive cross-field checks activate only for `job_name='backup-rpo'`.
- No workflow YAML, Android/TV, production, deployment, release, OTA, tag, merge, or version `1.0.157` state changed.
- Protected `normalize.patch` and the existing task-4 report remained unstaged and unmodified by this task.

## Tests not run

- `go test ./...`, `go test -race ./...`, and `go vet ./...` were not run locally because the owner machine is restricted to narrow single-core/memory-limited checks and broad validation is GitHub-first.
- Linux/real-rqlite integration, HA workflow, DR workflow, and Yandex isolated release workflow were not run because the push was blocked before GitHub could receive the exact SHA.
- No production migration, backup, restore, systemd, S1-S4, Yandex Object Storage, customer, bot, billing, Android/TV, release, or OTA validation was authorized or performed.

## Risks and remaining work

- Remote exact-SHA evidence is not yet available. The root task must push the final amended SHA through the established approved route and verify the branch ref exactly.
- Broad GitHub checks remain pending on that pushed SHA.
- This task adds schema only. Durable store methods, transactional mutation wiring, Yandex adapter behavior, systemd exclusivity, and production migration gates belong to later tasks and remain unimplemented.
- Production remains NO-GO.

## Remote exact-SHA evidence

Blocked at this task boundary. No successful network write occurred, so no remote ref is claimed. Required closeout after the root push:

```powershell
git rev-parse HEAD
git ls-remote origin refs/heads/codex/yandex-cdn-whitelist-task3-sync
```

The two full SHAs must match before remote synchronization is reported.

## Fix round 1 — reviewer findings

Date: 2026-08-23.

### Findings fixed

1. A verified `backup_rpo_state` row could set `verified_size_bytes`, `verified_manifest_version`, or `verified_at_unix` to SQL `NULL`. SQLite accepts a `CHECK` expression whose result is `NULL`, so the existing positive/equality comparisons were insufficient.
2. `UNIQUE(restore_epoch, attempt_sequence)` plus the no-delete trigger did not burn an identity pair durably because an `UPDATE` could re-key the row and free the old pair.

The minimal production change adds explicit `IS NOT NULL` predicates for those three verified scalar fields and a `BEFORE UPDATE OF restore_epoch, attempt_sequence` trigger whose `WHEN` clause rejects only actual identity-key changes. Phase, object-version, and timestamp lifecycle updates remain allowed.

### Files changed in fix round 1

- `backend/internal/controlplane/schema_constraints_test.go` — permanent behavioral coverage for all three nullable verified scalars, both identity-key updates, and one allowed phase/object-version update.
- `backend/internal/controlplane/migrations/0005_backup_rpo.sql` — three explicit non-null predicates and the identity-key immutability trigger.
- `.superpowers/sdd/2026-08-23-maestrovpn-ha-backup-production-adapters/task-2-report.md` — this fix-round evidence.

Protected `normalize.patch` and `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-4-report.md` remained unstaged and unmodified by this task.

### Strict TDD evidence

The local machine must not start a three-node rqlite cluster. The tagged tests were therefore compiled locally and executed behaviorally through a scratch-only Python `sqlite3` bridge that applies the real repository migration file. The scratch harness is outside the repository and is not committed.

Initial test selection without the build tag:

```powershell
& 'C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\go1.25.0\go\bin\go.exe' test ./internal/controlplane -run '^TestBackupRPOSchemaRejectsInconsistentStateAndUnfencedAttempts$' -count=1
```

Result: `ok ... [no tests to run]`. The guard recorded this as a failure; inspection confirmed `//go:build rqlite_integration`.

The corrected tagged runtime command reached the fixed local endpoints but no cluster was available:

```powershell
& 'C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\go1.25.0\go\bin\go.exe' test -tags=rqlite_integration ./internal/controlplane -run '^TestBackupRPOSchemaRejectsInconsistentStateAndUnfencedAttempts$' -count=1
```

Result: failed at `controlplane: verify voter foreign keys: rqlite: request transport failed`. It was not retried. The owner prohibited starting rqlite on this PC and corrected the local gate to compile-only plus an isolated behavioral executor.

Compile-only baseline after adding permanent Go tests:

```powershell
& 'C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\go1.25.0\go\bin\go.exe' test -c -tags=rqlite_integration -o 'C:\Users\User\Documents\Codex\2026-08-23-task15-task2-fix1-red-controlplane.test.exe' ./internal/controlplane
```

Result: exit `0`.

The test-only RED checkpoint was committed and pushed before production SQL changed:

- commit `409437644a493f6c74c12c1c632c63dad0b09470`, title `test(ha): expose backup RPO schema gaps`;
- `git push origin HEAD:refs/heads/codex/yandex-cdn-whitelist-task3-sync` succeeded;
- `git ls-remote` matched the same full SHA.

HA run `32652513522` failed at `Test rqlite integration`, but base run `32650871498` for `8d22a342282da8149b383b159dfa1789f51970d0` failed at the same step. That comparison was explicitly rejected as TDD evidence. Public job logs required repository admin access, and the browser runtime was unavailable; neither blocked route was retried.

The corrected scratch behavioral harness command was:

```powershell
$env:PYTHONDONTWRITEBYTECODE='1'
python 'C:\Users\User\Documents\Codex\2026-08-23-task15-task2-sqlite-harness-01\verify_v5_backup_rpo.py' 'C:\Users\User\Documents\Codex\2026-08-05\new-chat\mvpn-yandex-cdn-whitelist-task3-sync\backend\internal\controlplane\migrations\0005_backup_rpo.sql'
```

Before the SQL fix it executed the real v5 statements and produced the required assertion-level RED:

```text
ASSERTION_LEVEL_RED=null-verified-size-bytes,null-verified-manifest-version,null-verified-at-unix,rekey-restore-epoch,rekey-attempt-sequence
AssertionError: backup RPO v5 behavior gaps: null-verified-size-bytes, null-verified-manifest-version, null-verified-at-unix, rekey-restore-epoch, rekey-attempt-sequence
EXPECTED_ASSERTION_LEVEL_RED_CONFIRMED
```

The same RED execution also proved the pending-to-applied phase/object-version update succeeded; otherwise it would have reported `allowed-lifecycle-update` and failed the expected-RED gate for the wrong reason.

The generated SQL patch was inspected completely, passed `git apply --check --recount -p2`, and was applied exactly once. Running the identical harness afterward produced:

```text
ASSERTION_LEVEL_GREEN=all-null-and-identity-rejections-with-lifecycle-update
```

### Local GREEN commands and results

Every Go command used Go `1.25.0` with `GOMAXPROCS=1`, `GOMEMLIMIT=512MiB`, `GOFLAGS=-p=1`, and the established external Go caches.

```powershell
& 'C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\go1.25.0\go\bin\go.exe' test -c -tags=rqlite_integration -o 'C:\Users\User\Documents\Codex\2026-08-23-task15-task2-fix1-green-controlplane.test.exe' ./internal/controlplane
```

Result: exit `0`.

```powershell
& 'C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\go1.25.0\go\bin\go.exe' test ./internal/controlplane -run 'TestOrderedMigrations|TestBackupRPO' -count=1
```

Result: `ok .../backend/internal/controlplane 0.049s`.

```powershell
& 'C:\Users\User\Documents\Codex\2026-08-05\new-chat\.tools\go1.25.0\go\bin\go.exe' test ./internal/controlplane -count=1
```

Result: `ok .../backend/internal/controlplane 0.053s`.

`git diff --check` and the staged diff check both exited `0`. Migrations v1-v4 remained byte-identical:

- v1 `0b24196b715b6acac0f253368fe77ec6066d5582` — `MATCH=True`;
- v2 `2ebfe039f5048d6597ba907e6ea0fbc731e6c49a` — `MATCH=True`;
- v3 `4f5343766ea7f53e5af36597358b4d15366dfcdd` — `MATCH=True`;
- v4 `853a94f9c986ed801a25c49eb09d116ecda8982d` — `MATCH=True`.

### Fix commit, remote SHA, and GitHub GREEN

The minimal SQL fix commit is:

- `95bfe9f230ea48b3c2544863d18712fa022dda88` — `fix(ha): enforce backup RPO invariants`.

```powershell
git push origin HEAD:refs/heads/codex/yandex-cdn-whitelist-task3-sync
git rev-parse HEAD
git ls-remote origin refs/heads/codex/yandex-cdn-whitelist-task3-sync
```

Result: the explicit-refspec push succeeded and both local and remote were exactly `95bfe9f230ea48b3c2544863d18712fa022dda88` before this report-only closeout commit.

GitHub HA evidence on that exact code SHA:

- run `32653849782`: `completed/success`;
- job `97229458579`, `Go and isolated rqlite`: `completed/success`;
- step `Test rqlite integration`: `completed/success`.

The exact-SHA HA job also contains the repository formatting, unit, race, vet, harness-contract, and isolated-rqlite gates defined by the unchanged workflow. No workflow YAML was edited.

### Migration and rollback compatibility after fix round 1

- Migrations v1-v4 are unchanged byte-for-byte.
- The v5 change is still additive relative to v4; safe defaults for every pre-existing non-backup `cluster_job_leases` row are unchanged.
- The identity trigger is limited to changes of `restore_epoch` or `attempt_sequence`; a no-op assignment of the same keys and ordinary lifecycle updates are not blocked.
- The verified tuple now rejects SQL `NULL` for all required scalar fields instead of relying on three-valued `CHECK` evaluation.
- The v5 checksum changed between preview commits `8d22a34...`/`4094376...` and the corrected code commit `95bfe9f...`. No production migration was run and production remains NO-GO. Any disposable environment that applied the preview v5 must be reset or restored from a pre-v5 snapshot; deleting or editing the migration receipt in place is not an approved rollback.
- Rolling a database that has applied corrected v5 back to a binary containing the preview v5 checksum fails closed by design. Operational rollback requires the corrected-v5-aware binary or a verified pre-v5 database restore under the existing write-freeze procedure.

### Fix-round self-review

- The production delta is twelve SQL lines: three explicit `IS NOT NULL` predicates and one nine-line trigger statement.
- The attempt uniqueness remains exactly `UNIQUE(restore_epoch, attempt_sequence)`; the same sequence remains valid in a different restore epoch.
- The trigger closes only the re-key escape hatch and leaves phase/object-version updates functional, as proved by both permanent Go coverage and the local real-SQL harness.
- Frozen Task 1 expectations were strengthened, not weakened; no v1-v4 migration, workflow, production, deployment, release, OTA, tag, merge, or version `1.0.157` state changed.
- No secrets, customer identifiers, production endpoints, or credentials were written to the harness, tests, migration, report, or command output.

### Tests not run and remaining risks

- Tagged rqlite integration tests were not executed locally because the owner prohibited starting a cluster on the weak PC. They compiled locally and passed on GitHub's isolated three-node cluster at the exact code SHA above.
- Local `go test ./...`, `go test -race ./...`, and `go vet ./...` were not run under the owner-machine limits. The unchanged exact-SHA HA job completed successfully, including its broader backend/race/vet gates.
- The scratch Python harness is an executor bridge, not a shipped test artifact; permanent behavior coverage is in `schema_constraints_test.go`.
- No production database, Yandex Object Storage, systemd service, S1-S4 node, customer, billing, bot, Android/TV, release, deployment, OTA, tag, merge, or version mutation was accessed or performed.
- Production remains NO-GO. Later adapter/store/wiring tasks and their rollout gates remain required.
