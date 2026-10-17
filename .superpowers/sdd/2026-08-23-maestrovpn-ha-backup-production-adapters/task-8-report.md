# Task 8 Report: Resumable Production Backup Worker

**Date:** 2026-08-26
**Baseline application version:** `1.0.157` (unchanged)
**Task 8 parent/report context:** `39cd0ff7571570af629213e085a5c59d097434bc`
**Binding feature commit:** `8590abadcacb3b6293ea351aa6345872c985062e`
**Exact reviewed and tested code head:** `8cd34f28e4d71156b41aaf97d821313ec1fbe7e2`

## Scope

Task 8 completes the resumable one-shot backup worker defined in `docs/superpowers/plans/2026-08-23-maestrovpn-ha-backup-production-adapters.md`: strict production configuration, secure manifest-v2 bundle creation, pinned encrypted-candidate validation, one bounded durable state-machine cycle, exact-version upload/readback/authentication, ambiguous-outcome preservation, and Go/Python transition parity.

The review-driven closeout also makes verified-candidate removal crash-safe and retryable. This report and the handoff update are documentation-only; the authoritative code and CI evidence remain the exact code SHA above.

## Implementation

- `backend/internal/backuprpo/runner.go` executes one bounded cycle: capability proof, fenced lease acquisition, durable resume decision, safe candidate creation, durable upload-start transition before network I/O, one immutable PUT, exact-version readback, offline authenticated verification, durable acknowledgment, then safe local candidate removal.
- Clean RPO state exits without creating or uploading. Dirty state resumes from its exact durable phase; unknown upload outcomes reconcile instead of blind PUT retry. Stale fences, lost capability, concurrent post-capture writes, and every ambiguous outcome remain dirty and return fixed redacted errors.
- `backend/internal/backuprpo/bundle_creator.go` and the Linux runtime parse a strict versioned config containing only bounded endpoint identities, TLS/credential paths, bucket/prefix, signer/recipient fingerprints, timeouts, and size limits. Unknown keys, inline secrets, unsafe paths/prefixes, public endpoints, insecure files, and unbounded values are rejected.
- `ops/ha/backup-rqlite.sh` retains drill behavior and adds a worker-only path that creates exactly one manifest-v2 encrypted candidate in the secure runtime directory.
- Candidate handling pins descriptors before hashing/upload and verifies owner, mode, link count, device/inode identity, expected file set, size bounds, stable signer/verifier identity, and secret-free fixed output. Symlinks, hard links, replacement, unexpected files, unsafe modes, oversize bundles, or descriptor drift fail closed.
- Verified cleanup closes the pinned candidate first, validates the exact backup identity and safe directory shape, atomically renames to a no-clobber cleanup tombstone, performs ordered durable unlink/rmdir operations, and synchronizes the trusted root.
- Cleanup is idempotent but not optimistic: an interrupted or failed final root sync is retried on the next no-active cycle before any new candidate can be created. Unsafe source/tombstone residue is never deleted automatically.
- `backend/cmd/maestro-backup-worker` exposes the bounded worker entrypoint without adding deploy, daemon, release, or production-side effects.

## TDD Evidence

### RED history

- Runner tests were added first for clean/no-op, dirty create/upload/readback/ack, unknown-upload reconciliation, crash boundaries, concurrent writes, stale fences, capability loss, and bounded retry exits. The first happy/no-active runs proved cleanup was absent (`remove=0`).
- Bundle-creator RED coverage failed to compile until the narrow `RemoveExisting` lifecycle port existed.
- Linux RED coverage failed to compile until the production runtime implemented the removal contract and its strict filesystem validation.
- Independent review exposed that duplicating a directory descriptor shares its cursor; a regression showed a second enumeration could incorrectly appear empty. The runtime now opens `.` relative to the pinned descriptor to obtain an independent cursor.
- Final specification review exposed a durability gap after successful `rmdir`: when the final root `fsync` failed, a later absent-path retry could previously skip the sync. An injectable root-sync regression was RED before the retry contract was implemented.

### GREEN history

- Runner coverage proves exact ordering through acknowledgment, close-before-remove, evidence retention on acknowledgment failure, retryable post-ack cleanup failure without reupload, cleanup on no-active restart, and cleanup completion before any new create.
- Creator and Linux tests prove validation/redaction, idempotency, safe residue handling, symlink/hard-link/owner/mode/inode rejection, independent directory enumeration, ordered durability, and final-root-sync retry.
- Existing manifest-v2, shell worker, drill, configuration, state-machine, object-store, and cross-contract tests remain green.
- The official Go 1.25 toolchain passed focused Windows tests and Linux cross-compilation with constrained local resources; authoritative Linux, race/vet, rqlite, and Android evidence is recorded below.

## Contract Coverage

