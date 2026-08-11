# MaestroVPN HA Import Delete Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add fail-closed explicit customer delta deletion with durable S1–S4 tombstones while preserving `full(base)+delta == fresh-full` canonical business digest.

**Architecture:** A typed import registry records exact canonical source digests and immutable cluster target IDs. Customer deletion transitions that registry, revokes runtime access, freezes tombstone targets and writes an immutable delete receipt in one rqlite batch transaction; encrypted secrets remain physically sealed but become logically deleted through their registry state. Canonical business digest filters logically deleted rows and excludes operational reconciliation/import bookkeeping.

**Tech Stack:** Go 1.25, SQLite/rqlite 10.1.0, GitHub Actions, existing importer `ApplyStore`, immutable migration `0001_control_plane.sql`.

## Global Constraints

- Work only in branch `codex/ha-rqlite-task2`, draft PR `#82`.
- Do not mutate production servers, bots, OTA, DNS, customers or subscriptions.
- Production `maestro-import` apply factory remains nil and fail-closed.
- Use `ops/maestro-repetition-guard.py` before every mutation, commit, push and CI scan.
- Use external patch files plus `git apply --check --recount`, then exactly one guarded `git apply --recount` in the linked Windows worktree.
- Run Go, race, vet, harness and three-node rqlite verification only in GitHub Actions.
- Raw credentials, login secrets, UUID, SubID, SubToken, bot tokens, trial salt and decrypted envelopes never enter reports, registry, receipts or logs.
- Only user-provided explicit `customer` delete and planner-derived owner-bound `encrypted_secret` marker are supported in this slice.
- Operational tombstones persist until real target ACK/retention; importer performs no hard delete and simulates no ACK.

---

### Task 1: Deterministic typed delete planning

**Files:**
- Modify: `backend/internal/importer/model.go`
- Modify: `backend/internal/importer/digest.go`
- Modify: `backend/internal/importer/validate.go`
- Modify: `backend/internal/importer/apply.go`
- Modify: `backend/internal/importer/resume_test.go`
- Modify: `backend/internal/importer/importer_test.go`

**Interfaces:**
- Consumes: `PlanOptions.Namespace`, exact `PlanOptions.ParentSnapshot`, `AppliedParentDigest`, `LegacyDelete`.
- Produces: `canonicalLegacyDigest(any) string`, enriched `PlannedDelete`, and tombstone `ApplyOperation.CanonicalJSON` containing the full typed delete proof.

- [ ] **Step 1: Write failing validation and planning tests**

Add tests that build the existing base/delta fixtures and assert exact blocker codes:

```go
func TestDeltaDeleteRequiresExactPriorDigestAndSupportedEntity(t *testing.T) {
    base := decodeFixture(t, "full-then-delta/base-full.json")
    basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
    delta := preparedDelta(t, base, basePlan)
    options := testPlanOptions()
    options.ParentSnapshot = &base
    options.AppliedParentDigest = basePlan.SourceDigest

    delta.Deletes[0].ExpectedPriorDigest = ""
    if _, report := Plan(delta, options); !hasBlockerCode(report.Blockers, "delta_delete_prior_digest_mismatch") {
        t.Fatalf("missing prior digest blockers = %#v", report.Blockers)
    }

    delta = preparedDelta(t, base, basePlan)
    delta.Deletes[0].Entity = "order"
    if _, report := Plan(delta, options); !hasBlockerCode(report.Blockers, "unsupported_delta_delete_entity") {
        t.Fatalf("unsupported delete blockers = %#v", report.Blockers)
    }
}

func TestDeltaDeleteCarriesTypedCustomerAndDerivedSecretProof(t *testing.T) {
    base := decodeFixture(t, "full-then-delta/base-full.json")
    basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
    delta := preparedDelta(t, base, basePlan)
    options := testPlanOptions()
    options.ParentSnapshot = &base
    options.AppliedParentDigest = basePlan.SourceDigest

    plan, report := Plan(delta, options)
    if len(report.Blockers) != 0 || len(plan.Deletes) != 1 || len(plan.CascadeDeletes) != 1 {
        t.Fatalf("typed deletes = %#v / %#v / %#v", plan.Deletes, plan.CascadeDeletes, report.Blockers)
    }
    customerDelete := plan.Deletes[0]
    if customerDelete.TargetID != deterministicID(options.Namespace, "customer", "customer-beta") ||
        customerDelete.PriorGeneration != 1 || customerDelete.NextGeneration != 2 ||
        customerDelete.TombstoneID == "" || !customerDelete.Tombstone {
        t.Fatalf("customer delete proof = %#v", customerDelete)
    }
    secretDelete := plan.CascadeDeletes[0]
    if secretDelete.Entity != "encrypted_secret" || secretDelete.SourceKey != "secret-beta" ||
        secretDelete.TargetID != "secret-beta" || secretDelete.ExpectedPriorDigest == "" || secretDelete.Tombstone {
        t.Fatalf("secret delete proof = %#v", secretDelete)
    }
}
```

