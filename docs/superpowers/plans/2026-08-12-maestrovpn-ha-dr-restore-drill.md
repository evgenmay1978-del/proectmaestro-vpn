# MaestroVPN HA DR Restore Drill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove an authenticated encrypted rqlite backup, strict empty-cluster restore, monotonic restore epoch and stale-writer fencing using only synthetic GitHub Actions infrastructure.

**Architecture:** The existing mandatory-mTLS three-node harness produces a canonical DELETE-mode SQLite image through the rqlite backup API. Standard-library Python validates an exact offline manifest, shell boundaries perform ephemeral GPG sign/encrypt/decrypt and a fresh-cluster load, and typed Go control-plane code atomically advances and activates the restore epoch while invalidating restored leases. A dedicated GitHub workflow proves success, adversarial rejection, parity and cleanup without production secrets or artifacts.

**Tech Stack:** Go 1.25, Python 3 standard library, Bash, GnuPG, SQLite, rqlite 10.1.0, OpenSSL test PKI, GitHub Actions Ubuntu 24.04.

## Global Constraints

- Work only on `codex/ha-rqlite-task2` and draft PR #82; work GitHub-first.
- The weak Windows computer runs only repetition guard and narrow Git/doc checks; no local Go build/test.
- Run `ops/maestro-repetition-guard.py check` before every GitHub mutation, workflow retry or long scan.
- Every behavior change has a pushed RED contract with exact failing SHA/run/job, then a separate GREEN implementation commit.
- Use only synthetic rows, keys, PKI, GPG identities and loopback endpoints.
- Do not connect to or mutate S1-S4, panels, Telegram bots, customers, VPN services, DNS, Android/TV, Release or OTA.
- Backup uses only `GET /db/backup?fmt=delete` over mandatory verified client mTLS; live SQLite/Raft directories are never canonical input.
- Restore accepts only a fresh empty three-node test cluster with no concurrent writers.
- Private directories are `0700`, files `0600`; links, traversal, ambiguous paths and existing output fail closed.
- Logs contain no endpoints, private paths, response bodies, identities, keys, payloads or markers.
- No backup, SQLite image, key, manifest or receipt is uploaded as an Actions artifact.
- An unexplained retry is not evidence. Production remains `NO-GO (repository DR implementation only)`.

---

### Task 1: Ordered migration 2 and monotonic restore epoch

**Files:**
- Create: `backend/internal/controlplane/migrations/0002_restore_epoch.sql`
- Create: `backend/internal/controlplane/restore_epoch.go`
- Create: `backend/internal/controlplane/restore_epoch_test.go`
- Modify: `backend/internal/controlplane/migrations.go`
- Modify: `backend/internal/controlplane/migrations_test.go`
- Modify: `backend/internal/controlplane/schema_constraints_test.go`

**Interfaces:**
- Produces: `RestoreState`, `NewRestoreEpochStore(rqlite.RQLite)`, `Current(context.Context)`, `AdvanceAfterRestore(context.Context, int64, string)`, `Activate(context.Context, int64)`.
- Produces ordered immutable migrations 1 and 2, `SchemaVersion == 2`, and a combined schema identity.
- Consumes existing node/job leases and Telegram poller lease/fence fields.

- [x] **Step 1: Add RED migration and restore-state contracts**

~~~go
func TestAdvanceAfterRestoreRaisesEpochAndInvalidatesEveryLease(t *testing.T) {
    db := newRestoreRecorder(t, restoreFixture{
        Epoch: 7, Activated: true, NodeLeases: 1, JobLeases: 1,
        PollerToken: "synthetic-old-token", PollerFence: 11,
    })
    state, err := NewRestoreEpochStore(db).AdvanceAfterRestore(
        context.Background(), 7, strings.Repeat("a", 64),
    )
    if err != nil { t.Fatal(err) }
    if state.RestoreEpoch != 8 || state.Activated { t.Fatalf("state=%#v", state) }
    requireAtomicLeaseInvalidation(t, db, 8, 12)
}

func TestAdvanceAfterRestoreRejectsStaleEpochWithoutMutation(t *testing.T)
func TestAdvanceAfterRestoreResolvesUnknownOutcomeByExactRead(t *testing.T)
func TestActivateRequiresExactUnactivatedEpoch(t *testing.T)
func TestCurrentRejectsMissingDuplicateOrMalformedRestoreState(t *testing.T)
~~~

