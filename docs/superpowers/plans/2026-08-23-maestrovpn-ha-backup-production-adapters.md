# MaestroVPN HA Backup Production Adapters Implementation Plan

> **For Codex:** REQUIRED SUB-SKILLS: use `executing-plans`, `subagent-driven-development`, `test-driven-development`, `requesting-code-review`, and `verification-before-completion`. Execute one task at a time in the canonical worktree and keep the Maestro repetition guard single-writer.

**Goal:** Turn the existing offline HA backup state machine into a durable, fail-closed, repository-ready production backup path with transactional RPO generations, fenced ownership, exact Yandex Object Storage versions, and mutually exclusive legacy/HA systemd templates.

**Architecture:** Keep the existing Go rqlite client as the only production database transport. Add migration v5 and a Go `BackupRPOStore` whose every mutation is a single linearizable rqlite transaction and whose unknown outcomes are resolved only by exact linearizable reads. Keep `ops/ha/backup_worker.py` as the executable-independent transition/security contract. Add a Go runtime under `internal/backuprpo` and `cmd/maestro-backup-worker` that uses the durable store, the existing authenticated bundle format, and a narrow AWS SDK v2 S3 port. The worker accepts only a versioned Yandex bucket, stores and reads an exact `VersionId`, and has no delete capability. Repository systemd units remain inert: this plan does not enable, start, deploy, migrate production, change Yandex Cloud, or touch ordinary VPN users.

**Tech stack:** Go 1.25, existing `internal/rqlite` mTLS client, SQLite/rqlite migration SQL, Python 3 contract tests, Bash bundle tooling, AWS SDK for Go v2 S3 client, systemd unit templates, GitHub Actions.

**Approved parent design:** `docs/superpowers/specs/2026-08-09-maestrovpn-ha-control-plane-design.md` and Task 14 Step 4 of `docs/superpowers/plans/2026-08-09-maestrovpn-ha-04-operations-cutover.md`. This file narrows implementation details; it does not introduce a new product or a new production cutover decision.

**Hard gates:** production remains NO-GO throughout this plan. Never enable/start either backup unit, change a production database, create or modify a Yandex bucket, upload a production backup, delete an object version, deploy credentials, or alter ordinary VPN/3x-ui/Xray/OTA state.

---

### Task 1: Freeze migration v5 and upgrade invariants in failing tests

**Files:**
- Modify: `backend/internal/controlplane/migrations_ordered_test.go`
- Modify: `backend/internal/controlplane/migrations_test.go`
- Modify: `backend/internal/controlplane/schema_constraints_test.go`
- Test: `backend/internal/controlplane/migrations_ordered_test.go`
- Test: `backend/internal/controlplane/schema_constraints_test.go`

1. Add RED assertions that the ordered chain is exactly v1-v5 and that an exact v4 prefix applies only v5.
2. Add RED schema assertions for singleton `backup_rpo_state`, durable `backup_rpo_attempts`, and added fenced fields on `cluster_job_leases`.
3. Assert the seed is `dirty_generation=1`, `verified_generation=0`, has no verified object, and is bound to the current restore epoch.
4. Assert impossible partial verified identities, non-canonical SHA-256, `verified_generation > dirty_generation`, invalid phases, duplicate attempt sequence, null/latest object versions, and non-positive fences are rejected.
5. Run the focused unit tests and capture the expected failure because migration v5 does not exist:
   `cd backend && go test ./internal/controlplane -run 'TestOrderedMigrations|TestBackupRPO' -count=1`
6. Commit only after the RED result is recorded in the SDD task report.

### Task 2: Add the additive v5 schema

**Files:**
- Create: `backend/internal/controlplane/migrations/0005_backup_rpo.sql`
- Modify: `backend/internal/controlplane/migrations.go`
- Modify: `backend/internal/controlplane/migrations_ordered_test.go`
- Modify: `backend/internal/controlplane/migrations_test.go`