Also assert duplicate customer deletes and upsert-plus-delete of the same source produce `delta_delete_collision`.

- [ ] **Step 2: Commit and push RED tests**

Run only static checks locally:

```powershell
git diff --check
git status --short
```

Commit message:

```text
test(importer): require typed delete proofs
```

Push and verify GitHub Actions fails only because typed prior-digest/delete fields and validation are absent.

- [ ] **Step 3: Add canonical source digest helpers and typed fields**

Extend `PlannedDelete` exactly:

```go
type PlannedDelete struct {
    Entity              string `json:"entity"`
    SourceKey           string `json:"source_key"`
    TargetID            string `json:"target_id"`
    ExpectedPriorDigest string `json:"expected_prior_digest"`
    PriorGeneration     int64  `json:"prior_generation,omitempty"`
    NextGeneration      int64  `json:"next_generation,omitempty"`
    TombstoneID         string `json:"tombstone_id,omitempty"`
    Tombstone           bool   `json:"tombstone"`
}
```

Add deterministic helpers in `digest.go`:

```go
func canonicalLegacyDigest(value any) string {
    encoded, err := json.Marshal(value)
    if err != nil {
        return ""
    }
    return sha256Hex(encoded)
}

func plannedCustomerSourceDigest(customer PlannedCustomer) string {
    return canonicalLegacyDigest(LegacyCustomer{
        SourceKey: customer.SourceKey, Login: customer.DisplayLogin,
        LoginKeyHMAC: customer.LoginKeyHMAC, UUIDHMAC: customer.UUIDHMAC,
        SubIDHMAC: customer.SubIDHMAC, TokenHMAC: customer.TokenHMAC,
        CredentialFingerprintHMAC: customer.CredentialFingerprintHMAC,
        IdentitySecretRef: customer.IdentitySecretRef,
        ExpiresAtUnix: customer.ExpiresAtUnix, Generation: customer.Generation,
        Status: customer.Status,
    })
}
```

`preparedDelta` must set the fixture delete digest from the exact parent customer rather than hard-code a digest:

```go
delta.Deletes[0].ExpectedPriorDigest = canonicalLegacyDigest(base.Customers[1])
```

- [ ] **Step 4: Implement exact delete validation and derivation**

In `validateDelta`, require one parent customer, exact prior digest, no duplicate delete and no same-key upsert. Reject every user-provided entity except `customer`. Use `math.MaxInt64` to block generation overflow.

Build the customer proof in `Plan` with:

```go
targetID := deterministicID(options.Namespace, "customer", deletion.SourceKey)
nextGeneration := parentCustomer.Generation + 1
tombstoneID := sha256Hex([]byte("import-tombstone\x00" + targetID + "\x00" +
    strconv.FormatInt(nextGeneration, 10)))
```

Derive each owner-bound secret marker only from `ParentSnapshot.EncryptedSecrets`, using `canonicalLegacyDigest(secret)`. Do not trust user-provided secret delete entries.

Change `planOperations` tombstones from an empty payload to exact canonical JSON:

```go
for _, deletion := range append(append([]PlannedDelete(nil), plan.Deletes...), plan.CascadeDeletes...) {
    encoded, err := json.Marshal(deletion)
    if err != nil {
        return nil, errors.New("encode typed delete operation")
    }
    operations = append(operations, ApplyOperation{
        Entity: deletion.Entity, Key: deletion.SourceKey,
        CanonicalJSON: encoded, Tombstone: true,
    })
}
```

- [ ] **Step 5: Push GREEN planning commit and verify full CI**

