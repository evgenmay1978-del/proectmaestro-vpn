# Task 4 report: exact backup attempt transitions

Date: 2026-08-23

Status at exact-SHA GREEN closeout: isolation-fix commit `bfbaca288947f29743eefe09c9fbe803fe39e56e` passed the complete HA workflow in run `32673823686`, job `97278614026`; implementation, review corrections, authenticated RED diagnosis, and final three-voter evidence are complete.

## Scope and compatibility

- Extended the existing `BackupRPOStore` API in `backend/internal/controlplane/backup_rpo.go`; no parallel store or lease identity was introduced.
- Preserved the canonical `backup-rpo` job name, database `unixepoch()` time authority, strict row parsing, fixed redacted errors, linearizable transactions, and one exact evidence read after an unknown database outcome.
- Kept migration v5 frozen. The existing attempt phases, composite identity, and verified tuple constraints are consumed without schema or migration changes.
- The review correction replaces raw string VersionId inputs in the unshipped Task 4 API with the opaque `BackupRPOVersionID` constructor boundary. Stored/read attempt and verified object-version fields remain strings, and no production consumer outside this store was changed.
- It does not alter workflow YAML, production configuration, deployment, release, OTA, tags, merges, or version `1.0.157`.

## TDD evidence

RED was established before production implementation with the focused transition suite:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test ./internal/controlplane -run 'TestBackupRPO(RegisterAttempt|MarkUploadStarted|RecordUploadOutcome|AcknowledgeVerified|SupersedeStaleAttempt|AttemptTransition)' -count=1
```

The RED result was the expected compile failure: the new attempt identity, proof/outcome types, phase constants, and transition methods were undefined.

GREEN used the same focused command and passed:

```text
ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.048s
```

The tagged integration source also compiles with the prescribed Go 1.25 toolchain:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test -tags rqlite_integration ./internal/controlplane -run '^$' -count=1
```

```text
ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.054s [no tests to run]
```

The full backend unit command was also run under the same one-core/512 MiB limits:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test ./... -count=1
```

`internal/controlplane` passed. The aggregate command failed only in unrelated Windows-sensitive suites: `cmd/maestro-import` fixture/runtime checks, `internal/applyagent` directory `fsync`, and `internal/vkturnconf` POSIX `0600` mode assertions.

## Independent review corrections (2026-08-24)

Independent review required two corrections.

1. Applied attempts retain a non-null VersionId. The original supersession expectation implicitly required null evidence, so both a known supersession result and the one-read committed-unknown reconciliation rejected a successfully superseded applied attempt. The correction gives every transition an explicit version expectation: exact, must be null, or preserve null/any strictly valid VersionId. Supersession uses preserve mode while still binding the complete attempt identity, phase, and `stale-fence` failure code.
2. A raw string boundary could confuse VersionId and ETag provenance. The correction accepts upload/verification inputs only through the opaque `BackupRPOVersionID` constructor, defensively rejects uppercase, quoted, and multipart ETag shapes, and permits a legitimate opaque 32-character lowercase-hex VersionId. Stored rows are parsed with the same exact-version validator.

The supersession RED test was:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test ./internal/controlplane -run '^TestBackupRPOSupersedeStaleAttemptPreservesAppliedVersionKnownAndUnknown$' -count=1
```

Before the production correction, the known branch returned the fixed unavailable error and the committed-unknown branch returned the fixed unresolved-outcome error. The same test passed after the tri-state expectation was implemented.

The VersionId-boundary RED test was:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test ./internal/controlplane -run '^TestBackupRPOVersionIDAcceptsOpaqueLowerHexAndRejectsETagShapes$' -count=1
```

It failed to compile with the expected undefined `BackupRPOVersionID`, `NewBackupRPOVersionID`, and `VersionID` API. After the minimal implementation, the combined review suite passed:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test ./internal/controlplane -run '^(TestBackupRPOSupersedeStaleAttemptPreservesAppliedVersionKnownAndUnknown|TestBackupRPOVersionIDAcceptsOpaqueLowerHexAndRejectsETagShapes|TestBackupRPORecordUploadOutcomeRejectsAmbiguousVersionProvenance)$' -count=1
```

```text
ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.058s
```

Fresh bounded verification after correction:

- `go test ./internal/controlplane -count=1`: passed in 0.081s.
- `go test -tags rqlite_integration ./internal/controlplane -run '^$' -count=1`: passed in 0.043s with no tests run.

## Exact transition proof