The recorder requires one transaction: epoch CAS, delete node/job leases, clear
Telegram poller owner/token/expiry, increment its fence. Every dependent
statement contains the exact successful epoch-CAS gate.

- [x] **Step 2: Push RED and record the exact intended failure**

Commit `test(controlplane): require monotonic restore epoch`. Expected failure:
migration 2 and restore APIs are undefined; no unrelated failure counts.

- [x] **Step 3: Implement ordered immutable migrations**

Use:

~~~go
type migration struct {
    Version int
    Path string
    Data []byte
    Checksum string
    Statements []rqlite.Statement
}
~~~

`Apply` rejects gaps or unknown checksums and atomically applies each missing
migration. `VerifyIdentity` requires exact versions 1..2 and hashes canonical
JSON `[{version,checksum},...]`.

Migration 2 creates:

~~~sql
CREATE TABLE cluster_restore_state (
 singleton_id INTEGER PRIMARY KEY CHECK(singleton_id=1),
 cluster_id TEXT NOT NULL CHECK(length(cluster_id)=64),
 restore_epoch INTEGER NOT NULL CHECK(restore_epoch>0),
 restored_from_backup_sha256 TEXT
  CHECK(restored_from_backup_sha256 IS NULL OR length(restored_from_backup_sha256)=64),
 activated INTEGER NOT NULL CHECK(activated IN (0,1)),
 created_at_unix INTEGER NOT NULL CHECK(created_at_unix>=0),
 activated_at_unix INTEGER
)
~~~

Seed one generated lowercase 64-hex cluster ID, epoch 1, active, without a
backup digest.

- [x] **Step 4: Implement fail-closed epoch transitions**

`AdvanceAfterRestore` accepts a positive expected epoch and canonical backup
SHA-256. One non-retried linearizable transaction sets epoch + 1 inactive,
records the digest, deletes node/job leases, clears Telegram poller lease
ownership and increments poller fences. Resolve unknown outcome only by one
exact linearizable state read. `Activate` is an exact inactive-to-active CAS
and idempotent only for the same epoch.

- [x] **Step 5: Run GREEN and commit**

~~~bash
cd backend
go test ./internal/controlplane -run 'TestMigrations|TestRestore|TestAdvance|TestActivate|TestCurrent' -count=1
go test -race ./internal/controlplane -run 'TestRestore|TestAdvance|TestActivate|TestCurrent' -count=1
go vet ./internal/controlplane
~~~

Commit `feat(controlplane): add monotonic restore epoch`.

---

### Task 2: Canonical offline backup manifest verifier

**Files:**
- Create: `ops/__init__.py`
- Create: `ops/ha/__init__.py`
- Create: `ops/ha/tests/__init__.py`
- Create: `ops/ha/verify_backup.py`
- Create: `ops/ha/tests/test_verify_backup.py`
- Create: `ops/ha/tests/fixtures/backup-manifest-v1.json`

**Interfaces:**
- Produces `build_manifest(image_path, keys_path, metadata) -> dict`.
- Produces `verify_bundle(directory, trusted_signer_fingerprint, gpg_home) -> dict`.
- Produces CLI `python -m ops.ha.verify_backup build|verify ...`.

- [x] **Step 1: Add RED strict manifest tests**

~~~python
class VerifyBackupTests(unittest.TestCase):
    def test_round_trip_manifest_is_canonical_and_image_derived(self): ...
    def test_rejects_unknown_or_missing_manifest_field(self): ...
    def test_rejects_missing_extra_duplicate_link_or_traversal_member(self): ...
    def test_rejects_wrong_hash_size_signature_or_signer(self): ...
    def test_rejects_schema_checksum_count_or_receipt_drift(self): ...
    def test_rejects_sqlite_integrity_or_foreign_key_failure(self): ...
    def test_output_and_errors_exclude_synthetic_markers_and_paths(self): ...
~~~

Use only complete synthetic fixtures. Inject the GPG command runner; raw GPG
output is never printable.

- [x] **Step 2: Push RED**

Commit `test(ha): require authenticated backup manifest`. Expected failure:
`ops.ha.verify_backup` is absent.

