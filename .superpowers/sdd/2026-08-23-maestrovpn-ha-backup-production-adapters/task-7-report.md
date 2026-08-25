# Task 7 Report: Exact-Version Yandex Object Storage Adapter

**Date:** 2026-08-25  
**Baseline application version:** `1.0.157` (unchanged)  
**Task 7 parent/report context:** `65c5a676`  
**Task 7 code head:** `a2655a67729d6843b040c0ab3253514c3149f0ef`

## Scope

Task 7 completes the exact-version Yandex Object Storage adapter defined in `docs/superpowers/plans/2026-08-23-maestrovpn-ha-backup-production-adapters.md`: the narrow backup-worker object-store port, minimum pinned AWS SDK v2 dependencies, strict bucket-versioning capability gates, immutable attempt-unique upload, exact opaque `VersionId` readback, bounded unknown-PUT reconciliation, and comprehensive synchronized fake-S3 coverage.

This closeout adds only this report. It does not change application code, plans, progress tracking, manifests, workflows, release state, production infrastructure, or cutover state.

## Implementation

- `backend/internal/backuprpo/object_store.go` defines only the backup-worker port required by the binding plan: `CheckVersioning`, `PutImmutable`, `GetExact`, and `ReconcileUnknownPut`. It exposes no delete operation and introduces no general cloud abstraction.
- `backend/internal/backuprpo/yandex_s3.go` constructs the adapter with static credentials, a validated HTTPS Yandex-compatible endpoint, explicit region and bucket, rejected redirects, fixed redacted errors, and `RetryMaxAttempts=1`.
- Bucket versioning is required to be exactly `Enabled` before the worker can proceed and again before PUT/readback. Missing or suspended versioning, request errors, an empty returned version, and literal `null` are fail-closed capability loss.
- Uploads use an attempt-unique bounded key and mandatory `If-None-Match: *`. Payloads are limited to 1 GiB, prehashed for MD5 and SHA-256, rewound before upload, and sent with Content-MD5 plus exactly seven immutable metadata fields: `maestro-sha256`, `size-bytes`, `captured-generation`, `attempt-sequence`, `backup-id`, `manifest-version`, and `lease-fence`.
- The adapter persists the exact opaque `VersionId` returned by PUT. Exact readback supplies both key and version, bounds and streams the body, proves length and SHA-256, closes the body on every path, and invokes the authenticated-manifest verifier as a separate bounded verification step. ETag remains diagnostic only.
- Unknown-PUT reconciliation lists versions with both continuation markers, `MaxKeys=100`, and hard bounds of 10 pages/1,000 entries. It adopts only one exact-key, exact-version candidate after metadata, length, complete digest, and authenticated-manifest verification all succeed.
- Reconciliation fails closed for zero or multiple valid candidates, delete markers, foreign keys, empty/literal-null versions, pagination ambiguity, timeouts, body-close failures, and malformed responses. `IsLatest` is ignored entirely as non-evidence: `true`, `false`, and absent values follow the same complete verification path.
- The fake S3 implementation is synchronized for race-safe tests and records the immutable PUT precondition. Tests are synthetic and make no real Yandex API request.

## TDD Evidence

### RED history

- Comprehensive fake-S3 tests were written before production implementation. The first focused run failed because the Task 7 worker port, constructor, adapter behavior, and required AWS modules did not yet exist.
- Initial status/version tests exposed the missing strict `Enabled` gate, empty and literal-`null` `VersionId` rejection, exact-version request behavior, redirect rejection, bounded payload handling, fixed redacted errors, and response-body closure requirements.
- Upload tests exposed the missing complete prehash/rewind contract, Content-MD5 and SHA-256 proofs, exact seven-field metadata contract, attempt-unique keying, and immutable PUT precondition coverage.
- Unknown-PUT tests first exposed ambiguity and pagination boundaries: both continuation markers, foreign keys, delete markers, empty versions, multiple matches, response mismatches, timeouts, bounded page/entry traversal, and authenticated-manifest verification all had to remain fail closed.
- Independent review added a tri-state regression for `IsLatest`; the prior rejection of `true` failed even though a real single-version attempt key can legitimately be latest.
- Review-driven fake coverage exposed that `IfNoneMatch="*"` was not captured and asserted by the fake, so the production precondition was not contract-locked by the test harness.
- Explicit adversarial pagination regressions covered a repeated marker pair, a multi-step marker/version cycle, a stalled single marker with the other changing, and duplicate `VersionId` observations across pages. These cases had to remain unknown within the fixed call bound.

### GREEN history

- The focused fake-S3 suite now proves constructor/configuration validation, static credential use without leakage, redirect rejection, one-attempt retry behavior, every bucket-versioning state, and all empty/literal-null version boundaries.
- Immutable upload coverage proves the 1 GiB limit, prehash and rewind behavior, Content-MD5, SHA-256, `IfNoneMatch="*"`, attempt-unique keys, exact metadata field set and values, exact opaque returned version, and fail-closed error redaction.
- Exact readback coverage proves explicit version selection, bounded streaming, content length and digest verification, separately bounded authenticated-manifest verification, fail-closed timeout handling through fixed redacted errors, and body closure on success and failure.
- Reconciliation coverage proves one-candidate adoption only after full verification and unknown results for zero/multiple/mismatched candidates, delete markers, foreign keys, duplicate versions, cyclic/stalled pagination, pagination limits, timeouts, and body-close failures.
- The `IsLatest` table now proves that `true`, `false`, and absent values take the identical full verification path and produce the same adoption result when all authoritative evidence matches.
- The synchronized fake and adapter pass the focused/default relevant tests locally under the official Go 1.25 toolchain with `GOMAXPROCS=1` and `GOMEMLIMIT=512MiB`; race and vet authority is provided by the exact-SHA GitHub gates below.

