# Task 5 Report: Atomic Backup Dirty Tracking

Date: 2026-08-24
Baseline application version: 1.0.157 (unchanged)

## Scope

Implemented atomic backup dirty-generation tracking for the four approved core mutations: ClaimDevice, updateSetting, RevokeSessions, and ensureWhiteListEntitlement. The dirty marker is package-private, parameterized, and emitted immediately after the authoritative mutation in the same rqlite transaction.

## Implementation

- Added backupRPODirtyGenerationStatement(updatedAtUnix int64).
- The statement increments the singleton dirty generation only when SQLite changes() reports a changed immediately preceding mutation.
- The increment is additionally gated on an active restore state whose restore epoch matches backup_rpo_state.
- No standalone MarkDirty API or post-commit dirty window was introduced.
- ClaimDevice exact idempotent replay now leaves the authoritative upsert unchanged and does not dirty the backup.
- RevokeSessions now requires a committed principal epoch increment; a missing principal returns ErrNotFound and cannot produce audit-only success.
- The whitelist test fixture was updated for the new atomic transaction shape.

## TDD Evidence

RED evidence: the focused control-plane tests failed to compile with undefined: backupRPODirtyGenerationStatement before the implementation existed.

GREEN evidence:
- Focused Task 5 control-plane tests: PASS (0.067s).
- Full backend/internal/controlplane regression package: PASS (0.096s).
- rqlite integration-tag compile: PASS (0.046s, no tests to run).
- In-memory SQLite behavioral proof: PASS; success=2, failed=2, inactive=2, mismatched=2.
- git diff --check: PASS.

The first full regression correctly exposed the stale whitelist fixture transaction length; the fixture was corrected and the package then passed.

## Contract Coverage

Tests cover one increment for successful mutations, zero increments for failed CAS, device-limit rejection, missing principal, exact idempotent replay, and read-only calls. Existing public return/error behavior and unknown-outcome reconciliation paths remain intact.

## Independent Review Correction

Independent review found a race in the original updateSetting wiring: a losing CAS was followed by dependent SQL gated only on the requested next generation. If another request had already committed that generation, the loser could replace members and secret data and append its audit event while returning ErrConflict; the immediate dirty marker correctly stayed unchanged.

The correction adds immutable migration v6 with cluster_settings.last_mutation_token, generates a fresh opaque 128-bit-backed setting-mut ID for every valid update request, writes it only in the winning authoritative CAS, and requires the exact key, generation, and token in the immediate dirty statement and every dependent member, secret, placeholder, and audit statement. Public validation, return/error behavior, transaction ordering, and existing unknown-outcome behavior remain unchanged.

Review RED evidence:
- The real in-memory SQLite regression executed the exact generated SQL and binds. Before the fix, the stale loser returned ErrConflict and left dirty generation unchanged, but replaced the winner member/secret and appended a loser audit; snapshot equality failed.
- The additive migration regression failed with SchemaVersion = 5, want 6.

Review GREEN evidence:
- Focused token/CAS/migration tests: PASS (0.403s).
- Ordered migration v1-v6 upgrade/no-op tests: PASS (0.053s).
- Full backend/internal/controlplane regression package: PASS (0.334s).
- rqlite_integration tagged compile: PASS (0.048s, no tests to run).
- Seven changed Go files equal their official Go 1.25 gofmt mirrors after LF normalization; the canonical CRLF working-tree policy is the only raw gofmt -d difference.
- git diff --check: PASS; the only output is the pre-existing protected Task 4 report EOL warning.

The first post-fix full regression exposed only the old exact v1-v5 migration-chain expectation. The test was updated to verify v1 to v6, v4 to v5 plus v6, v5 to v6, and exact v6 no-op behavior, then the full package passed.

## Second Independent Review Correction

A second independent review found that both dirty-generation helpers incremented dirty_generation without changing phase. The real migration v5 invariant permits dirty_generation greater than verified_generation only when phase is dirty. Therefore the first core mutation after a verified backup violated the CHECK constraint and rolled back its entire business transaction.