- `RegisterAttempt` performs one linearizable two-statement transaction. It compare-and-swaps `last_attempt_sequence` from exactly `attempt_sequence-1`, requires the captured dirty generation, activated restore epoch, exact live lease/capability tuple, and absence of any existing non-terminal attempt; the insert is conditional on `changes()=1`.
- Every attempt mutation binds restore epoch, attempt sequence, captured generation, backup ID, object key, object digest, object size, manifest version, adapter contract, capability generation/digest/expiry, lease holder, lease token, and lease fence.
- Upload start is one-way from `pending` to `applying`. A durable `unknown` upload outcome cannot transition back to `applying`, so it never authorizes another PUT.
- Upload outcome persistence distinguishes `applying -> unknown` with a null object version from `applying|unknown -> applied` with a strict, non-sentinel, non-ETag exact VersionId.
- Verification requires exact VersionId, complete readback, matching digest and byte count, and authenticated manifest v2 fields matching the entire captured object tuple.
- Acknowledgement updates only `verified_generation=captured_generation`. If `dirty_generation` advanced concurrently, the state remains `dirty` while the exact captured generation is acknowledged.
- Stale non-terminal attempts can be superseded only by an exact live lease with a strictly newer fence in the same restore epoch; the durable failure code is `stale-fence`.

## Unknown database outcome proof

Each transition makes one mutating `Request` and never replays it. When rqlite reports an unknown outcome, the store makes exactly one linearizable query for the complete composite identity plus expected phase, object version, and failure code. Registration evidence additionally proves the sequence is burned; acknowledgement evidence additionally proves the complete verified state tuple. Missing, multiple, malformed, or mismatched evidence returns the fixed redacted unresolved-outcome error.

Unit coverage asserts one mutation/one evidence read and no replay. Tagged integration coverage persists `unknown`, reconstructs the store between phases, rejects a second upload start, reconciles the exact VersionId, acknowledges through a concurrent dirty-generation increase, rejects a second active attempt, and exercises newer-fence supersession.

## Self-review

- Cross-checked the parser lifecycle against frozen migration v5 constraints.
- Verified supersession preserves a null or strictly valid applied VersionId in both direct results and the single committed-unknown evidence read.
- Verified the typed VersionId boundary rejects zero/forged inputs before any database call and rejects uppercase, quoted, and multipart ETag shapes without excluding legitimate opaque lowercase-hex VersionIds.
- Cross-checked SQL predicates and argument lists for all identity fields and exact live-lease predicates.
- Verified transaction-local `changes()=1` coupling for registration and acknowledgement.
- Verified all timestamps are supplied by SQLite `unixepoch()` and no caller clock was added.
- Verified no migration, workflow, deployment, release, OTA, tag, merge, or version file changed.
- Verified the pre-existing modified 2026-08-20 Task 4 report and untracked `normalize.patch` remain untouched and will not be staged.

## Skipped or CI-owned checks

- Local real three-voter rqlite integration was not run on the weak Windows PC.
- Local race and vet runs were not run.
- Exact-SHA GitHub Actions run `32673823686`, job `97278614026`, supplied the race, vet, and real three-voter integration evidence.

## Risks

- Real rqlite SQL semantics, race, vet, importer parity, shadow parity, and three-node mTLS/importer behavior passed the final exact-SHA HA job.
- The authenticated RED was isolated to tagged-test state contamination and corrected without changing production transition behavior or frozen schema assertions.
- The aggregate Windows unit suite continues to have the unrelated platform-sensitive failures noted above; Linux exact-SHA HA is authoritative for this closeout.

## Exact-SHA CI closeout

- Implementation commit: `7e0a7039a995cca6c1f3127a7505347f969ba0b3`; the remote SHA was verified exact after push.
- GitHub Actions run `32664247546`, job `97255074775` (`Go and isolated rqlite`) failed at step 12, `Test rqlite integration`.
- The real three-voter runtime failure was not reproduced locally. The review-correction commit must be pushed and evaluated by a new exact-SHA HA run before CI can be called green.

## Exact-SHA CI diagnostic instrumentation (2026-08-24)

- Review-correction commit `a714be9b67945c983755049cc5b24e66e2e5069f` was pushed and its remote SHA was verified exact.
- HA run `32668813584`, job `97266333388` (`Go and isolated rqlite`), again failed at step 12, `Test rqlite integration`.
- All preceding unit, race, vet, harness, and cluster-start steps passed. Public check annotations exposed only generic exit code 1; public logs were unavailable.
- The real three-voter failure was not reproduced locally, so no production defect or causal hypothesis is asserted.

The tagged Task 4 integration tests now emit one GitHub `::error` workflow-command annotation only after a failure. The annotation contains a fixed case token, the exact operation/assertion stage, and one safe linearizable fingerprint: `last_sequence`, current-restore `max_sequence`, and whether the exact expected composite attempt row exists. It emits no raw row, SQL text, error, lease token, object key, digest, endpoint, or credential. Fingerprint collection uses a fresh bounded five-second context so an expired test context still leaves an exact stage annotation.