Commit message:

```text
feat(importer): plan deterministic typed deletes
```

Required GitHub result: formatting, unit, race, vet, harness and existing real-rqlite integration all pass before Task 2.

---

### Task 2: Import registry, immutable delete receipts and logical digest

**Files:**
- Modify: `backend/internal/controlplane/migrations/0001_control_plane.sql`
- Modify: `backend/internal/controlplane/migrations.go`
- Modify: `backend/internal/controlplane/migrations_test.go`
- Modify: `backend/internal/controlplane/schema_constraints_test.go`
- Modify: `backend/internal/importer/rqlite_store.go`
- Modify: `backend/internal/importer/rqlite_store_test.go`

**Interfaces:**
- Consumes: canonical customer/secret digests from Task 1 and existing `batchWriteGate`.
- Produces: typed `imported_entity_state`, `import_delete_receipts`, registry upsert statements and a logical `InspectTarget` digest that hides deleted imported entities but not active rows.

- [ ] **Step 1: Write RED schema and store tests**

Add schema tests proving:

```sql
INSERT INTO imported_entity_state(
  entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix
) VALUES('customer','schema-customer','customer-id',?, 'active',1000000);
UPDATE imported_entity_state SET lifecycle='deleted' WHERE entity_kind='customer' AND source_key='schema-customer';
```

The test must accept `active -> deleted`, reject `deleted -> active`, target ID changes and digest changes after deletion. It must insert an exact `import_delete_receipts` row and reject a receipt whose expected digest or target differs from the deleted registry row.

Add store tests that inspect customer and standalone-secret requests and require typed registry writes with these exact identities:

```go
[]any{"customer", customer.SourceKey, customer.InternalID,
    plannedCustomerSourceDigest(customer), "active", int64(1_500_000),
    batch.RunID, batch.Index, batch.Digest}
[]any{"encrypted_secret", secret.SecretID, secret.SecretID,
    canonicalLegacyDigest(secret), "active", int64(1_500_000),
    batch.RunID, batch.Index, batch.Digest}
```

Add an `InspectTarget` unit test whose mocked registry marks one customer and secret deleted; the resulting digest must equal the digest from the same mocked result set with those rows absent.

- [ ] **Step 2: Commit and push RED registry tests**

Commit message:

```text
test(importer): require logical delete registry
```

Expected GitHub failure: missing registry/receipt tables, missing typed registry writes and unfiltered digest queries.

- [ ] **Step 3: Add registry and receipt schema**

Create `imported_entity_state` and `import_delete_receipts` in migration 0001. Use exact checks and foreign keys:

```sql
CREATE TABLE imported_entity_state (
  entity_kind TEXT NOT NULL CHECK(entity_kind IN ('customer','encrypted_secret')),
  source_key TEXT NOT NULL CHECK(length(source_key)>0),
  target_id TEXT NOT NULL CHECK(length(target_id)>0),
  canonical_sha256 TEXT NOT NULL CHECK(length(canonical_sha256)=64),
  lifecycle TEXT NOT NULL CHECK(lifecycle IN ('active','deleted')),
  updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix>=0),
  PRIMARY KEY(entity_kind,source_key),
  UNIQUE(entity_kind,target_id),
  UNIQUE(entity_kind,source_key,target_id,canonical_sha256,lifecycle)
);

CREATE TABLE import_delete_receipts (
  entity_kind TEXT NOT NULL,
  source_key TEXT NOT NULL,
  target_id TEXT NOT NULL,
  expected_prior_digest TEXT NOT NULL CHECK(length(expected_prior_digest)=64),
  lifecycle TEXT NOT NULL CHECK(lifecycle='deleted'),
  tombstone_id TEXT REFERENCES tombstones(tombstone_id) ON DELETE RESTRICT,
  import_run_id TEXT NOT NULL,
  batch_index INTEGER NOT NULL CHECK(batch_index>=0),
  batch_digest TEXT NOT NULL CHECK(length(batch_digest)=64),
  imported_at_unix INTEGER NOT NULL CHECK(imported_at_unix>=0),
  PRIMARY KEY(entity_kind,source_key),
  FOREIGN KEY(entity_kind,source_key,target_id,expected_prior_digest,lifecycle)
    REFERENCES imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle),
  FOREIGN KEY(import_run_id,batch_index)
    REFERENCES import_batches(import_run_id,batch_index) ON DELETE RESTRICT,
  CHECK((entity_kind='customer' AND tombstone_id IS NOT NULL) OR
        (entity_kind='encrypted_secret' AND tombstone_id IS NULL))
);
```