1. Add `backup_rpo_state` as a singleton with restore epoch, dirty/verified generations, verified timestamp, exact object key/version/digest/size, backup ID, manifest version, last attempt sequence, and explicit cross-field checks.
2. Add append-only `backup_rpo_attempts` with one burned sequence per restore epoch, exact captured generation, adapter contract `yandex-s3-v1`, object identity, upload/verification phase, lease fence, capability proof, and redacted failure code.
3. Add additive `cluster_job_leases` columns for restore epoch, monotonic lease fence, capability generation/digest/expiry; preserve all non-backup lease rows with safe defaults.
4. Seed the singleton from `cluster_restore_state`, forcing the first HA backup.
5. Register migration v5, set `SchemaVersion=5`, and add the two new tables to the exact table set.
6. Run the Task 1 tests until GREEN, then run `git diff --check` and the targeted package test.
7. Commit: `feat(ha): add durable backup RPO schema`.

### Task 3: Implement durable state reads and fenced lease ownership

**Files:**
- Create: `backend/internal/controlplane/backup_rpo.go`
- Create: `backend/internal/controlplane/backup_rpo_test.go`
- Create: `backend/internal/controlplane/backup_rpo_integration_test.go`
- Modify: `backend/internal/controlplane/models.go` only if shared exported types are necessary

1. Write RED table tests for `Current`, `AcquireLease`, and `RenewLease` using `recordingRQLite`.
2. Require active `cluster_restore_state`, exact matching restore epoch, DB time via `unixepoch()`, live capability evidence, and job name `backup-rpo`.
3. A same-holder renewal preserves the fence; an expired holder takeover increments it; a live different holder conflicts; stale epoch/capability/token/fence conflicts.
4. Never replay a mutating request. Resolve an unknown acquire/renew outcome by one exact linearizable read keyed by job, holder, token, epoch, fence, and capability digest.
5. Parse all rqlite numbers fail-closed and return only redacted fixed errors.
6. Add real three-voter tests for takeover, expiry boundary, node handoff, capability expiry, restore activation, and unknown-outcome evidence.
7. Run focused unit tests locally if cheap; leave tagged rqlite integration/race/vet to GitHub Actions.
8. Commit: `feat(ha): add fenced backup RPO lease store`.

### Task 4: Implement atomic attempt and verified-generation transitions

**Files:**
- Modify: `backend/internal/controlplane/backup_rpo.go`
- Modify: `backend/internal/controlplane/backup_rpo_test.go`
- Modify: `backend/internal/controlplane/backup_rpo_integration_test.go`

1. Write RED tests for `RegisterAttempt`, `MarkUploadStarted`, `RecordUploadOutcome`, `AcknowledgeVerified`, and `SupersedeStaleAttempt`.
2. Burn `last_attempt_sequence` and insert the attempt in one transaction; allow only one non-terminal attempt for the current restore epoch.
3. Bind every transition to holder, token, fence, restore epoch, capability generation/digest/expiry, captured dirty generation, attempt sequence, backup ID, object key, and object digest.
4. Persist upload unknown distinctly. Never re-PUT an attempt after upload started; recovery may only inspect exact versions/read back.
5. Advance verified fields only after exact-version full readback and authenticated manifest v2 proof. Concurrent writes remain dirty because only `captured_generation` is acknowledged.
6. Reject latest/null/empty VersionId, ETag-as-digest, stale fence, wrong generation, wrong manifest, ambiguous outcome, malformed rows, and verification after lease expiry.
7. Resolve unknown database writes only through exact keyed linearizable reads; never replay them.
8. Add rqlite integration coverage for crash/restart phases, newer-fence supersession, concurrent dirty bump, and one-active-attempt constraint.
9. Commit: `feat(ha): persist exact backup attempt transitions`.

### Task 5: Add one transaction-local dirty-generation statement

