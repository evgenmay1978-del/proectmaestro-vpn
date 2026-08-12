# MaestroVPN HA Production Import Factory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Wire a fail-closed, mandatory-mTLS production import factory with bound key/salt inputs and a resumable signed applied-run receipt, proven only against synthetic GitHub Actions infrastructure.

**Architecture:** The existing deterministic importer remains the only business writer. A command-owned runtime composition strictly validates all local protected material before network I/O, verifies rather than applies the immutable rqlite schema, constructs the existing RQLiteApplyStore, then signs evidence re-read from committed cluster rows. Snapshot protection metadata, schema identity and signed receipts are reusable typed boundaries rather than CLI-only maps.

**Tech Stack:** Go 1.25 standard library, AES-256-GCM and Ed25519, existing internal/rqlite client, SQLite/rqlite 10.1.0, Bash/OpenSSL test-only mTLS harness, GitHub Actions Ubuntu 24.04.

## Global Constraints

- Work only on branch codex/ha-rqlite-task2 and draft PR #82.
- Use GitHub App direct branch writes; do not perform local Go builds or heavy scans on the weak owner computer.
- Run ops/maestro-repetition-guard.py before every GitHub mutation, workflow retry or long scan.
- Every behavior change uses a pushed RED contract, exact failing GitHub SHA/run/job evidence, then a separate GREEN implementation commit.
- Use only synthetic keys, salts, identities and customer data in tests and GitHub Actions.
- Production main may become non-nil only after every local apply input is strict, bounded and fail-closed.
- Dry-run performs no network call and opens no protected apply input.
- Production rqlite configuration contains exactly S2, S3 and S4 HTTPS voters and mandatory verified client mTLS; no HTTP, TLS bypass, redirects, Basic Auth or default endpoints.
- Local key, salt, envelope and target-config validation completes before the first rqlite request.
- The importer calls Migrator.VerifyIdentity only; it never calls Migrator.Apply.
- Mutating rqlite requests are never transport-retried. Resume uses the same stable run ID and durable import batch receipts.
- Exit codes remain 0=clean, 2=plan blockers and 3=input/system.
- Errors, reports, receipts and job logs never expose endpoints, private paths, raw login/token/UUID/SubID, bot identity/token, salt/key/envelope/plaintext or rqlite response bodies.
- Completion remains NO-GO (repository implementation only); no live import, server deploy/restart, bot action, DNS/TLS production change, customer mutation, Android/TV edit, Release or OTA publication is authorized.
- Official rqlite HTTPS proof uses -http-cert, -http-key, -http-ca-cert and -http-verify-client as documented at https://rqlite.io/docs/guides/security/.

---

### Task 1: Versioned snapshot protection metadata

**Files:**
- Modify: backend/internal/importer/model.go
- Modify: backend/internal/importer/decoder.go
- Modify: backend/internal/importer/validate.go
- Modify: backend/internal/importer/digest.go
- Modify: backend/internal/importer/importer_test.go
- Modify: backend/internal/importer/resume_test.go
- Modify: backend/internal/importer/testdata/customers-valid.json
- Modify: backend/internal/importer/testdata/orders-pending-credited.json
- Modify: backend/internal/importer/testdata/collisions.json
- Modify: backend/internal/importer/testdata/bot-bindings-v1.json
- Modify: backend/internal/importer/testdata/settings-principals-v1.json
- Modify: backend/internal/importer/testdata/full-then-delta/base-full.json
- Modify: backend/internal/importer/testdata/full-then-delta/delta.json
- Modify: backend/internal/importer/testdata/full-then-delta/final-full.json

**Interfaces:**
- Produces: Snapshot.ClusterHMACKeySHA256 string and Snapshot.LegacyTrialSaltSHA256 string.
- Produces: matching ImportPlan fields included in Digest(plan).
- Consumes: canonical lowercase 64-character SHA-256 values and normalized snapshot format version 2.

- [ ] **Step 1: Add RED decoder and planner contracts**

Add these tests before changing production structs:

~~~go
func TestDecodeSnapshotV2RequiresClusterHMACKeyDigest(t *testing.T) {
    snapshot := decodeFixture(t, "customers-valid.json")
    snapshot.ClusterHMACKeySHA256 = ""
    encoded, err := json.Marshal(snapshot)
    if err != nil { t.Fatal(err) }
    if _, err := DecodeSnapshot(encoded); err == nil {
        t.Fatal("snapshot without cluster HMAC key digest was accepted")
    }
}