Add triggers for immutable target ID, no `deleted -> active`, no digest change after deletion, immutable/undeletable receipts, and customer receipt assertions that customer is deleted at the receipt generation proof, has zero enabled credentials, zero unrevoked tokens and a complete target set.

Add both tables to `expectedSchemaTables` and migration integration expectations.

- [ ] **Step 4: Add active registry writes to customer and secret upserts**

Create a focused helper:

```go
func entityStateUpsertStatement(batch ApplyBatch, entity, sourceKey, targetID, digest string, nowUnix int64) rqlite.Statement
```

Its `ON CONFLICT` updates `canonical_sha256`, `lifecycle='active'` and timestamp; schema triggers abort target substitution or resurrection. Append customer and consumed identity-secret state writes inside `customerStatements`; append standalone secret state inside `encryptedSecretStatements`.

- [ ] **Step 5: Convert `businessDigestQueries` to logical projections**

Remove the direct `tombstones` query. Filter customer-owned tables with `NOT EXISTS` against deleted customer registry target IDs. Filter `imported_secrets` against deleted encrypted-secret registry target IDs. Keep orders unchanged.

Use explicit aliases and deterministic ordering, for example:

```sql
SELECT c.* FROM customers c
WHERE NOT EXISTS(
  SELECT 1 FROM imported_entity_state s
  WHERE s.entity_kind='customer' AND s.target_id=c.customer_id AND s.lifecycle='deleted'
) ORDER BY c.customer_id
```

- [ ] **Step 6: Push GREEN registry commit and verify full CI**

Commit message:

```text
feat(importer): track logical entity lifecycle
```

Do not begin Task 3 until unit, race, vet, harness and real-rqlite schema tests pass.

---

### Task 3: Atomic customer revoke, S1–S4 tombstones and parity proof

**Files:**
- Modify: `backend/internal/controlplane/migrations/0001_control_plane.sql`
- Modify: `backend/internal/controlplane/schema_constraints_test.go`
- Modify: `backend/internal/importer/rqlite_store.go`
- Modify: `backend/internal/importer/rqlite_store_test.go`
- Modify: `backend/internal/importer/rqlite_store_integration_test.go`
- Modify: `.github/workflows/ha-control-plane.yml`

**Interfaces:**
- Consumes: `PlannedDelete` canonical JSON, registry/receipt schema and logical digest from Tasks 1–2.
- Produces: `customerDeleteStatements`, `encryptedSecretDeleteStatements`, exact rollback behavior, and a clean-cluster two-phase parity proof.

- [ ] **Step 1: Write RED unit and real-rqlite delete tests**

Replace the unsupported-tombstone expectation with tests that require customer delete statements to contain:

- registry `active -> deleted` CAS by exact digest/target;
- customer status/generation CAS;
- credential disable and token revoke;
- deterministic tombstone;
- four target inserts from seeded S1–S4 `maestro-core` services;
- immutable customer receipt;
- batch receipt in the same transaction.

Add an injected wrong `ExpectedPriorDigest` operation directly at store level and assert `CommitBatch` returns an error while customer, registry, tombstone targets and `import_batches` remain unchanged.

Add a derived encrypted-secret delete test requiring registry transition plus a receipt with `tombstone_id=NULL`; assert `imported_secrets` still contains the exact encrypted envelope.

- [ ] **Step 2: Commit and push RED apply tests**

Commit message:

```text
test(importer): require atomic customer tombstones
```

Expected GitHub failure: `operationStatements` still rejects tombstones.

- [ ] **Step 3: Implement typed tombstone dispatch and statements**

Change the tombstone branch before the upsert switch:

```go
if operation.Tombstone {
    switch operation.Entity {
    case "customer":
        return s.customerDeleteStatements(batch, operation)
    case "encrypted_secret":
        return s.encryptedSecretDeleteStatements(batch, operation)
    default:
        return nil, fmt.Errorf("unsupported import tombstone entity %q", operation.Entity)
    }
}
```