**Files:**
- Modify: `backend/internal/controlplane/backup_rpo.go`
- Modify: `backend/internal/controlplane/backup_rpo_test.go`
- Modify: `backend/internal/controlplane/customers.go`
- Modify: `backend/internal/controlplane/customers_test.go`
- Modify: `backend/internal/controlplane/store.go`
- Modify: `backend/internal/controlplane/store_test.go`
- Modify: `backend/internal/controlplane/service.go`
- Modify: `backend/internal/controlplane/store_test.go`
- Modify: `backend/internal/controlplane/whitelist_store.go`
- Modify: `backend/internal/controlplane/whitelist_store_test.go`

1. Add a package-private helper returning a parameterized `rqlite.Statement` that increments the singleton only when an immediately preceding authoritative mutation proved `changes()>0` and the restore epoch is active/current.
2. Do not expose a standalone `MarkDirty(ctx)` method; there must be no commit window between business data and the watermark.
3. RED/GREEN wire the statement immediately after the authoritative mutation in `ClaimDevice`, `updateSetting`, `RevokeSessions`, and `ensureWhiteListEntitlement`.
4. Harden `RevokeSessions`: a missing principal must not create an audit-only success; its audit and session update must be gated by the committed principal epoch increment.
5. Test successful mutation increments once; failed CAS, limit rejection, missing principal, exact idempotent replay, and read-only calls increment zero times.
6. Preserve each method's existing public return/error behavior and unknown-outcome resolution.
7. Commit: `feat(ha): mark core mutations backup-dirty atomically`.

### Task 6: Cover outbox, delete, importer, and restore boundaries

**Files:**
- Modify: `backend/internal/controlplane/outbox.go`
- Modify: `backend/internal/controlplane/outbox_test.go`
- Modify: `backend/internal/controlplane/outbox_regression_test.go`
- Modify: `backend/internal/controlplane/outbox_delete.go`
- Modify: `backend/internal/controlplane/outbox_delete_test.go`
- Modify: `backend/internal/controlplane/restore_epoch.go`
- Modify: `backend/internal/controlplane/restore_epoch_test.go`
- Modify: `backend/internal/importer/rqlite_store.go`
- Modify: `backend/internal/importer/rqlite_store_test.go`

1. RED/GREEN add exactly one gated bump to `UpsertDesired`, new `RecordApplyReceipt`, `PurgeTombstone`, `CreateCustomerTombstone`, and `PermanentlyRetireNodeService`.
2. Make exact desired-state replay a true no-op while preserving exact conflict detection and result evidence.
3. Add one bump to a newly committed importer batch and one to a newly completed import run; already-applied receipts/runs do not bump.
4. In `AdvanceAfterRestore`, invalidate leases, clear/supersede the active attempt, bind the singleton to the new epoch, and leave it dirty in the same transaction. Activation itself does not mark business data dirty.
5. Explicitly prove that node/backup lease churn, health canary, `CreateSession`, derived `ReconcileNode`, and importer `BeginOrResume` do not bump.
6. Add three-voter integration tests for v4-to-v5 upgrade, restore epoch handoff, importer unknown outcomes, and no-op/CAS boundaries.
7. Commit: `feat(ha): complete transactional backup dirty coverage`.

### Task 7: Implement the exact-version Yandex Object Storage adapter

**Files:**
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Create: `backend/internal/backuprpo/object_store.go`
- Create: `backend/internal/backuprpo/yandex_s3.go`
- Create: `backend/internal/backuprpo/yandex_s3_test.go`
- Create: `backend/internal/backuprpo/testdata/` only for synthetic non-secret fixtures