func TestPlanBindsTrialSaltDigestOnlyWhenTrialsExist(t *testing.T) {
    snapshot := decodeFixture(t, "customers-valid.json")
    snapshot.Trials = []LegacyTrial{{
        SourceKey: "trial-protection-v2",
        LegacyAnchorHMAC: strings.Repeat("1", 64),
        CurrentHMAC: strings.Repeat("2", 64),
        ExpiresAtUnix: 2_100_100,
    }}
    snapshot.LegacyTrialSaltSHA256 = strings.Repeat("a", 64)
    plan, report := Plan(snapshot, testPlanOptions())
    if len(report.Blockers) != 0 { t.Fatalf("blockers=%#v", report.Blockers) }
    if plan.LegacyTrialSaltSHA256 != snapshot.LegacyTrialSaltSHA256 {
        t.Fatalf("salt digest was not preserved")
    }

    snapshot.Trials = nil
    _, report = Plan(snapshot, testPlanOptions())
    if !hasBlockerCode(report.Blockers, "unexpected_legacy_trial_salt_digest") {
        t.Fatalf("blockers=%#v", report.Blockers)
    }
}

func TestProtectionMetadataChangesSourceAndPlanDigests(t *testing.T) {
    left := decodeFixture(t, "customers-valid.json")
    right := left
    right.ClusterHMACKeySHA256 = strings.Repeat("b", 64)
    leftPlan, _ := Plan(left, testPlanOptions())
    rightPlan, _ := Plan(right, testPlanOptions())
    if leftPlan.SourceDigest == rightPlan.SourceDigest ||
        leftPlan.PlanDigest == rightPlan.PlanDigest {
        t.Fatal("protection metadata was outside canonical digests")
    }
}
~~~

Also require DecodeSnapshot to reject format version 1, uppercase/nonhex digest,
a missing salt digest with nonempty Trials, and a salt digest with empty Trials.

- [ ] **Step 2: Push RED and verify exact failure**

Commit only tests and minimally changed test fixtures needed to compile:

~~~text
test(importer): require bound snapshot protection metadata
~~~

Expected GitHub failure: missing Snapshot and ImportPlan fields or decoder still
accepting version 1. No unrelated package failure counts as RED.

- [ ] **Step 3: Implement format version 2 and stable validation**

Add exact fields:

~~~go
type Snapshot struct {
    FormatVersion             int    `json:"format_version"`
    ClusterHMACKeySHA256      string `json:"cluster_hmac_key_sha256"`
    LegacyTrialSaltSHA256     string `json:"legacy_trial_salt_sha256,omitempty"`
    // existing fields remain unchanged
}

type ImportPlan struct {
    FormatVersion             int    `json:"format_version"`
    ClusterHMACKeySHA256      string `json:"cluster_hmac_key_sha256"`
    LegacyTrialSaltSHA256     string `json:"legacy_trial_salt_sha256,omitempty"`
    // existing fields remain unchanged
}
~~~

Decode only version 2. Require lowercase hex through one helper:

~~~go
func validCanonicalSHA256(value string) bool {
    if len(value) != 64 || value != strings.ToLower(value) { return false }
    decoded, err := hex.DecodeString(value)
    return err == nil && len(decoded) == sha256.Size
}
~~~

Always require ClusterHMACKeySHA256. Require LegacyTrialSaltSHA256 exactly when
len(Trials)>0. Copy both values into ImportPlan before Digest(plan). For a delta, require the parent snapshot to use the same cluster HMAC digest.
If the delta itself carries trial upserts, require its salt digest to equal the
parent salt digest. A delta with no trial operations carries no salt digest; its
exact ParentSourceDigest already binds the previously applied full snapshot.

Update every listed fixture to format_version 2 and deterministic synthetic
digests. Preserve all existing customer, order, topology, settings and OTA bytes.

- [ ] **Step 4: Run focused GREEN and commit**

GitHub workflow command:

~~~bash
cd backend
go test ./internal/importer -run 'TestDecodeSnapshot|TestPlan|TestProtectionMetadata|TestDelta' -count=1
~~~

Then require the ordinary backend, race and vet steps green. Commit:

~~~text
feat(importer): bind snapshot protection metadata
~~~

---