The production correction sets phase to dirty in the same backup_rpo_state UPDATE as the generation increment and timestamp. Immediate changes() proof, setting mutation-token proof, active/current restore-epoch gating, statement ordering, public APIs, and unknown-outcome behavior are unchanged.

Second review RED evidence:
- A real-schema SQLite test loaded the exact immutable migrations v1-v6, seeded a valid verified state, and executed an authoritative mutation immediately followed by the generated helper SQL/binds.
- Before the fix the focused test failed with: CHECK constraint failed: (dirty_generation > verified_generation AND phase = 'dirty') OR (dirty_generation = verified_generation AND phase = 'verified').
- The transaction rolled back, proving the production failure rather than a recording-mock mismatch.

Second review GREEN evidence:
- Focused verified-state actual-schema regression: PASS (0.578s).
- Actual-schema four-flow matrix: PASS (5.439s), covering ClaimDevice, updateSetting, RevokeSessions, and ensureWhiteListEntitlement in dirty, verified, inactive, mismatched-epoch, and flow-specific no-op/error/replay states (20 cases).
- The matrix proves successful active mutations increment exactly once and end dirty; inactive/mismatched restore epochs commit the business mutation without incrementing; device-limit rejection, failed setting CAS, missing principal, and whitelist replay increment zero and create no audit-only effects.
- Focused failed-CAS SQLite regression: PASS (0.369s).
- Full backend/internal/controlplane regression package: PASS (6.178s).
- rqlite_integration tagged compile: PASS (0.068s, no tests to run).

The first full regression after the phase fix exposed the old synthetic failed-CAS fixture's missing phase column. That fixture now includes verified_generation, phase, and the production phase/generation CHECK; its focused test and the full package then passed.

## Final Gate Closure

Final implementation code SHA: e9c1c773bba99e6b68f7ef4503f4a9e157524be9.

Local RED evidence:
- The actual migration-v1-v6 SQLite proof seeded a valid verified backup state and failed because the helper incremented dirty_generation without changing phase; SQLite rejected the transaction with the migration-v5 phase/generation CHECK.
- The failed-CAS regression separately proved the earlier setting generation-only gate could mutate winner members, secret, and audit while leaving backup dirty generation unchanged.

Local GREEN evidence:
- Focused verified-state actual-schema regression: PASS (0.578s).
- Actual-schema four-flow/five-state matrix: PASS, 20/20 cases (5.439s).
- Focused failed-CAS SQLite regression: PASS (0.369s).
- Full backend/internal/controlplane regression package: PASS (6.178s).
- rqlite_integration tagged compile: PASS (0.068s, no tests to run).
- Cached git diff check: PASS.

GitHub final verification:
- HA workflow run 32698556348: SUCCESS.
- HA job 97345308261: SUCCESS, 26/26 steps.
- Yandex isolated release workflow run 32698556317: SUCCESS.
- format-unit job 97345308153: SUCCESS.
- offline-replay job 97345453295: SUCCESS.
- race-vet job 97345453347: SUCCESS.
- rqlite-purge job 97345453396: SUCCESS.
- android-test-apk job 97345699081: SUCCESS.
- Android build, metadata, signer, and artifact steps: SUCCESS.

Fresh independent final review:
- Specification compliance: PASS.
- Code quality: APPROVED.
- Findings: no Critical, Important, or Minor issues.

## Safety and Version

- Baseline application version remains 1.0.157; no version bump was made.
- Protected normalize.patch and the Task 4 report were not edited or staged by this task.
- No production, server, Telegram bot, OTA, release, cutover, or workflow mutation was performed; the recorded GitHub runs were verification only.

## Commits

Implementation title: feat(ha): mark core mutations backup-dirty atomically
Final implementation code SHA: e9c1c773bba99e6b68f7ef4503f4a9e157524be9
Report-only closure title: docs(ha): close task 5 dirty-generation gate