1. Add the minimum pinned AWS SDK for Go v2 modules required for config, credentials, and S3; do not add a general cloud abstraction.
2. Define a worker port with `CheckVersioning`, `PutImmutable`, `GetExact`, and `ReconcileUnknownPut`. Do not expose delete to the worker.
3. Require `GetBucketVersioning=Enabled` before lease acquisition and again before PUT/readback. Treat absent, Suspended, errors, empty VersionId, and literal `null` as capability loss.
4. Put an attempt-unique bound key with Content-MD5 for transport integrity plus immutable metadata for SHA-256, size, generation, sequence, backup ID, manifest version, and fence. ETag is diagnostic only.
5. Persist and read only the exact returned VersionId. Stream full readback through size limit and SHA-256; pass authenticated bundle verification separately.
6. On unknown PUT, list object versions with both continuation markers. Adopt only one exact-key candidate whose metadata, length, full digest, and authenticated manifest match; zero/multiple/mismatch remain unknown and dirty.
7. Reject delete markers, foreign keys, `IsLatest` evidence, unbounded pagination, response/body leakage, and credential-bearing errors.
8. Use synthetic fake-S3 unit tests for all status, pagination, ambiguity, timeout, version, metadata, and digest boundaries. No real Yandex API call occurs in tests.
9. Commit: `feat(ha): add exact-version Yandex backup adapter`.

### Task 8: Wire a resumable one-shot backup worker

**Files:**
- Create: `backend/internal/backuprpo/runner.go`
- Create: `backend/internal/backuprpo/runner_test.go`
- Create: `backend/internal/backuprpo/bundle_creator.go`
- Create: `backend/internal/backuprpo/bundle_creator_test.go`
- Create: `backend/cmd/maestro-backup-worker/main.go`
- Create: `backend/cmd/maestro-backup-worker/main_test.go`
- Modify: `ops/ha/backup-rqlite.sh`
- Modify: `ops/ha/tests/test_backup_worker.py`
- Modify: `ops/ha/tests/test_backup_worker_security.py`

1. Add RED runner tests covering clean/no-op, dirty create/upload/readback/ack, upload unknown reconciliation, crash after each durable phase, concurrent write after capture, stale fence, capability loss, and bounded retry exit.
2. Parse a strict versioned config containing only endpoint identities, TLS/credential file paths, bucket/prefix, signer/recipient fingerprints, timeouts, and size limits. Reject inline secrets, unknown keys, insecure files, public endpoints, unsafe prefixes, and unbounded limits.
3. Reuse the existing authenticated manifest-v2 bundle contract. Extend `backup-rqlite.sh` with a worker-only mode that writes one candidate into the secure runtime directory; preserve drill-only behavior and existing tests.
4. Open and pin the produced encrypted bundle before hashing/upload. Reject symlinks, owner/mode changes, inode replacement, unexpected files, oversize bundles, and secret-bearing output.
5. Run one bounded state-machine cycle: capability proof, fenced lease, exact durable resume decision, create only when safe, mark upload started before network I/O, PUT once, exact readback, offline authenticated verification, then acknowledgment.
6. On every ambiguous result leave RPO dirty and exit with a fixed redacted code. Never blind-retry a mutating rqlite request or an upload-started object PUT.
7. Add cross-contract tests showing the Go runner transition matrix agrees with `ops/ha/backup_worker.py` for all public phases and failure codes.
8. Commit: `feat(ha): wire resumable production backup worker`.

### Task 9: Enforce legacy/HA systemd exclusivity without deployment

**Files:**
- Create: `deploy/ha/maestro-backup.service`
- Create: `deploy/ha/maestro-backup.timer`
- Modify: `deploy/maestro-backup.service`
- Modify: `deploy/maestro-backup.timer`
- Modify: `deploy/maestro-backup-onchange.path`
- Modify: `deploy/maestro-backup.sh`
- Create: `ops/ha/test-backup-systemd-policy.py`
- Create: `ops/ha/tests/test_backup_systemd_policy.py`