### Task 2: Authenticate key bundle, envelopes and legacy trial salt locally

**Files:**
- Create: backend/internal/importer/protection.go
- Create: backend/internal/importer/protection_test.go
- Modify: backend/internal/importer/model.go

**Interfaces:**
- Produces: SnapshotProtection and ProtectionFromSnapshot(snapshot Snapshot).
- Produces: ValidateSnapshotProtection(protection SnapshotProtection, box *controlplane.SecretBox, rawHMACKey, rawTrialSalt []byte) (*TrialImportProtection, error).
- Consumes: LegacyEncryptedSecret owner/source/field/kind scope and existing controlplane.SecretBox.

- [ ] **Step 1: Add RED protection tests**

Define synthetic AES and HMAC keys as distinct 32-byte arrays. Seal one secret
with owner scope and assert:

~~~go
func TestValidateSnapshotProtectionAuthenticatesEveryEnvelope(t *testing.T)
func TestValidateSnapshotProtectionRejectsWrongOwnerScope(t *testing.T)
func TestValidateSnapshotProtectionRejectsWrongHMACKeyBeforeStore(t *testing.T)
func TestValidateSnapshotProtectionRejectsTrialSaltNewlineDrift(t *testing.T)
func TestValidateSnapshotProtectionRequiresNoSaltWhenTrialsAreAbsent(t *testing.T)
func TestValidateSnapshotProtectionReturnsSealedTrialSalt(t *testing.T)
~~~

The happy-path test must open the returned trial envelope with exactly:

~~~go
controlplane.SecretScope{
    OwnerType: "trial_lookup",
    OwnerID:   "legacy",
    Field:     "salt",
    Kind:      "hmac-key",
}
~~~

A recorder passed after validation must remain at zero calls for every failure.

- [ ] **Step 2: Push RED and verify missing API**

Commit:

~~~text
test(importer): require local key and salt authentication
~~~

Expected failure: SnapshotProtection, ProtectionFromSnapshot or
ValidateSnapshotProtection is undefined.

- [ ] **Step 3: Implement minimal protection boundary**

Use:

~~~go
type SnapshotProtection struct {
    ClusterHMACKeySHA256  string
    LegacyTrialSaltSHA256 string
    HasTrials             bool
    EncryptedSecrets      []LegacyEncryptedSecret
}
~~~

For each secret:

1. decode canonical base64 nonce and ciphertext;
2. construct controlplane.Envelope with exact KeyVersion;
3. call box.Open using OwnerType, OwnerSourceKey, Field and Kind;
4. require SHA-256 of plaintext equals LegacyEncryptedSecret.SHA256;
5. zero plaintext immediately.

Require SHA-256(rawHMACKey) to match ClusterHMACKeySHA256. If HasTrials, hash
exact rawTrialSalt bytes without trimming, compare the digest, seal with the
fixed trial scope and marshal this canonical envelope:

~~~go
struct {
    KeyVersion    int    `json:"key_version"`
    NonceB64      string `json:"nonce_b64"`
    CiphertextB64 string `json:"ciphertext_b64"`
}
~~~

Return TrialImportProtection with KeyVersion, canonical envelope JSON and exact
salt SHA-256. If HasTrials is false, require len(rawTrialSalt)==0 and return nil.
Printable errors are fixed strings without secret metadata.

- [ ] **Step 4: Run GREEN and commit**

~~~bash
cd backend
go test ./internal/importer -run '^TestValidateSnapshotProtection' -count=1
go test -race ./internal/importer -run '^TestValidateSnapshotProtection' -count=1
~~~

Commit:

~~~text
feat(importer): authenticate protected import inputs
~~~

---

### Task 3: Expose verified immutable schema identity

**Files:**
- Modify: backend/internal/controlplane/migrations.go
- Modify: backend/internal/controlplane/migrations_test.go

**Interfaces:**
- Produces: controlplane.SchemaIdentity{Version int, Checksum string}.
- Produces: (*Migrator).VerifyIdentity(context.Context) (SchemaIdentity, error).
- Preserves: (*Migrator).Verify(context.Context) error.

- [ ] **Step 1: Add RED identity tests**