1. **Runner matrix:** clean/no-op, dirty progress, unknown reconciliation, all durable restart phases, concurrent write, stale fence, capability loss, and bounded exits are contract-tested.
2. **Strict configuration:** only the versioned bounded schema is accepted; inline secrets, unknown keys, public/insecure endpoints or files, unsafe prefixes, and unbounded limits are rejected.
3. **Manifest-v2 worker mode:** the existing authenticated shell contract produces exactly one candidate while drill-only behavior remains intact.
4. **Pinned candidate:** upload input is descriptor-pinned and checked for identity, owner/mode/link count, expected contents, size, verifier identity, replacement, and redacted output.
5. **Bounded durable cycle:** capability, fence, resume, create, mark-before-PUT, one PUT, exact readback, offline authentication, acknowledgment, and cleanup occur in the required order.
6. **Ambiguity safety:** dirty state and fixed redacted failure survive every ambiguous result; no mutating rqlite request or upload-started PUT is blindly retried.
7. **Cross-contract parity:** Go runner phases/failure codes remain synchronized with `ops/ha/backup_worker.py`.
8. **Binding commit:** `8590abadcacb3b6293ea351aa6345872c985062e` has the exact title `feat(ha): wire resumable production backup worker`.

## Independent Review Corrections

- Hardened restart capability checks and retained the exact pinned shell descriptor paths across creation and verification.
- Preserved dynamic verifier descriptors and bound the GPG agent home to the pinned identity boundary.
- Dereferenced `/proc/self/fd` paths for publish-device comparison while keeping the original pinned object authoritative.
- Exposed fixed redacted worker stages and safe-abort survivor evidence to end-to-end tests.
- Added verified-candidate cleanup, then corrected its directory-cursor isolation and final-root-sync retry semantics with regressions.

Final independent review at exact HEAD `8cd34f28e4d71156b41aaf97d821313ec1fbe7e2`:

- **Specification compliance:** PASS; zero actionable findings.
- **Code quality/security:** APPROVED; zero actionable findings.

## Final Gate Closure

### Local verification

- `go test -p=1 ./internal/backuprpo -count=1`: PASS under official Go 1.25 (`GOMAXPROCS=1`, `GOMEMLIMIT=512MiB`).
- Linux cross-compile test build (`GOOS=linux`, `GOARCH=amd64`, `CGO_ENABLED=0`, `-p=1`): PASS after the final cleanup durability change.
- Go formatting and `git diff --check`: clean.
- Protected pre-existing Task 4 work and `normalize.patch` remained untouched and outside every Task 8 commit.

### GitHub verification at exact SHA

Verified SHA: `8cd34f28e4d71156b41aaf97d821313ec1fbe7e2`.

- HA run `32999753382` (push, attempt 1): job `98278165821`, `Go and isolated rqlite`, completed successfully with 26/26 steps successful.
- Yandex isolated release run `33000187245` (manual `workflow_dispatch`, attempt 1) completed successfully with all five jobs and all 50/50 reported steps successful:
  - job `98279679095`, `format-unit`: 11/11;
  - job `98280283126`, `rqlite-purge`: 10/10;
  - job `98280283155`, `offline-replay`: 8/8;
  - job `98280283167`, `race-vet`: 8/8;
  - job `98281234848`, `android-test-apk`: 13/13.

The dispatch ran only the repository's isolated validation workflow. It did not merge, tag, sign, publish, release, deploy, upload a production backup, change OTA, or mutate production.

## Safety and Version

- Production Android/TV baseline remains exactly `1.0.157`; the template value in `version.properties` is not treated as the production baseline.
- No production rqlite cluster, S1-S4 server, Yandex bucket, credential, DNS, ordinary VPN user, billing path, release, tag, signing, OTA, systemd unit, deployment, canary, or cutover was accessed or changed.
- Heavy Linux, race/vet, real-rqlite-isolated, and Android checks ran in GitHub Actions. The weak local Windows computer was limited to focused tests, cross-compilation, formatting, diff, Git, and documentation work.
- `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-4-report.md` and `normalize.patch` remained protected and were never staged.
- This docs-only closeout does not replace the tested code SHA and does not authorize any production operation.

## Commits

Task 8 implementation and review-fix sequence:

1. `8590aba` — `feat(ha): wire resumable production backup worker`.
2. `01c7ab0` — `fix(ha): harden backup worker restart capability gate`.
3. `9e9e41a` — `fix(ha): preserve pinned shell descriptor paths`.
4. `c202449` — `test(ha): exercise production backup worker descriptors`.
5. `2c97f1b` — `fix(ha): retain dynamic verifier descriptors`.
6. `8174b2b` — `test(ha): expose redacted worker stage in e2e`.
7. `a0c5ba2` — `fix(ha): bind gpg agent home to pinned identity`.
8. `d3441da` — `fix(ha): dereference procfd for publish device check`.
9. `3f0df88` — `fix(ha): preserve drill verifier identity path`.
10. `06f3afb` — `test(ha): expose safe abort survivors`.
11. `4ae4216` — `fix(ha): clean extracted verifier payload`.
12. `05b938a` — `fix(ha): clean verified backup candidates`.
13. `8cd34f2` — `fix(ha): retry verified cleanup root sync` (exact reviewed/tested code head).

Interleaved `14f8c21` and `c91554d` are maintenance/documentation commits and do not broaden Task 8 runtime behavior.

## Remaining Scope

The next implementation is **Task 9: Enforce legacy/HA systemd exclusivity without deployment**. It is repository-only: add inert templates and policy tests, prove legacy early exit in rqlite mode, and run Python policy tests plus `bash -n`. No `systemctl`, `/etc` write, credential access, enablement, deployment, or cutover is authorized.
