# Task 6 Report: Transactional Backup Dirty Coverage

**Date:** 2026-08-25  
**Baseline application version:** `1.0.157` (unchanged)  
**Task 6 code head:** `0905714f3b4ce6c9bf18ddab18520d00eaf61918`

## Scope

Task 6 completes the transactional backup-dirty boundary work defined in `docs/superpowers/plans/2026-08-23-maestrovpn-ha-backup-production-adapters.md`: outbox and deletion mutations, importer commit boundaries, restore-epoch handoff, exact replay/CAS behavior, and shared three-voter integration coverage.

This closeout adds only this report. It does not change application code, plans, progress tracking, manifests, workflows, release state, production infrastructure, or cutover state.

## Implementation

- `backend/internal/controlplane/outbox.go` and `outbox_delete.go` now make each covered business mutation and its backup dirty-generation bump one atomic rqlite transaction. The covered mutations are `UpsertDesired`, new `RecordApplyReceipt`, `PurgeTombstone`, `CreateCustomerTombstone`, and `PermanentlyRetireNodeService`.
- Desired-state exact replay is a true no-op, while mismatched generations still preserve conflict detection and result evidence.
- Customer tombstone creation uses the schema-valid terminal state `deleted`. Its authoritative customer CAS gates the frozen target set, desired rows, outbox rows, tombstone, and one dirty bump. Replays, zero eligible targets, and desired-generation conflicts cannot commit partial state.
- `backend/internal/importer/rqlite_store.go` bumps once for a newly committed importer batch and once for a newly completed run. If a committed-unknown request is later proven newly committed, that original mutation still contributes exactly one bump; resolution and subsequent replay add zero further bumps. Already-applied receipts/runs remain no-ops.
- `backend/internal/controlplane/restore_epoch.go` performs the restore handoff atomically: it advances the cluster epoch, proves and rebinds the backup singleton to the new epoch, leaves backup state dirty, clears/supersedes the active attempt, and invalidates relevant leases. A singleton mismatch rolls back the whole handoff, and committed-unknown resolution proves the full postcondition rather than only the cluster epoch.
- Activation itself remains backup-neutral. Tests also keep node/backup lease churn, health canaries, `CreateSession`, derived `ReconcileNode`, and importer `BeginOrResume` backup-neutral.
- Test-only integration support provides exact populated-v4-to-v5 migration proof, restore/importer/no-op/CAS coverage, and cluster-scoped crash-safe coordination for shared live-rqlite packages.

## TDD Evidence

### RED history

- Focused boundary tests first exposed missing or non-atomic dirty bumps, replay mutations, importer committed-unknown ambiguity, and incomplete restore postcondition checks.
- Current-schema review tests exposed the invalid tombstone state `disabled`, replay propagation to a newly eligible target, restore singleton mismatch partial commit, zero-target tombstone partial commit, and conflicting desired-generation partial propagation.
- The first v4-to-v5 integration proof exposed that the fixture did not represent a populated exact v4 prefix with v5 absent.
- GitHub materialized-agent validation exposed legacy intentionally noncanonical formatting changed by Go 1.25 gofmt.
- Live DR fixture validation exposed `TestPrepareSyntheticDRSource` failing with `maestro-import: apply failed`: the target was non-empty because the preservation proof deliberately retained v4 proof rows, while `Migrator.Apply` had already applied v5, so the later importer apply conflicted with those proof rows.
- Shared-cluster review exposed unsafe cleanup ownership and a fragile constant temporary-directory lock. Subsequent adversarial tests exposed renewal committed-unknown handling, transient renewal, fail-stop timing, endpoint identity, crash takeover, DR bypass, and helper-process synchronization boundaries.
- GitHub HA run `32785949172`, job `97617803655`, failed in step 12 (`Test rqlite integration`) at `TestBackupRPOSchemaFreezesDurableColumnsAndSeed`: the shared-cluster seed query returned `[]` after a preceding purge correctly changed `dirty_generation` from 1 to 2.
- The final focused SQLite regression `TestBackupRPOMigrationSeedProofIsFreshAfterPurgeMutationSQLite` initially failed with `contaminated state=(dirty=0 seeds=0), want (2,0)`. The fixture clock had not crossed the production 90-day retention boundary; correcting only the test clock produced the intended purge state.