~~~go
func TestVerifyIdentityReturnsExactCommittedVersionAndChecksum(t *testing.T) {
    db := mustIntegrationRQLite(t)
    migrator := NewMigrator(db)
    if err := migrator.Apply(context.Background()); err != nil { t.Fatal(err) }
    identity, err := migrator.VerifyIdentity(context.Background())
    if err != nil { t.Fatal(err) }
    if identity.Version != SchemaVersion || len(identity.Checksum) != 64 {
        t.Fatalf("identity=%#v", identity)
    }
}

func TestVerifyIdentityRejectsChangedChecksumWithoutApplying(t *testing.T)
~~~

The second test changes the stored checksum, calls VerifyIdentity, requires an
error and proves the recorder received no Request call.

- [ ] **Step 2: Push RED**

Commit:

~~~text
test(controlplane): require verified schema identity
~~~

Expected failure: SchemaIdentity and VerifyIdentity are undefined.

- [ ] **Step 3: Refactor Verify without adding a write path**

Implement:

~~~go
type SchemaIdentity struct {
    Version  int
    Checksum string
}

func (m *Migrator) Verify(ctx context.Context) error {
    _, err := m.VerifyIdentity(ctx)
    return err
}
~~~

Move the current verification body into VerifyIdentity and return identity only
after foreign-key, checksum, exact table-set and foreign_key_check success.
Do not call Apply from VerifyIdentity.

- [ ] **Step 4: Run GREEN and commit**

~~~bash
cd backend
go test ./internal/controlplane -run 'TestMigrations|TestVerifyIdentity' -count=1
~~~

Commit:

~~~text
refactor(controlplane): expose verified schema identity
~~~

---

### Task 4: Strict target/key configuration and mandatory-mTLS factory

**Files:**
- Create: backend/cmd/maestro-import/runtime.go
- Create: backend/cmd/maestro-import/runtime_test.go
- Modify: backend/cmd/maestro-import/main.go
- Modify: backend/internal/importer/rqlite_store.go
- Modify: backend/internal/importer/rqlite_store_test.go

**Interfaces:**
- Produces: targetConfig, keyBundle, receiptSigningKey, applyRuntime and productionApplyRuntimeFactory.
- Consumes: importer.SnapshotProtection, controlplane.SchemaIdentity and importer.RQLiteApplyStore.
- Produces: (*RQLiteApplyStore).ReadReferencedKeyVersions(context.Context) ([]int, error).
- Produces: targetConfigSHA256 from normalized S2/S3/S4 origins and public certificate fingerprints.

- [ ] **Step 1: Add RED strict-config tests**

Create table tests for:

~~~go
func TestLoadTargetConfigRequiresExactS2S3S4HTTPSMTLS(t *testing.T)
func TestLoadTargetConfigRejectsUnknownFieldsAndBroadPrivateFiles(t *testing.T)
func TestLoadKeyBundleRejectsDuplicateMissingOrEqualKeys(t *testing.T)
func TestProductionFactoryValidatesLocalProtectionBeforeNewRQLite(t *testing.T)
func TestProductionFactoryCallsVerifyIdentityButNeverApply(t *testing.T)
func TestProductionFactoryRejectsMissingTargetKeyVersionBeforeMutation(t *testing.T)
func TestProductionFactoryErrorTextIsSecretFree(t *testing.T)
~~~

Cases include http URL, duplicate URL/node, S1/S5, URL userinfo/path/query,
missing CA/cert/key, malformed/noncanonical base64, 31/33-byte key, absent
current version and identical AES/HMAC bytes.

- [ ] **Step 2: Push RED**

Commit:

~~~text
test(importer): require strict mandatory-mtls runtime
~~~

Expected failure: production runtime types and factory are absent.

- [ ] **Step 3: Implement strict protected readers and config types**

Use strict JSON:

~~~go
type targetConfig struct {
    SchemaVersion int           `json:"schema_version"`
    Voters        []targetVoter `json:"voters"`
    CAFile        string        `json:"ca_file"`
    CertFile      string        `json:"cert_file"`
    KeyFile       string        `json:"key_file"`
    TimeoutSeconds int          `json:"timeout_seconds"`
}

type targetVoter struct {
    NodeID string `json:"node_id"`
    URL    string `json:"url"`
}

type keyBundle struct {
    SchemaVersion     int                 `json:"schema_version"`
    CurrentKeyVersion int                 `json:"current_key_version"`
    EncryptionKeys   []versionedKey       `json:"encryption_keys"`
    HMACKeyB64        string              `json:"hmac_key_b64"`
}