- [x] **Step 3: Implement canonical parsing and image-only inspection**

Strict JSON rejects duplicate/unknown/missing keys and noncanonical encoding.
Exact archive basenames are `control-plane.sqlite3`,
`application-keys.json`, `manifest.json`, `manifest.sig`. Open SQLite via
URI `mode=ro&immutable=1`; require `PRAGMA integrity_check`, empty
`foreign_key_check`, schema identity, restore epoch, sorted table counts,
import/batch receipt and backup-watermark high-watermarks derived only from the
image.

- [x] **Step 4: Implement signature and secrecy gates**

Run GPG with private `0700` home, `--batch --no-tty --status-fd`; require
exactly one `VALIDSIG` matching the expected full fingerprint. Success output
is exactly `{"format_version":1,"status":"verified"}`. Failures use fixed
codes and marker scans precede success.

- [x] **Step 5: Run GREEN and commit**

~~~bash
python -m unittest ops.ha.tests.test_verify_backup
python -m py_compile ops/ha/verify_backup.py ops/ha/tests/test_verify_backup.py
~~~

Commit `feat(ha): verify canonical backup manifests`.

---

### Task 3: Authenticated encrypted backup creator

**Files:**
- Create: `ops/ha/backup-rqlite.sh`
- Create: `ops/ha/test-backup-rqlite.sh`
- Create: `ops/ha/tests/create-synthetic-dr-identity.sh`
- Modify: `ops/ha/ci-rqlite-cluster.sh`
- Modify: `ops/ha/test-ci-rqlite-cluster.sh`

**Interfaces:**
- Produces `backup-rqlite.sh --drill --cluster-root ROOT --keys FILE --output FILE --signer FPR --recipient FPR`.
- Produces an encrypted `.tar.gpg` only after independent decrypt-and-verify.
- Consumes the validated mTLS harness and Task 2 verifier.

- [x] **Step 1: Add RED shell contracts**

Tests reject missing `--drill`, roots outside `RUNNER_TEMP`, links, broad
permissions, HTTP, missing client certificate, existing output, `set -x`,
unsafe cleanup and missing commands. Require `fmt=delete`, timeouts, disabled
redirects, same-filesystem temp, deterministic tar, detached signature,
encryption and post-encryption verification.

- [x] **Step 2: Push RED**

Commit `test(ha): require fail-closed encrypted backup`. Expected failure:
creator and synthetic identity helper are absent.

- [x] **Step 3: Add safe harness metadata boundary**

Add `describe-mtls --output FILE`: write one no-clobber `0600` JSON file
inside the marked root containing three loopback HTTPS voters and relative
CA/client cert/key names. Reject plain mode, stdout, existing/outside paths.
Never expose server or CA private keys.

- [x] **Step 4: Implement backup creation**

Install traps first. Download a bounded DELETE-mode image to `0600`, validate
SQLite header, build manifest, detach-sign it, create exact deterministic tar,
encrypt to the ephemeral recipient, decrypt to a second private directory and
run Task 2 verification before atomic no-clobber publication. Delete only
validated owned temporaries on failure.

- [x] **Step 5: Run GREEN and commit**

~~~bash
bash -n ops/ha/backup-rqlite.sh ops/ha/tests/create-synthetic-dr-identity.sh
bash ops/ha/test-backup-rqlite.sh
bash ops/ha/test-ci-rqlite-cluster.sh
python -m unittest ops.ha.tests.test_verify_backup
~~~

Commit `feat(ha): create authenticated encrypted rqlite backup`.

---

### Task 4: Strict empty-cluster restore loader

**Files:**
- Create: `ops/ha/restore-rqlite.sh`
- Create: `ops/ha/test-restore-rqlite.sh`
- Create: `ops/ha/restore_api.py`
- Create: `ops/ha/tests/test_restore_api.py`

**Interfaces:**
- Produces `restore-rqlite.sh --drill --cluster-root ROOT --bundle FILE --signer FPR --gpg-home DIR`.
- Produces `restore_api.inspect_empty(config) -> bool` and `restore_api.load_sqlite(config, path) -> None`.
- Consumes Tasks 2-3 and a new mandatory-mTLS harness cluster.

- [x] **Step 1: Add RED restore and HTTP contracts**