### GREEN history

- Actual-schema tests now cover dirty, verified, inactive, epoch-mismatched, exact replay/no-op, CAS-conflict, zero-target, and late-eligible-target states with exact row and generation assertions.
- Restore tests prove rollback on singleton mismatch and full committed-unknown postcondition resolution across cluster epoch, backup singleton, attempts, and leases.
- Importer tests prove exactly one bump when a batch or run is newly committed, including when committed-unknown resolution proves that original commit; the resolution step and every replay add zero further bumps, and already-complete work remains a no-op.
- The populated exact-v4 fixture now applies only the remaining migrations v5 and v6 and proves preserved data plus their seed/default contracts; the DR source remains importable.
- Cluster coordination tests cover two helper processes, exclusion across multiple TTLs, transient-renew recovery, committed-unknown renewal resolution, fail-stop/lost ownership, hanging HTTP renewal before the safety deadline, crash takeover, endpoint/cluster identity, exact DR bypass, cleanup ownership CAS, and synchronized helper output/wait handling.
- The seed invariant now runs the exact current migrations in a fresh in-memory SQLite database. An order-dependent regression proves the shared purge state is `(dirty=2, seed_count=0)` while the fresh migrated state is `(epoch=1, dirty=1, verified=0, last=0, phase=dirty, seed_count=1)`.

## Contract Coverage

1. **Exactly one gated bump per covered control-plane mutation:** `UpsertDesired`, new `RecordApplyReceipt`, `PurgeTombstone`, `CreateCustomerTombstone`, and `PermanentlyRetireNodeService` mutate and bump atomically.
2. **Replay and conflict semantics:** exact desired-state replay is a true zero-row/zero-bump no-op; mismatched state retains conflict and result evidence.
3. **Importer boundaries:** each newly committed batch and newly completed run bumps once; already-applied receipts/runs and resolved replays bump zero times.
4. **Restore handoff:** `AdvanceAfterRestore` atomically invalidates leases, clears/supersedes the active attempt, binds the singleton to the new epoch, and leaves backup dirty; activation itself does not dirty backup state.
5. **Backup-neutral operations:** node/backup lease churn, health canary, `CreateSession`, derived `ReconcileNode`, and importer `BeginOrResume` are executable zero-bump cases.
6. **Three-voter coverage:** live integration covers populated v4-to-v5 migration, restore handoff, importer unknown outcomes, exact no-op behavior, CAS conflicts, and shared-cluster isolation.
7. **Binding feature commit:** the principal implementation commit is `2b8993d94a0b3f706d0ee5ddc0ee97b4da9e8e2b` (`feat(ha): complete transactional backup dirty coverage`); follow-up commits contain only verified corrections and test isolation required to close the same contract.

## Independent Review Corrections

- Replaced the schema-invalid tombstone status with `deleted` and added real-schema proof.
- Made exact tombstone replay a true no-op with frozen targets and transaction-local authoritative CAS gating.
- Added in-transaction abort/postcondition gates so zero eligible targets or a conflicting desired generation roll back every tombstone-side mutation.
- Made restore advancement fail atomically on backup-singleton epoch mismatch and strengthened committed-unknown resolution to the complete handoff state.
- Rebuilt the migration fixture as a genuinely populated v4 prefix with v5 absent and preserved all preexisting rows while seeding v5 defaults.
- Restored only the pre-existing intentional noncanonical formatting required by the materialized-agent boundary; production semantics were unchanged.
- Corrected the DR fixture by removing the conflicting proof rows through exact asserted cleanup after their preservation had been proved; the already-migrated v5 target then remains valid and importable.
- Replaced broad shared-state cleanup with owned baseline/post-mutation CAS cleanup and replaced the constant directory lock with cluster/environment-specific, crash-safe rqlite-backed coordination.
- Made renewal retry transient failures, resolve committed-unknown by exact holder/token/expiry evidence, bound every request by the remaining conservative safety window, and fail-stop before ownership can expire.
- Removed cross-test seed dependence by proving migration seed/defaults in a fresh authoritative schema while retaining an executable contaminating-order regression.