type receiptSigningKey struct {
    SchemaVersion int    `json:"schema_version"`
    SeedB64       string `json:"seed_b64"`
}
~~~

Decode with DisallowUnknownFields and EOF check. Reuse a bounded regular-file
reader; require mode&0077==0 for target config, client private key, key bundle,
trial salt and receipt signing key. Bound config/key files to 1 MiB, certificate
files to 4 MiB, and timeout to 1..30 seconds.

Parse each origin with net/url and require scheme=https, empty user/path/query/
fragment. Lexically sort the canonical voter representation by node ID before
hashing. Parse CA and client certificates, require validity at injected clock,
client-auth extended key usage, and compute SHA-256 over DER public cert bytes.
Do not hash private paths into targetConfigSHA256.

Decode receiptSigningKey strictly, require schema version 1 and one canonical
base64 32-byte Ed25519 seed, derive the private/public key and define signer key
ID as lowercase SHA-256 of the public key.

- [ ] **Step 4: Implement the factory in local-before-network order**

The factory receives injected constructors in tests and performs:

~~~go
type applyRuntime struct {
    Store              *importer.RQLiteApplyStore
    Schema             controlplane.SchemaIdentity
    TargetConfigSHA256 string
    Signer             ed25519.PrivateKey
    SignerKeyID        string
}

type applyRuntimeConfig struct {
    TargetConfigFile     string
    KeyBundleFile        string
    LegacyTrialSaltFile  string
    ReceiptSigningFile   string
    Protection           importer.SnapshotProtection
}
~~~

Order: read/validate every local file; construct SecretBox; call
ValidateSnapshotProtection; zero decoded raw keys/salt; then call rqlite.New
with exactly three HTTPS endpoints, CAFile, CertFile, KeyFile and bounded sizes.
Call NewMigrator(db).VerifyIdentity. Construct
NewRQLiteApplyStoreWithTrialProtection only when protection.HasTrials; otherwise
use NewRQLiteApplyStore. Call ReadReferencedKeyVersions, which performs one
QueryLinearizable over active imported_secrets plus setting_secrets, and pass
the exact sorted distinct set to SecretBox.ReadyForVersions. A missing version
blocks before importer.Apply and produces zero Request calls. Never call
Migrator.Apply.

- [ ] **Step 5: Run GREEN and commit**

~~~bash
cd backend
go test ./cmd/maestro-import -run 'TestLoad|TestProductionFactory' -count=1
go test -race ./cmd/maestro-import -run 'TestLoad|TestProductionFactory' -count=1
~~~

Commit:

~~~text
feat(importer): add mandatory-mtls production runtime
~~~

---

### Task 5: Re-read applied-run evidence and sign a canonical receipt

**Files:**
- Create: backend/internal/importer/receipt.go
- Create: backend/internal/importer/receipt_test.go
- Modify: backend/internal/importer/rqlite_store.go
- Modify: backend/internal/importer/rqlite_store_test.go
- Modify: backend/internal/importer/rqlite_store_integration_test.go

**Interfaces:**
- Produces: AppliedRunEvidence and (*RQLiteApplyStore).ReadAppliedRunEvidence.
- Produces: ImportReceipt, SignImportReceipt and VerifyImportReceipt.
- Consumes: exact run ID, schema identity, targetConfigSHA256 and Ed25519 key.

- [ ] **Step 1: Add RED evidence and signature tests**

~~~go
func TestReadAppliedRunEvidenceRequiresOneCompletedExactRun(t *testing.T)
func TestReadAppliedRunEvidenceRejectsMissingExtraOrMismatchedBatches(t *testing.T)
func TestSignAndVerifyImportReceiptCanonicalRoundTrip(t *testing.T)
func TestReceiptSignatureRejectsChangedRunSchemaOrTarget(t *testing.T)
func TestReceiptJSONContainsNoBusinessRowsOrSecrets(t *testing.T)
~~~

Require one QueryLinearizable batch and zero Request calls. Sort batch receipts
by batch_index and reject gaps, duplicates, non-applied status or count drift.

- [ ] **Step 2: Push RED**

Commit:

~~~text
test(importer): require signed applied-run evidence
~~~

Expected failure: evidence and receipt APIs are undefined.

- [ ] **Step 3: Implement evidence query and canonical digest**

Use:

~~~go
type AppliedRunEvidence struct {
    RunID             string
    SnapshotKind      string
    SourceDigest      string
    PlanDigest        string
    ParentDigest      string
    TargetDigest      string
    BatchCount        int
    BatchReceiptDigest string
    CompletedAtUnix   int64
}
~~~

Read import_runs and ordered import_batches in one QueryLinearizable call.
Canonicalize batch evidence as JSON array of index and digest only, then SHA-256
that byte sequence. Require run status applied, non-null target digest, exact
batch count and every batch status applied.

- [ ] **Step 4: Implement receipt signer/verifier**

~~~go
type ImportReceipt struct {
    SchemaVersion       int    `json:"schema_version"`
    RunID               string `json:"run_id"`
    SnapshotKind        string `json:"snapshot_kind"`
    SourceDigest        string `json:"source_sha256"`
    PlanDigest          string `json:"plan_sha256"`
    ParentDigest        string `json:"parent_source_sha256,omitempty"`
    TargetDigest        string `json:"target_sha256"`
    BatchCount          int    `json:"batch_count"`
    BatchReceiptDigest  string `json:"batch_receipt_sha256"`
    ControlSchemaVersion int   `json:"control_schema_version"`
    ControlSchemaChecksum string `json:"control_schema_checksum"`
    TargetConfigDigest  string `json:"target_config_sha256"`
    SignerKeyID         string `json:"signer_key_id"`
    CompletedAtUnix     int64  `json:"completed_at_unix"`
    SignatureB64        string `json:"signature_b64"`
}
~~~

Canonical unsigned bytes are the same struct without SignatureB64 and with fixed
field order through json.Marshal. Sign with Ed25519. Define SignerKeyID as
lowercase SHA-256 of the public key. Verify strict JSON, canonical base64,
expected key ID and signature. Do not accept unknown fields or trailing JSON.

- [ ] **Step 5: Run GREEN and commit**

~~~bash
cd backend
go test ./internal/importer -run 'TestReadAppliedRunEvidence|TestSign|TestReceipt' -count=1
go test -race ./internal/importer -run 'TestReadAppliedRunEvidence|TestSign|TestReceipt' -count=1
~~~

Commit:

~~~text
feat(importer): add signed applied-run receipts
~~~

---

### Task 6: Wire the real CLI and resumable atomic receipt output

**Files:**
- Modify: backend/cmd/maestro-import/main.go
- Modify: backend/cmd/maestro-import/main_test.go
- Create: backend/cmd/maestro-import/receipt_file.go
- Create: backend/cmd/maestro-import/receipt_file_test.go

**Interfaces:**
- Produces: applyRuntimeFactory func(context.Context, applyRuntimeConfig) (*applyRuntime, error).
- Produces: writeReceiptAtomic(path string, receipt importer.ImportReceipt) error.
- Consumes: runtime factory, importer.Apply, ReadAppliedRunEvidence and SignImportReceipt.

- [x] **Step 1: Add RED CLI orchestration tests**

~~~go
func TestMainPassesNonNilProductionFactory(t *testing.T)
func TestRunDryRunNeverOpensApplyInputsOrFactory(t *testing.T)
func TestRunApplyRequiresTargetReceiptAndSigningPaths(t *testing.T)
func TestRunApplyValidatesLocalInputsBeforeRQLite(t *testing.T)
func TestRunSignsOnlyExactReReadCompletedRun(t *testing.T)
func TestRunReceiptWriteFailureResumesWithoutSecondBatchMutation(t *testing.T)
func TestRunConflictingExistingReceiptFailsClosed(t *testing.T)
func TestWriteReceiptAtomicUses0600RenameAndDirectorySync(t *testing.T)
~~~

The dry-run test passes paths whose opener panics and requires exit 0. The resume
test returns an already-completed store on its second call and requires batch
commit count remains one.

- [x] **Step 2: Push RED**

Commit:

~~~text
test(importer): require production cli receipt gate
~~~

Expected failure: main still passes nil and new flags/writer are absent.

- [x] **Step 3: Extend flags and replace nil factory**

Add apply-only flags --rqlite-config, --receipt and
--receipt-signing-key-file. Make --legacy-trial-salt-file conditional on
snapshot trials. Preserve current --key-file, --expected-plan-digest, --run-id
and --batch-size.

Change main to:

~~~go
func main() {
    os.Exit(run(
        os.Args[1:],
        os.Stdout,
        os.Stderr,
        productionApplyRuntimeFactory,
    ))
}
~~~