1. Write RED policy tests requiring bidirectional `Conflicts=`/ordering across HA and all legacy units.
2. Require the legacy script to exit successfully in rqlite mode before reading JSON/database state, opening SSH, creating an archive, invoking GPG/AWS, or pruning.
3. Give the HA service `RuntimeDirectoryMode=0700`, `UMask=0077`, strict sandboxing, bounded timeouts, no shell expansion, protected credential-file inputs, and fixed config/runtime paths.
4. Require the cutover policy to prove legacy service/timer/path are stopped, disabled, and masked before HA enable. `Conflicts=` alone is not sufficient.
5. Keep all units disabled/inert in the repository; do not run `systemctl`, copy files to `/etc`, or access credentials.
6. Run Python policy tests and `bash -n` only.
7. Commit: `feat(ha): add exclusive backup unit templates`.

### Task 10: Extend GitHub gates and repository documentation

**Files:**
- Modify: `.github/workflows/ha-control-plane.yml`
- Modify: `.github/workflows/ha-dr-restore-drill.yml`
- Modify: `.github/workflows/yandex-cdn-release.yml`
- Modify: `ops/ha/README.md`
- Modify: `docs/superpowers/plans/2026-08-09-maestrovpn-ha-04-operations-cutover.md` only to link this focused plan and evidence
- Modify: `CONTEXT_HANDOFF.md`

1. Add path filters and explicit formatting scopes for `internal/backuprpo`, `cmd/maestro-backup-worker`, `deploy/ha`, and the policy tests. Do not silently reformat accepted legacy debt.
2. Add unit, race, vet, Python contract, shell syntax, systemd policy, and isolated three-voter integration gates to both HA workflows; keep the Yandex isolated release NO-GO and artifact-only.
3. Document config fields without values, least-privilege Yandex actions, exact VersionId semantics, RPO health boundary of 60 minutes, manual provisioning requirements, cutover proof, rollback, and why ETag/latest are rejected.
4. Update the handoff with exact commits/run IDs only after GitHub completes. Preserve production baseline `1.0.157` and state explicitly that no production mutation occurred.
5. Commit: `ci(ha): gate durable backup production adapters`.

### Task 11: Independent review and exact-SHA verification

**Files:**
- Review all files changed by Tasks 1-10
- Modify only defects proven by review or failing evidence

1. Run an independent spec-compliance review against the parent design, this plan, `MASTER_REQUIREMENTS.md`, and the offline worker contract.
2. Run an independent code-quality/security review focused on SQL gates, unknown outcomes, restore epochs, fence races, secret redaction, object-version ambiguity, bundle pinning, systemd privileges, and ordinary VPN non-regression.
3. Fix findings one at a time with RED tests first and separate guarded attempts.
4. Run lightweight local syntax/format/diff checks only. Push the canonical branch once.
5. Wait for exact-SHA GitHub runs: HA control plane, HA DR restore drill, and Yandex CDN isolated release. Require every required job to pass on the same commit SHA.
6. Record workflow/job IDs and artifact metadata in `CONTEXT_HANDOFF.md`, commit/push the docs-only evidence update, and verify local/GitHub branch heads match.
7. Do not merge, tag, release, enable systemd, migrate production, upload to Yandex, deploy, canary, or cut over. Those remain later explicitly authorized gates.

## Completion evidence for this focused plan

- Migration v5 upgrades an exact v4 prefix and verifies on a real three-voter isolated rqlite cluster.
- Every listed authoritative mutation dirties the RPO singleton in the same transaction; proved no-ops do not.
- Lease and attempt transitions remain fail-closed across expiry, restore, crash, handoff, stale fence, and unknown outcomes.
- The object adapter requires bucket versioning and exact non-null VersionId; exact-version full readback is authenticated before acknowledgment.
- Runtime has no delete capability and systemd templates are mutually exclusive but not enabled.
- HA, DR, and Yandex isolated-release workflows are green on one exact SHA.
- Protected local files remain untouched; production `1.0.157`, ordinary VPN, 3x-ui/Xray, billing, OTA, Yandex resources, and credentials remain unchanged.