Final independent review result:

- **Specification compliance:** PASS.
- **Code quality:** APPROVED.
- **Findings:** zero Critical, Important, or Minor findings.

## Final Gate Closure

### Local verification

- Focused exact SQLite regression: PASS (`backend/internal/controlplane`, 0.491s).
- Full control-plane default suite: PASS (9.516s; repeated relevant run 8.794s).
- Relevant untagged packages: PASS — `controlplane` 8.794s, `importer` 0.190s, `rqlite` 0.156s.
- `controlplane` with `rqlite_integration`: compile-only PASS.
- Tagged `internal/testsupport/rqliteintegrationlock` suite: PASS (21.465s).
- `git diff --check`: PASS; only the pre-existing protected Task 4 report produced an EOL warning.
- LF-normalized Go 1.25 gofmt mirrors for Task 6 files: clean.
- A local Windows `go test ./...` remained unsuitable as an all-package authority because of pre-existing filesystem mode/fsync differences in `maestro-import`, `applyagent`, and `vkturnconf`; the relevant Task 6 packages passed locally and the complete Linux GitHub gates below passed.

### GitHub verification at exact SHA

Verified SHA: `0905714f3b4ce6c9bf18ddab18520d00eaf61918`.

- HA run `32792655326`: job `97637236188` (`Go and isolated rqlite`) succeeded with all 26/26 reported steps successful, including the rqlite integration suite, isolated three-node mTLS suite, and production importer coverage.
- Release run `32792655357`: all five jobs and all 50/50 reported steps succeeded:
  - job `97637236202`, `format-unit`: 11/11;
  - job `97637424954`, `offline-replay`: 8/8;
  - job `97637424960`, `rqlite-purge`: 10/10;
  - job `97637424967`, `race-vet`: 8/8;
  - job `97637685468`, `android-test-apk`: 13/13, including build, metadata/signer validation, and artifact upload.

No failed log existed in the final runs, and monitoring performed no repository, release, or production mutation.

## Safety and Version

- Application version remains exactly `1.0.157`.
- No workflow, manifest, Android release, OTA, server deployment, credential, live Yandex API, production, or cutover mutation was made for Task 6 or its closeout.
- The final GitHub actions were verification-only; no release or cutover was executed.
- Protected `normalize.patch`, both protected Task 4 reports, the implementation plan, and `progress.md` were not modified.
- This report is the only closeout file created, and it is intentionally left uncommitted and unpushed per instruction.

## Commits

Exact first-parent chain from the Task 5 parent through the Task 6 code head:

1. `7bb7b69323b15768d56207ff68b61036236918af` — `docs(ha): close task 5 dirty-generation gate` (parent/baseline).
2. `2b8993d94a0b3f706d0ee5ddc0ee97b4da9e8e2b` — `feat(ha): complete transactional backup dirty coverage`.
3. `a0fb3f5ef0fb41c33a34798927b01d21a8c368d1` — `fix(ha): preserve materialized agent boundary`.
4. `e50956a70eba5f427affe06aad2421881a3cf6e7` — `test(ha): keep v4 upgrade fixture importable`.
5. `bd44538d0658b1ff8b3229c59efb40d2125ba495` — `test(ha): isolate shared rqlite integration state`.
6. `0905714f3b4ce6c9bf18ddab18520d00eaf61918` — `test(ha): isolate backup seed schema proof` (Task 6 code head).

No report-only commit or push was created.

## Remaining Scope

The remaining implementation begins at **Task 7: Implement the exact-version Yandex Object Storage adapter**. Task 7 owns the minimum pinned AWS SDK v2 dependencies, the narrow backup-worker object-store port, strict bucket-versioning capability checks, immutable attempt-unique PUT plus exact VersionId readback, bounded unknown-PUT reconciliation, and synthetic fake-S3 coverage. None of that Task 7 scope was started or folded into Task 6.