Use one 30-minute command context and retain the rqlite per-request timeout from
target config. Dry-run returns before apply-only protected-file checks and
factory construction.

- [x] **Step 4: Apply, re-read, sign and atomically persist**

After importer.Apply succeeds, call ReadAppliedRunEvidence and compare every
field with the approved plan and ApplyResult. Build and sign ImportReceipt using
runtime schema and target digest.

writeReceiptAtomic must:

1. reject symlink destination and conflicting existing bytes;
2. accept exact existing canonical bytes as a no-op;
3. create a same-directory temp file with mode 0600;
4. write, Sync, Close and Rename;
5. open and Sync the parent directory;
6. remove only its exact temp file on failure.

Print success only after receipt persistence. Receipt failure exits 3. Fixed
stderr messages contain no underlying path or secret.

- [x] **Step 5: Run GREEN and commit**

~~~bash
cd backend
go test ./cmd/maestro-import -count=1
go test -race ./cmd/maestro-import -count=1
go vet ./cmd/maestro-import
~~~

Commit:

~~~text
feat(importer): wire production apply receipt gate
~~~

---

### Task 7: Prove the real binary against isolated three-node mTLS rqlite

**Files:**
- Modify: ops/ha/ci-rqlite-cluster.sh
- Modify: ops/ha/test-ci-rqlite-cluster.sh
- Create: backend/cmd/maestro-import/production_integration_test.go
- Create: backend/cmd/maestro-import/testdata/production-full-v2.json
- Modify: .github/workflows/ha-control-plane.yml

**Interfaces:**
- Produces: ci-rqlite-cluster.sh start-mtls and existing start/status/stop.
- Produces: TestPrepareProductionImportSchemaMTLS and TestProductionImportFactoryBinaryProof.
- Consumes: built maestro-import binary and test-only CA/server/client materials inside validated RUNNER_TEMP.

- [ ] **Step 1: Add RED harness and binary-proof contracts**

Extend shell contract tests to require start-mtls, exact certificate permissions,
client rejection without a certificate and unchanged safe stop behavior.

Add build-tagged tests:

~~~go
//go:build rqlite_integration

func TestPrepareProductionImportSchemaMTLS(t *testing.T)
func TestProductionImportFactoryBinaryProof(t *testing.T)
~~~

The proof test must execute the built binary as a subprocess, not call run
directly. It creates strict 0600 target/key/salt/signing files in t.TempDir,
runs dry-run then apply, verifies the receipt with the synthetic public key,
reruns the same run ID and requires the same target/batch receipt digests.

- [ ] **Step 2: Push RED**

Commit tests and workflow step names only:

~~~text
test(ha): require production importer mtls proof
~~~

Expected failure: start-mtls and the real-binary proof are absent. Ordinary
plain isolated-rqlite tests must still pass before the intended RED step.

- [ ] **Step 3: Add test-only mTLS cluster mode**

Within the validated harness root, use OpenSSL to create a test-only CA, one
server certificate with IP SAN 127.0.0.1 and one client certificate. Private
keys use mode 0600. Start each node with:

~~~text
-http-cert <server-cert>
-http-key <server-key>
-http-ca-cert <ca-cert>
-http-verify-client
~~~

Keep Raft traffic on the existing loopback ports and preserve current -fk,
bootstrap, join, PID, marker and cleanup guards. mTLS wait/status probes use
curl with --cacert, --cert and --key. A probe without client material must fail.
The existing start command remains plaintext for existing integration tests.

- [ ] **Step 4: Add exact schema-prep and real-binary proof**

TestPrepareProductionImportSchemaMTLS constructs internal/rqlite.Config from
test-only environment paths and calls NewMigrator(db).Apply exactly as an
isolated harness preparation step. Production CLI code never calls Apply.

TestProductionImportFactoryBinaryProof:

1. builds deterministic synthetic key/salt/envelope inputs matching
   production-full-v2.json;
2. runs dry-run with deliberately nonexistent apply-only protected paths and
   requires exit 0, proving those files and the network were not opened;
3. on the clean prepared cluster, runs wrong HMAC key, wrong salt, wrong client
   certificate and HTTP target cases, requiring failure plus zero business rows
   after every case;
4. executes the exact built binary with valid inputs and requires exit 0;
5. verifies signed receipt, applied evidence and shadow parity;
6. repeats the same run and proves no extra batch mutation;
7. scans subprocess stdout/stderr, report, receipt and captured job text for all
   synthetic raw markers.