## Contract Coverage

1. **Minimum SDK surface:** the only new direct AWS SDK v2 requirements are `github.com/aws/aws-sdk-go-v2/config v1.32.38`, `github.com/aws/aws-sdk-go-v2/credentials v1.19.37`, and `github.com/aws/aws-sdk-go-v2/service/s3 v1.107.3`; no general cloud abstraction was added.
2. **Narrow worker port:** the worker receives only `CheckVersioning`, `PutImmutable`, `GetExact`, and `ReconcileUnknownPut`; delete is not exposed.
3. **Versioning gates:** `GetBucketVersioning` must report exactly `Enabled` before lease acquisition and before PUT/readback; absent, suspended, errored, empty-version, and literal-`null` states lose capability.
4. **Immutable PUT:** the adapter uses an attempt-unique bounded key, `If-None-Match: *`, Content-MD5, SHA-256, exact size/generation/sequence/backup/manifest/fence metadata, and treats ETag only as diagnostic data.
5. **Exact version readback:** only the exact returned opaque `VersionId` is persisted and fetched; the full bounded body is streamed through size/SHA-256 checks and a separate authenticated-bundle verifier.
6. **Unknown-PUT reconciliation:** both continuation markers are honored; adoption requires exactly one exact-key/version candidate with matching metadata, length, full digest, and authenticated manifest, while zero/multiple/mismatch remains unknown and dirty.
7. **Fail-closed evidence:** delete markers, foreign keys, `IsLatest` as evidence, unbounded or cyclic pagination, response/body leakage, credential-bearing errors, and ambiguous results cannot produce adoption.
8. **Synthetic verification only:** status, pagination, ambiguity, timeout, version, metadata, digest, body-close, and race boundaries are exercised through fake S3 only; no real Yandex call occurred.
9. **Binding commit:** the implementation commit is exactly `a2655a67729d6843b040c0ab3253514c3149f0ef` with title `feat(ha): add exact-version Yandex backup adapter`.

## Independent Review Corrections

- Removed `IsLatest` as a rejection/adoption gate. All three representable values now receive the same authoritative key/version, metadata, length, digest, and manifest verification.
- Extended the synchronized fake to capture `IfNoneMatch` and added a mandatory assertion for the exact value `*`.
- Added bounded regressions for repeated marker pairs, multi-step marker/version cycles, one-marker stalls, and duplicate `VersionId` values across pages; every ambiguous traversal remains unknown without exceeding the configured bounds.
- Preserved the narrow worker boundary, exact seven metadata fields, exact opaque-version contract, 1 GiB limit, fixed redacted errors, and fake-only external boundary while applying the review corrections.

Final independent review result:

- **Specification compliance:** PASS.
- **Code quality:** APPROVED.
- **Findings:** zero Critical, Important, or Minor findings.

## Final Gate Closure

### Local verification

- Focused `backuprpo` fake-S3 tests: PASS under official Go 1.25 with `GOMAXPROCS=1` and `GOMEMLIMIT=512MiB`.
- Full/default relevant `backuprpo` coverage: PASS.
- Review regressions for `IsLatest`, immutable `IfNoneMatch`, pagination cycles/stalls, and duplicate version IDs: PASS.
- Go formatting for the three authorized Go source files: clean; `backend/go.mod` and `backend/go.sum` were verified through the resolved module graph and `go mod verify`.
- Final diff validation: PASS; protected pre-existing Task 4/`normalize.patch` work remained outside Task 7.
- No live object-store, Yandex, release, OTA, server, or production check was run because Task 7 explicitly requires fake S3 only.

### GitHub verification at exact SHA

Verified SHA: `a2655a67729d6843b040c0ab3253514c3149f0ef`.

- HA run `32818256386`: job `97710846880` (`Go and isolated rqlite`) succeeded with all 26/26 reported steps successful.
- Release run `32818256348`: all five jobs and all 50/50 reported steps succeeded:
  - job `97710846845`, `format-unit`: 11/11;
  - job `97711022506`, `rqlite-purge`: 10/10;
  - job `97711022598`, `race-vet`: 8/8;
  - job `97711022890`, `offline-replay`: 8/8;
  - job `97711291531`, `android-test-apk`: 13/13, including build, metadata/signer validation, and artifact upload.

Both workflows were push runs, attempt 1, and resolved to the exact Task 7 SHA. No failed job log existed, and monitoring performed no repository, release, or production mutation.

## Safety and Version

- Application version remains exactly `1.0.157`.
- Task 7 changed only the five authorized implementation/dependency files:
  - `backend/go.mod`;
  - `backend/go.sum`;
  - `backend/internal/backuprpo/object_store.go`;
  - `backend/internal/backuprpo/yandex_s3.go`;
  - `backend/internal/backuprpo/yandex_s3_test.go`.
- No `testdata` directory was needed; all fake-S3 fixtures remain synthetic and non-secret.
- No real Yandex API call, workflow edit, production call, server/client mutation, cutover, release, tag, signing, OTA, or protected-file mutation occurred.
- The implementation plan, `progress.md`, manifests, protected Task 4 reports, and `normalize.patch` were not modified.
- This report is the only closeout file created and is intentionally left uncommitted and unpushed per instruction.

## Commits

Exact Task 7 transition:

1. `65c5a676` — parent/report-head context before Task 7 implementation.
2. `a2655a67729d6843b040c0ab3253514c3149f0ef` — `feat(ha): add exact-version Yandex backup adapter` (Task 7 code head).

No report-only commit or push was created.

## Remaining Scope

The remaining implementation begins at **Task 8** in the binding production-adapters plan. No Task 8 behavior, production integration, deployment, or cutover work was folded into Task 7.