~~~python
def test_inspect_empty_requires_three_exact_https_voters_and_mtls(): ...
def test_load_uses_post_db_load_octet_stream_without_redirect_or_retry(): ...
def test_transport_failure_is_unknown_outcome_and_never_replayed(): ...
def test_http_wrong_ca_missing_client_and_oversize_fail_closed(): ...
~~~

Shell tests require verification before inspection, inspection before load,
one load only, non-empty/prior-attempt rejection and unconditional cleanup.

- [x] **Step 2: Push RED**

Commit `test(ha): require fresh-cluster restore gate`. Expected failure:
restore loader/API are absent.

- [x] **Step 3: Implement strict restore API**

Use Python `ssl` and `http.client`, TLS 1.2 minimum, hostname verification
and exact CA/client material. Drill config permits exactly loopback S2/S3/S4.
Reject redirects, Basic Auth, HTTP, oversized responses/images. `inspect_empty`
strong-reads absence of schema/business state; `load_sqlite` sends one
`POST /db/load` with `application/octet-stream`. Ambiguous transport outcome
returns a fixed code and is never retried.

- [x] **Step 4: Implement restore orchestration**

Strict order: private extract, GPG decrypt, Task 2 offline verification, exact
empty proof, no-clobber restore-attempt marker, one load, strong schema/count
readback. Any failure requires a new harness cluster. Stop before epoch advance
and activation, which Task 1 Go code owns.

- [x] **Step 5: Run GREEN and commit**

~~~bash
python -m unittest ops.ha.tests.test_restore_api ops.ha.tests.test_verify_backup
python -m py_compile ops/ha/restore_api.py ops/ha/tests/test_restore_api.py
bash -n ops/ha/restore-rqlite.sh
bash ops/ha/test-restore-rqlite.sh
~~~

Commit `feat(ha): restore only into a fresh mtls cluster`.

---

### Task 5: Real restored-cluster fencing and parity

**Files:**
- Create: `backend/internal/controlplane/restore_epoch_integration_test.go`
- Create: `backend/cmd/maestro-import/dr_fixture_integration_test.go`
- Modify: `backend/internal/importer/rqlite_store_integration_test.go`

**Interfaces:**
- Produces build-tagged `TestPrepareSyntheticDRSource`,
  `TestAdvanceRestoredEpochAndFence`, `TestVerifyRestoredBusinessParity`.
- Consumes mTLS cluster, exact importer binary, signed receipt and redacted metadata.

- [x] **Step 1: Add RED integration proof**

~~~go
func TestAdvanceRestoredEpochAndFence(t *testing.T) {
    before := readRestoreState(t)
    seedSyntheticOldLeases(t, before.RestoreEpoch)
    after := advanceAfterRestore(t, before, expectedBackupSHA256())
    if after.RestoreEpoch <= before.RestoreEpoch || after.Activated {
        t.Fatalf("after=%#v", after)
    }
    requireOldNodeJobAndPollerLeasesRejected(t, before.RestoreEpoch)
    requireInactiveEpochRejectsSyntheticMutation(t, after.RestoreEpoch)
    activateExactEpoch(t, after.RestoreEpoch)
    requireOldEpochMutationRejected(t, before.RestoreEpoch)
    requireCurrentEpochMutationExactlyOnce(t, after.RestoreEpoch)
}
~~~

Parity requires exact schema identity, counts, import/batch evidence, business
digest and shadow match, allowing only restore state, lease invalidation and
activation audit differences.

- [x] **Step 2: Push RED**

Commit `test(ha): require restored epoch fencing and parity`. Expected failure:
epoch proof wiring is absent.

- [x] **Step 3: Implement source/destination coordination**

Write only `0600` redacted metadata under `RUNNER_TEMP`: source business
digest, signed receipt digest, backup SHA and source epoch. After restore,
connect through all three voters, advance epoch, prove inactive rejection,
reconcile synthetic desired state, activate and prove current-epoch exact-once.

- [x] **Step 4: Strengthen crash and quorum boundaries**

Terminate isolated cases after verification, after load and before activation;
each uses a new destination. Reuse of root, lease or epoch fails. After
activation one voter loss preserves exact strong state; two-voter loss rejects
writes.

- [x] **Step 5: Run GREEN and commit**