- [ ] **Step 5: Wire named GitHub Actions gates**

After ordinary shadow parity, add named steps:

~~~bash
bash ops/ha/ci-rqlite-cluster.sh stop
bash ops/ha/ci-rqlite-cluster.sh start-mtls
cd backend
go test -tags=rqlite_integration ./cmd/maestro-import -run '^TestPrepareProductionImportSchemaMTLS$' -count=1
go build -o "$RUNNER_TEMP/maestro-import" ./cmd/maestro-import
MAESTRO_IMPORT_BINARY="$RUNNER_TEMP/maestro-import"   go test -tags=rqlite_integration ./cmd/maestro-import   -run '^TestProductionImportFactoryBinaryProof$' -count=1
~~~

Expand formatting to cmd/maestro-import, internal/importer, internal/controlplane
and internal/rqlite. Keep checkout/setup actions pinned, permissions contents:
read, timeout bounded and final always-stop cleanup.

- [ ] **Step 6: Run GREEN and commit**

Require all named steps success on the exact full SHA, including ordinary unit,
race, vet, harness, real-rqlite, delete digest parity, shadow parity, schema
prepare, real binary mTLS import, receipt verification and cleanup.

Commit:

~~~text
test(ha): prove production importer over mtls
~~~

---

### Task 8: Full scope, secrecy and durable handoff gate

**Files:**
- Modify: docs/superpowers/plans/2026-08-12-maestrovpn-ha-production-import-factory.md
- Modify: CONTEXT_HANDOFF.md

**Interfaces:**
- Produces: exact-SHA evidence and the next backup/restore design gate.
- Consumes: completed Tasks 1-7 and GitHub Actions run/job conclusions.

- [ ] **Step 1: Run complete exact-SHA verification**

Require these GitHub commands through workflow steps:

~~~bash
cd backend
go test ./...
go test -race ./...
go vet ./...
cd ..
bash -n ops/ha/ci-rqlite-cluster.sh
bash -n ops/ha/test-ci-rqlite-cluster.sh
bash ops/ha/test-ci-rqlite-cluster.sh
~~~

Do not rerun an unexplained failure. Record exact failed command/output, diagnose,
correct once, then submit one new exact-SHA run.

- [ ] **Step 2: Prove scope and secret absence**

Use GitHub compare against the branch SHA preceding Task 1. Require:

- no app/src/main, app/src/test or app/src/androidTest diff;
- no deploy or production service mutation;
- no server IP/password, bot token, customer raw identity or private URL added;
- no InsecureSkipVerify, http production endpoint, Migrator.Apply call from
  cmd/maestro-import, SSH, DNS or OTA code;
- production factory is invoked only for apply after local validation;
- dry-run proof remains network/protected-input free.

Download no production artifact. The receipt workflow artifact remains disabled.

- [ ] **Step 3: Update plan checkboxes and CONTEXT_HANDOFF**

Record full commit SHA, GitHub run ID, job ID and every production-factory step
conclusion. State explicitly:

~~~text
NO-GO (repository implementation only)
S1-S4, panels, bots, customers, Android/TV, Release and OTA unchanged.
Next gate: separate backup/restore design and empty-cluster restore drill.
~~~

- [ ] **Step 4: Final documentation commit**

Commit:

~~~text
docs(ha): record production importer proof
~~~

Production deployment remains forbidden after this commit.

## Plan acceptance

- Snapshot format 2 binds the exact cluster HMAC key digest and conditional
  legacy trial salt digest into source and plan digests.
- Every imported encrypted envelope authenticates under exact scope before any
  network request.
- Target config is exactly S2/S3/S4 HTTPS with verified mandatory client mTLS.
- Production importer verifies immutable schema identity and has no schema-apply
  path.
- Existing importer resume, collision, full/delta, delete and shadow invariants
  remain green.
- A completed run is independently re-read and signed into a canonical Ed25519
  receipt; interrupted receipt output resumes without repeating business writes.
- The real built binary passes synthetic full import, exact rerun, wrong-input
  zero-write and secret-scan proofs against isolated three-node mTLS rqlite.
- Android mobile, TV UI/assets, servers, panels, bots, customers, Release and OTA
  remain untouched.
- Repository implementation alone never reports production GO.