Decode `PlannedDelete` with unknown fields rejected. Validate exact entity/key, 64-byte digest, non-empty target, customer generation increment and deterministic tombstone ID.

Customer statement order must be: registry CAS, customer CAS, credentials revoke, tokens revoke, tombstone insert, target insert-select, delete receipt insert, then the existing batch finish statement. Add schema triggers so any zero/mismatched CAS aborts before receipt and batch completion.

Encrypted-secret delete must update registry and insert its receipt only. Never issue `DELETE FROM imported_secrets` or put envelope bytes in the receipt.

- [ ] **Step 4: Add clean-cluster two-phase digest integration test**

Create `TestRQLiteImportDeleteDigestPhase`. It runs only when both environment variables are present:

```go
phase := os.Getenv("MAESTRO_IMPORT_DIGEST_PHASE")
proofPath := os.Getenv("MAESTRO_IMPORT_DIGEST_PROOF")
if phase == "" || proofPath == "" { t.Skip("dedicated parity phase") }
```

For `phase=delta`, apply and complete base-full then prepared delta, verify customer-beta has status deleted, four targets exist and secret-beta remains encrypted, then write only the 64-character `InspectTarget().BusinessDigest` to `proofPath` with mode `0600`.

For `phase=fresh`, apply and complete final-full on a newly started empty cluster, read/validate the proof hash and compare it to fresh `InspectTarget().BusinessDigest`.

Update GitHub Actions after the standard integration step:

```yaml
- name: Reset rqlite for importer delta parity
  run: |
    bash ops/ha/ci-rqlite-cluster.sh stop
    bash ops/ha/ci-rqlite-cluster.sh start

- name: Capture full-plus-delta digest
  working-directory: backend
  env:
    MAESTRO_IMPORT_DIGEST_PHASE: delta
    MAESTRO_IMPORT_DIGEST_PROOF: ${{ runner.temp }}/maestro-import-digest-proof
  run: go test -tags=rqlite_integration ./internal/importer -run '^TestRQLiteImportDeleteDigestPhase$' -count=1

- name: Reset rqlite for importer fresh-full parity
  run: |
    bash ops/ha/ci-rqlite-cluster.sh stop
    bash ops/ha/ci-rqlite-cluster.sh start

- name: Compare fresh-full digest
  working-directory: backend
  env:
    MAESTRO_IMPORT_DIGEST_PHASE: fresh
    MAESTRO_IMPORT_DIGEST_PROOF: ${{ runner.temp }}/maestro-import-digest-proof
  run: go test -tags=rqlite_integration ./internal/importer -run '^TestRQLiteImportDeleteDigestPhase$' -count=1
```

Keep the existing always-run final cluster stop. Raise the job timeout from 25 to 35 minutes because the proof intentionally creates two additional clean three-voter clusters.

- [ ] **Step 5: Push GREEN apply commit and inspect exact CI evidence**

Commit message:

```text
feat(importer): apply durable customer tombstones
```

Required evidence: formatting, unit, race, vet, harness, standard integration, delta phase and fresh phase all succeed. Record run ID, job ID and any diagnosed correction in the repetition ledger.

---

### Task 4: Review, handoff and bounded completion

**Files:**
- Modify: `CONTEXT_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-08-11-maestrovpn-ha-import-delete.md`

**Interfaces:**
- Consumes: final RED/GREEN commits and GitHub run/job evidence.
- Produces: durable next-chat checkpoint without claiming production readiness.

- [ ] **Step 1: Run final verification-before-completion audit**

Verify clean worktree, remote HEAD, draft PR state and latest full GitHub conclusion. Search changed files for plaintext markers, unsupported generic staging and accidental production factory enablement.

- [ ] **Step 2: Record exact checkpoint**

Append RED commit/run/job, every correction, final GREEN commit/run/job, typed tables, CAS/revoke/tombstone invariants, parity result and explicit statement that no production/server/bot/OTA/customer mutation occurred.

Mark only this focused plan complete. Do not mark the parent Task 6 or the HA project complete: redacted shadow verification, production factory wiring, cutover gates and server deployment remain separately gated.

- [ ] **Step 3: Commit and push documentation**

Commit message:

```text
docs(importer): record typed delete verification
```

Confirm the documentation-only GitHub run or the most recent code run remains green and the worktree is clean.
