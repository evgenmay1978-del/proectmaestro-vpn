# Task 4 report: exact backup attempt transitions

Date: 2026-08-23

Status at review correction: implementation commit `7e0a7039a995cca6c1f3127a7505347f969ba0b3` is pushed; two independent-review corrections and bounded local verification are complete; replacement exact-SHA CI is pending.

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
- Exact-SHA GitHub Actions owns race, vet, and real three-voter integration evidence.

## Risks

- Real rqlite SQL semantics remain gated on a new exact-SHA HA integration job; local evidence is focused/full controlplane unit coverage plus tagged compile.
- The prior exact-SHA Linux HA run is red at the real rqlite integration step. Because that runtime was not reproduced locally, this report makes no causal claim that either review correction explains or resolves the CI failure.
- The aggregate Windows unit suite continues to have the unrelated platform-sensitive failures noted above.

## Exact-SHA CI closeout

- Implementation commit: `7e0a7039a995cca6c1f3127a7505347f969ba0b3`; the remote SHA was verified exact after push.
- GitHub Actions run `32664247546`, job `97255074775` (`Go and isolated rqlite`) failed at step 12, `Test rqlite integration`.
- The real three-voter runtime failure was not reproduced locally. The review-correction commit must be pushed and evaluated by a new exact-SHA HA run before CI can be called green.