The diagnostic helper RED command was:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test -p=1 -tags rqlite_integration ./internal/controlplane -run '^TestBackupRPOIntegrationFailureMessageIsStageSpecificAndSafe$' -count=1
```

After correcting only a malformed test literal, the intended RED was the undefined `backupRPOIntegrationFailureMessage` helper. The same focused command passed GREEN:

```text
ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.041s
```

The complete rqlite-tagged controlplane test source compiled without starting a cluster:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test -p=1 -tags rqlite_integration ./internal/controlplane -run '^$' -count=1
```

```text
ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.048s [no tests to run]
```

No workflow YAML, production implementation, schema, migration, deploy/release/OTA/tag/version file, protected 2026-08-20 report, or `normalize.patch` was changed. The authenticated diagnostic RED and final exact-SHA GREEN are recorded below.

## Authenticated exact-SHA isolation diagnosis and correction (2026-08-24)

The authenticated HA job log for diagnostic commit `f8effd2e8d4138256b1493f8b7bb7c3cb9d959a3`, run `32672174692`, job `97274544281`, proves that the Task 4 transition cases themselves passed. The later frozen-schema cases failed as follows:

- `TestBackupRPOSchemaFreezesDurableColumnsAndSeed`, `schema_constraints_test.go:919`: `backup RPO seed = []`.
- `TestBackupRPOSchemaRejectsInconsistentStateAndUnfencedAttempts`, `schema_constraints_test.go:1111`: statement 0 failed with `UNIQUE constraint failed: backup_rpo_attempts.restore_epoch, backup_rpo_attempts.attempt_sequence`.

This is cross-test state contamination, not a production transition failure. The prior tagged integration cleanup removed the synthetic `backup-rpo` lease but left the singleton state verified/mutated and retained append-only attempt `(restore_epoch,attempt_sequence)` identities used later by schema-contract tests.

Migration v5 remains frozen and its append-only trigger is not bypassed. Each Task 4 integration test now prepares a synthetic restore epoch at or above `1000000`, choosing one greater than every persisted attempt epoch. Its cleanup removes the synthetic lease and restores the exact migration seed visible to later tests: restore epoch `1`, dirty generation `1`, verified generation `0`, null verified tuple, last attempt sequence `0`, and phase `dirty`. Persisted attempts remain durable evidence under their isolated high restore epochs and cannot collide with schema tests at epochs `1` and `2` or with later Task 4 cases.

The isolation contract RED command was:

```powershell
$env:GOMAXPROCS='1'; $env:GOMEMLIMIT='512MiB'; & 'C:\Users\User\Documents\Codex\2026-08-21\webcmd-plugin-webcmd-openai-curated-remote\task15-go125-gofmt-6084f26\full-extracted\go\bin\go.exe' test -p=1 -tags rqlite_integration ./internal/controlplane -run '^TestBackupRPOIntegrationIsolationUsesUniqueEpochAndRestoresMigrationSeed$' -count=1
```

RED was the expected undefined `backupRPOIntegrationPrepareStatements`, `backupRPOIntegrationCleanupStatements`, and `backupRPOIntegrationRestoreEpochFloor`. After the minimal tagged-test helper correction, the same command passed:

```text
ok github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane 0.054s
```

Additional bounded local evidence:

- `go test -p=1 ./internal/controlplane -count=1`: passed in `0.069s`.
- `go test -p=1 -tags rqlite_integration ./internal/controlplane -run '^$' -count=1`: passed in `0.049s` with no tests run.

The real three-voter suite was not rerun locally. No production implementation, migration, schema assertion, workflow YAML, deploy/release/OTA/tag/version file, protected 2026-08-20 report, or `normalize.patch` was changed.

## Final exact-SHA GREEN closeout (2026-08-24)

- Implementation commit `7e0a7039a995cca6c1f3127a7505347f969ba0b3` introduced the exact durable attempt transitions.
- Review-correction commit `a714be9b67945c983755049cc5b24e66e2e5069f` fixed both independent findings: applied-attempt supersession now preserves null or strictly valid VersionId evidence in known and committed-unknown paths, and the API uses an opaque VersionId boundary with defensive ETag-shape rejection.
- Diagnostic commit `f8effd2e8d4138256b1493f8b7bb7c3cb9d959a3` produced authenticated RED run `32672174692`, job `97274544281`. Its Task 4 transitions passed; the exact later failures were the missing migration seed at `schema_constraints_test.go:919` and composite attempt collision at `schema_constraints_test.go:1111`.
- Isolation-fix commit `bfbaca288947f29743eefe09c9fbe803fe39e56e` moved synthetic Task 4 attempts into fresh high restore epochs and restored the exact epoch-1 migration seed during cleanup without deleting append-only attempt evidence.
- Replacement HA run `32673823686`, job `97278614026`, is GREEN for formatting, Python contracts, backend unit, race, vet, harness, isolated rqlite integration, importer parity, shadow parity, three-node mTLS/importer, and cleanup.

The final exact-SHA evidence closes the Task 4 transition scope GREEN. No production/deploy/release/OTA/tag/merge/version `1.0.157` action was performed.