~~~bash
cd backend
go test -tags=rqlite_integration ./internal/controlplane -run '^TestAdvanceRestoredEpochAndFence$' -count=1
go test -tags=rqlite_integration ./cmd/maestro-import -run 'TestPrepareSyntheticDRSource|TestVerifyRestoredBusinessParity' -count=1
~~~

Commit `test(ha): prove restored epoch fencing and parity`.

---

### Task 6: Dedicated exact-SHA GitHub DR workflow

**Files:**
- Create: `.github/workflows/ha-dr-restore-drill.yml`
- Create: `ops/ha/test-dr-workflow-policy.py`
- Modify: `ops/ha/README.md`

**Interfaces:**
- Produces named repository-only CI gates and exact run/job evidence.
- Consumes Tasks 1-5, pinned rqlite 10.1.0 and ephemeral runner keys.

- [x] **Step 1: Add RED workflow-policy test**

Require pinned 40-hex actions, `contents: read`, no environment/production
secrets, Ubuntu 24.04, bounded timeouts, non-cancelling concurrency, no artifact
upload and unconditional cleanup.

- [x] **Step 2: Push RED**

Commit `test(ha): require isolated dr workflow policy`. Expected failure:
workflow is absent.

- [x] **Step 3: Wire exact named gates**

Order: unit/race/vet/syntax; source mTLS cluster; exact importer/source; ephemeral
GPG identity; backup and independent verification; tamper/wrong-key/signer
matrix; fresh destination; non-empty rejection in a separate cluster; one load;
epoch advance/fence/reconcile/activate; digest/shadow/receipt parity; duplicate
no-op; one-voter/no-quorum; marker scan; always cleanup.

- [x] **Step 4: Run exact full GREEN**

Require all named steps success on one exact code SHA. Fetch step list and
failed logs only. No backup artifact may exist.

- [x] **Step 5: Commit workflow proof**

Commit `ci(ha): prove authenticated empty-cluster restore`.

---

### Task 7: Scope, secrecy and durable handoff

**Files:**
- Modify: `docs/superpowers/plans/2026-08-12-maestrovpn-ha-dr-restore-drill.md`
- Modify: `CONTEXT_HANDOFF.md`

**Interfaces:**
- Produces final exact-SHA/run/job evidence and the read-only server audit gate.
- Consumes Tasks 1-6.

- [x] **Step 1: Run final scope and secret audit**

Compare baseline `ffa3f7f2a0c88b2c754a1949a72daa2d686a49bf` to final code.
Require no `app/**`, `deploy/**`, production service/address/password/token,
customer identity, DNS, OTA, SSH, `InsecureSkipVerify` or production endpoint.
Only loopback and `synthetic.invalid` negative fixtures are allowed.

- [x] **Step 2: Verify all exact evidence**

~~~bash
cd backend
go test ./...
go test -race ./...
go vet ./...
cd ..
python -m unittest ops.ha.tests.test_verify_backup ops.ha.tests.test_restore_api
bash -n ops/ha/backup-rqlite.sh ops/ha/restore-rqlite.sh
bash ops/ha/test-backup-rqlite.sh
bash ops/ha/test-restore-rqlite.sh
python ops/ha/test-dr-workflow-policy.py
~~~

The exact workflow also proves real mTLS backup/restore/fencing and cleanup.

- [x] **Step 3: Update plan and handoff**

Record exact SHA, run, job and conclusions. State:

~~~text
NO-GO (repository DR implementation only)
S1-S4, panels, bots, customers, VPN protocols, Android/TV, Release and OTA unchanged.
Next gate: read-only S2/S3/S4 readiness audit; no installation or restart.
~~~

- [x] **Step 4: Final documentation commit**

Commit `docs(ha): record authenticated restore proof`.

## Plan acceptance

- Backup is obtained only through mandatory-mTLS rqlite API and verified before target inspection.
- Restore cannot run on non-empty or previously attempted state; ambiguous load discards destination.
- Epoch advancement invalidates node/job/Telegram leases before activation.
- Old/inactive epoch mutations fail; current epoch commits exactly once.
- Restored business digest, signed receipt evidence and redacted shadow match.
- One voter may fail; no quorum rejects writes.
- No production system/customer is touched and no sensitive artifact is uploaded.
- Repository GREEN does not authorize deployment.
