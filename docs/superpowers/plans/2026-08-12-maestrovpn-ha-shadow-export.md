# MaestroVPN HA Reproducible Shadow Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generate both strict redacted shadow exports from one validated synthetic import and prove their exact parity on the real isolated three-node rqlite GitHub Actions cluster.

**Architecture:** Extend the normalized import contract with explicit public protocol/node topology, persist that topology as typed desired business state, and use one canonical `ShadowExport` model for legacy-plan and linearizable candidate projections. The existing offline `shadow-verify.sh` remains the final independent comparator; production factory and live collectors remain absent.

**Tech Stack:** Go 1.25 standard library, existing `internal/rqlite` client, SQLite migration SQL, Bash/Python verifier contract, GitHub Actions Ubuntu runner.

## Global Constraints

- Work only on `codex/ha-rqlite-task2` in the existing linked worktree and draft PR #82.
- Run repetition guard before every mutation, GitHub write, push, workflow retry or long scan.
- Every production/configuration behavior change uses RED -> GREEN; commit and push its failing contract before implementation.
- Local Windows has no Go toolchain; authoritative Go, race, vet, shell and rqlite runs execute in GitHub Actions.
- Use an external unified patch, `git apply --check --recount`, a fresh guard check and exactly one `git apply --recount` for linked-worktree edits.
- Never use path globs such as `backend/*.go` with Windows `rg`; use `rg -g '*.go'`.
- Use unique synthetic `(source_sha256, plan_sha256)` values in every real-rqlite test sharing a cluster.
- No SSH, HTTP or filesystem collector for live S1-S4 is added.
- `backend/cmd/maestro-import/main.go` must keep `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))`.
- No production import, factory wiring, deploy, restart, DNS, TLS, panel, bot, customer, Android, TV, release or OTA mutation.
- Raw login, raw token, UUID, SubID, credential, private URL, bot token/ID, envelope and secret bytes must not appear in export JSON, errors or reports.
- Production status remains `NO-GO (repository implementation only)` after this plan.

---

### Task 1: Explicit topology and typed desired business rows

**Files:**
- Modify: `backend/internal/importer/model.go`
- Modify: `backend/internal/importer/validate.go`
- Modify: `backend/internal/importer/digest.go`
- Modify: `backend/internal/importer/importer_test.go`
- Modify: `backend/internal/importer/rqlite_store.go`
- Modify: `backend/internal/importer/rqlite_store_test.go`
- Modify: `backend/internal/importer/rqlite_store_integration_test.go`
- Modify: `backend/internal/importer/testdata/customers-valid.json`
- Modify: `backend/internal/importer/testdata/orders-pending-credited.json`
- Modify: `backend/internal/importer/testdata/full-then-delta/base-full.json`
- Modify: `backend/internal/importer/testdata/full-then-delta/delta.json`
- Modify: `backend/internal/importer/testdata/full-then-delta/final-full.json`
- Modify: `backend/cmd/maestro-import/main.go`
- Modify: `backend/internal/controlplane/migrations/0001_control_plane.sql`
- Modify: `backend/internal/controlplane/schema_constraints_test.go`

**Interfaces:**
- Produces: `LegacyCustomer.ProtocolTags []string`, `LegacyCustomer.NodeIDs []string`, matching `PlannedCustomer` fields, and `PlanOptions.SupportedProtocolTags/SupportedNodeIDs`.
- Produces: typed `desired_protocol_tags(customer_id,node_id,service_name,protocol_tag)` rows bound by FK to `desired_node_state`.
- Consumes: exact supported protocol tags `vless,hysteria2,anytls,naive,wdtt,olcrtc` and exact nodes `S1,S2,S3,S4` from explicit `PlanOptions`.

- [ ] **Step 1: Write failing topology contract tests**

Add tests before changing production structs:

```go
func TestPlanRequiresExplicitSupportedProtocolAndNodeTopology(t *testing.T) {
    snapshot := decodeFixture(t, "customers-valid.json")
    snapshot.Customers[0].ProtocolTags = []string{"vless", "hysteria2"}
    snapshot.Customers[0].NodeIDs = []string{"S1", "S2", "S3", "S4"}
    plan, report := Plan(snapshot, testPlanOptions())
    if len(report.Blockers) != 0 { t.Fatalf("blockers: %#v", report.Blockers) }
    if got := plan.Customers[0].ProtocolTags; !reflect.DeepEqual(got, []string{"hysteria2", "vless"}) {
        t.Fatalf("protocol tags = %v", got)
    }
    if got := plan.Customers[0].NodeIDs; !reflect.DeepEqual(got, []string{"S1", "S2", "S3", "S4"}) {
        t.Fatalf("node ids = %v", got)
    }
}

func TestPlanBlocksMissingUnsupportedOrDuplicateTopology(t *testing.T) {
    base := decodeFixture(t, "customers-valid.json")
    cases := []struct {
        name, code string
        protocols, nodes []string
    }{
        {"missing protocols", "missing_customer_protocols", nil, []string{"S1"}},
        {"missing nodes", "missing_customer_nodes", []string{"vless"}, nil},
        {"duplicate protocol", "duplicate_customer_protocol", []string{"vless", "vless"}, []string{"S1"}},
        {"duplicate node", "duplicate_customer_node", []string{"vless"}, []string{"S1", "S1"}},
        {"unsupported protocol", "unsupported_customer_protocol", []string{"unknown"}, []string{"S1"}},
        {"unsupported node", "unsupported_customer_node", []string{"vless"}, []string{"S9"}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            snapshot := base
            snapshot.Customers = append([]LegacyCustomer(nil), base.Customers...)
            snapshot.Customers[0].ProtocolTags = tc.protocols
            snapshot.Customers[0].NodeIDs = tc.nodes
            _, report := Plan(snapshot, testPlanOptions())
            if !hasBlockerCode(report.Blockers, tc.code) { t.Fatalf("blockers=%#v, want %s", report.Blockers, tc.code) }
        })
    }
}
```

Add a store recorder test that selects one customer operation and requires one
`desired_node_state` write per node plus one `desired_protocol_tags` write per
node/protocol pair in the same transaction and under the existing batch gate.
A second recorder case starts with stale `S4/wdtt` topology, applies a narrowed
exact set and requires gated removal of both the obsolete tag and every node
that is absent from the new plan.

- [ ] **Step 2: Push and verify topology RED**

Commit only tests/fixtures, push once and inspect the exact-SHA HA workflow.

Expected: compile failure because `ProtocolTags`, `NodeIDs`,
`SupportedProtocolTags` and `SupportedNodeIDs` do not exist; no unrelated
package failure is accepted as RED.

- [ ] **Step 3: Add minimal normalized topology model and validation**

Add exact fields:

```go
type LegacyCustomer struct {
    // existing fields remain byte-compatible
    ProtocolTags []string `json:"protocol_tags"`
    NodeIDs      []string `json:"node_ids"`
}

type PlanOptions struct {
    Namespace             string
    SupportedBotSchemas   []string
    SupportedProtocolTags []string
    SupportedNodeIDs      []string
    ParentSnapshot        *Snapshot
    AppliedParentDigest   string
}
```

Mirror the two slices in `PlannedCustomer`. Validate nonempty, unique values
and exact membership in the explicit options. Clone and lexical-sort slices
before storing them in the plan. Include them in `plannedCustomerSourceDigest`
and therefore in source/plan/delete prior digests.

Set both test and CLI defaults explicitly:

```go
SupportedProtocolTags: []string{"vless", "hysteria2", "anytls", "naive", "wdtt", "olcrtc"},
SupportedNodeIDs:      []string{"S1", "S2", "S3", "S4"},
```

Update every customer fixture with explicit topology; special-access behavior
is represented by each customer's exact list, never inferred from login.

- [ ] **Step 4: Add typed desired protocol schema and writes**

Add after `desired_node_state`:

```sql
CREATE TABLE desired_protocol_tags (
    customer_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    service_name TEXT NOT NULL,
    protocol_tag TEXT NOT NULL,
    PRIMARY KEY(customer_id,node_id,service_name,protocol_tag),
    FOREIGN KEY(customer_id,node_id,service_name)
        REFERENCES desired_node_state(customer_id,node_id,service_name)
        ON DELETE CASCADE
)
```

Include it in the exact schema table set/count and in `businessDigestQueries`:

```sql
SELECT p.* FROM desired_protocol_tags p WHERE NOT EXISTS(
  SELECT 1 FROM imported_entity_state s
  WHERE s.entity_kind='customer' AND s.target_id=p.customer_id
    AND s.lifecycle='deleted'
) ORDER BY p.customer_id,p.node_id,p.service_name,p.protocol_tag
```

For every planned node, `customerStatements` inserts/upserts one
`desired_node_state` with service `maestro-core`, exact customer generation,
the existing protected identity envelope/SHA, status `pending`, and the batch
gate. It then inserts each node/protocol tuple idempotently under the same gate.
Before those upserts, the same transaction deletes existing protocol tuples
outside the exact new protocol set and removes desired-node rows outside the
exact new node set. Empty sets are already blocked by validation. Every delete
contains the existing batch gate, so a failed or replayed batch cannot narrow
topology independently from the customer generation update.
No plaintext or decoded credential is introduced.

- [ ] **Step 5: Verify GREEN locally-light and in GitHub**

Run locally only:

```bash
git diff --check
```

Push the GREEN commit. Require exact-SHA GitHub success for format, unit,
race, vet, harness and real rqlite integration. The integration assertion must
see four desired rows, every expected node/protocol tuple, and no unknown row.

- [ ] **Step 6: Commit Task 1**

```bash
git add backend/internal/importer backend/internal/controlplane backend/cmd/maestro-import
git commit -m "feat(importer): persist explicit customer topology"
```

### Task 2: Canonical shadow model, legacy producer and protected writer

**Files:**
- Create: `backend/internal/importer/shadow_model.go`
- Create: `backend/internal/importer/shadow_plan.go`
- Create: `backend/internal/importer/shadow_write.go`
- Create: `backend/internal/importer/shadow_export_test.go`
- Modify: `backend/internal/importer/testdata/settings-principals-v1.json`

**Interfaces:**
- Produces: `ShadowExport`, `ShadowCustomer`, `ShadowOrder`, `ShadowOTA`, `ShadowURLShapes`.
- Produces: `ShadowFromPlan(ImportPlan, ShadowURLShapes) (ShadowExport, error)`.
- Produces: `EncodeShadowExport(ShadowExport) ([]byte, error)` and `WriteShadowExport(string, ShadowExport) error`.
- Consumes: Task 1 explicit topology and existing protected fingerprints.

- [ ] **Step 1: Write failing canonical model tests**

Use the wished-for API:

```go
func TestShadowFromPlanIsByteStableAndSecretFree(t *testing.T) {
    plan := fullShadowPlan(t)
    shapes := ShadowURLShapes{
        Maestro: "maestro://subscription/{opaque-token}",
        Karing:   "https://example.invalid/karing/{opaque-token}",
    }
    first, err := ShadowFromPlan(plan, shapes)
    if err != nil { t.Fatal(err) }
    reverseShadowPlanInput(&plan)
    second, err := ShadowFromPlan(plan, shapes)
    if err != nil { t.Fatal(err) }
    a, _ := EncodeShadowExport(first)
    b, _ := EncodeShadowExport(second)
    if !bytes.Equal(a, b) { t.Fatal("shadow encoding is order-dependent") }
    for _, forbidden := range []string{"CaseSensitiveUser", "ciphertext_b64", "nonce_b64", "private-token"} {
        if bytes.Contains(a, []byte(forbidden)) { t.Fatalf("export leaked %q", forbidden) }
    }
}
```

Add table tests for duplicate identity, malformed HMAC, missing topology,
invalid shapes, ambiguous/missing OTA, malformed setting/principal secret
binding, non-full plan, plan blockers and invalid order state.

Add a Linux permission test requiring a newly written regular file with mode
`0600`, valid JSON, deterministic bytes, and refusal to overwrite an existing
path.

- [ ] **Step 2: Push and verify model RED**

Expected exact failure: undefined `ShadowExport`, `ShadowURLShapes`,
`ShadowFromPlan`, `EncodeShadowExport` and `WriteShadowExport`.

- [ ] **Step 3: Implement the strict shared model**

Use JSON tags matching the existing verifier exactly:

```go
type ShadowExport struct {
    SchemaVersion         int              `json:"schema_version"`
    Customers            []ShadowCustomer `json:"customers"`
    Orders               []ShadowOrder    `json:"orders"`
    SettingsFingerprint  string           `json:"settings_fingerprint"`
    PrincipalsFingerprint string          `json:"principals_fingerprint"`
    OTA                   ShadowOTA        `json:"ota_manifest"`
}

type ShadowCustomer struct {
    IdentityHMAC    string   `json:"identity_hmac"`
    ExpiresAtUnix   int64    `json:"expires_at_unix"`
    Generation      int64    `json:"generation"`
    ProtocolTags    []string `json:"protocol_tags"`
    Nodes           []string `json:"nodes"`
    MaestroURLShape string   `json:"maestro_url_shape"`
    KaringURLShape  string   `json:"karing_url_shape"`
}

type ShadowOrder struct {
    IdentityDigest      string `json:"order_hmac"`
    State               string `json:"state"`
    ResultExpiresAtUnix int64  `json:"result_expires_at_unix"`
}
```

`IdentityDigest` is the existing deterministic 64-hex internal order ID; it
contains no source key or payment code. State is exactly
`payment_state + ":" + provisioning_state`.

Fingerprints are SHA-256 of canonical JSON arrays:

- settings: key, canonical public JSON, generation, optional protected
  `secret_sha256/key_version`;
- principals: internal ID, login HMAC, status, sorted roles, protected
  verifier SHA/key version.

Parse the exact `ota` public JSON keys `versionCode`, `versionName`, `sha256`
and `size`; export them as the verifier's snake-case keys. Update the synthetic
fixture to contain all four fields.

- [ ] **Step 4: Implement atomic protected output**

Validate before encoding. Sort customers/orders/topology and canonical
fingerprint rows. `WriteShadowExport` creates a temp file in the destination
directory with mode `0600`, writes/fsyncs/closes, then renames only when the
destination does not exist; clean up the temp file on every error. Return only
fixed errors such as `shadow export invalid` and `shadow export unavailable`.

- [ ] **Step 5: Verify Task 2 GREEN and commit**

Require exact-SHA GitHub GREEN, then:

```bash
git add backend/internal/importer
git commit -m "feat(importer): build canonical legacy shadow export"
```

### Task 3: Linearizable candidate projection and producer

**Files:**
- Create: `backend/internal/importer/shadow_candidate.go`
- Create: `backend/internal/importer/shadow_candidate_test.go`
- Create: `backend/internal/importer/shadow_export_integration_test.go`
- Modify: `backend/internal/importer/rqlite_store.go`
- Modify: `backend/internal/importer/rqlite_store_test.go`

**Interfaces:**
- Produces: `ShadowCandidateSource.ReadShadowProjection(context.Context, string) (ShadowProjection, error)`.
- Produces: `ShadowFromCandidate(context.Context, ShadowCandidateSource, string, ShadowURLShapes) (ShadowExport, error)`.
- Produces: `(*RQLiteApplyStore).ReadShadowProjection` using one linearizable query batch.
- Consumes: Task 1 desired topology and Task 2 shared canonical helpers.

- [ ] **Step 1: Write failing source-digest and projection tests**

Create a fake source returning typed rows and assert exact parity with the Task
2 legacy export. Add table tests for:

```go
type fakeShadowCandidateSource struct { projection ShadowProjection; err error }

func (f fakeShadowCandidateSource) ReadShadowProjection(context.Context, string) (ShadowProjection, error) {
    return f.projection, f.err
}

func TestShadowCandidateRejectsDigestMismatchWithoutLeakingRows(t *testing.T) {
    marker := strings.Repeat("a", 64)
    source := fakeShadowCandidateSource{projection: validShadowProjection()}
    source.projection.SourceDigest = strings.Repeat("b", 64)
    _, err := ShadowFromCandidate(context.Background(), source, marker, validShadowShapes())
    if err == nil { t.Fatal("digest mismatch was accepted") }
    if strings.Contains(err.Error(), marker) { t.Fatalf("error leaked input: %v", err) }
}
```

Add explicit table cases for unapplied run, missing receipt, deleted/duplicate
customer, disabled credential, revoked token, missing desired node, missing
protocol tag, duplicate order and malformed OTA/principal projections.

In the build-tagged integration test, guard execution with
`MAESTRO_SHADOW_EXPORT_PROOF=1`, build a complete synthetic full snapshot,
apply it through `Apply`, produce both exports, execute `shadow-verify.sh` and
require exact match output. This test is part of the candidate RED commit, so
its references to candidate APIs fail before production implementation.

The rqlite recorder must observe exactly one `QueryLinearizable` call and no
`Request` call.

- [ ] **Step 2: Push and verify candidate RED**

Expected: undefined candidate interfaces/functions only. Existing importer and
shadow tests must remain green.

- [ ] **Step 3: Implement one-batch linearizable projection**

Define the typed boundary before parsing wire rows:

```go
type ShadowProjection struct {
    SourceDigest      string
    TargetDigest      string
    BatchCount        int64
    AppliedBatchCount int64
    Customers         []ShadowProjectionCustomer
    Orders            []ShadowProjectionOrder
    Settings          []ShadowProjectionSetting
    Principals        []ShadowProjectionPrincipal
}

type ShadowCandidateSource interface {
    ReadShadowProjection(context.Context, string) (ShadowProjection, error)
}

func ShadowFromCandidate(
    ctx context.Context,
    source ShadowCandidateSource,
    expectedSourceDigest string,
    shapes ShadowURLShapes,
) (ShadowExport, error)
```

`ShadowProjectionCustomer` includes internal ID, login HMAC, status, expiry,
generation, credential-enabled/token-revoked evidence and exact node/protocol
sets. Order/setting/principal projection types contain only the canonical
fields required by Task 2 fingerprints.

The query batch contains:

1. all `businessDigestQueries` plus the exact applied run row, target digest,
   batch count and applied receipt count;
2. active customers with `EXISTS` enabled credential and unrevoked token;
3. active `desired_node_state` joined to `desired_protocol_tags`;
4. orders with payment/provisioning/result fields;
5. settings plus optional setting-secret fingerprint/version;
6. principals, roles and active credential fingerprint/version.

Use `applyRowInt` for rqlite INTEGER wire values. Require one applied run for
the expected source, non-null target digest, receipt count equal to batch
count, and a recomputed business digest equal to the committed target digest.
Reject any missing/extra/duplicate relation before producing `ShadowProjection`.

- [ ] **Step 4: Convert candidate rows through shared canonical code**

Map customer login HMAC to `identity_hmac`; build exact sorted node/protocol
sets per active customer; map deterministic `order_id` to `order_hmac`; reuse
the Task 2 state and fingerprint functions. Do not decrypt envelopes or emit
SQL rows in errors.

- [ ] **Step 5: Verify Task 3 GREEN and commit**

Require unit/race/vet GitHub GREEN and recorder proof of one read-only
linearizable batch, then:

```bash
git add backend/internal/importer
git commit -m "feat(importer): export linearizable shadow candidate"
```

### Task 4: Real three-node shadow parity proof

**Files:**
- Create: `backend/internal/importer/shadow_workflow_test.go`
- Modify: `.github/workflows/ha-control-plane.yml`
- Modify: `ops/ha/test-shadow-verify.sh`

**Interfaces:**
- Consumes: Task 1 full typed import, Task 2 legacy export, Task 3 candidate export and existing `shadow-verify.sh`.
- Produces: exact-SHA CI evidence that independently generated exports match on a clean real three-node rqlite cluster.

- [ ] **Step 1: Write a failing workflow contract**

```go
func TestShadowParityHasDedicatedCleanClusterWorkflowPhase(t *testing.T) {
    path := filepath.Join("..", "..", "..", ".github", "workflows", "ha-control-plane.yml")
    data, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    text := string(data)
    required := []string{
        "Reset rqlite for shadow parity",
        "Prove redacted shadow export parity",
        `MAESTRO_SHADOW_EXPORT_PROOF: "1"`,
        `-run '^TestRQLiteShadowExportParity$'`,
    }
    for _, fragment := range required {
        if !strings.Contains(text, fragment) { t.Fatalf("workflow missing %q", fragment) }
    }
}
```

- [ ] **Step 2: Push and verify workflow RED**

Expected: the focused unit test fails only because the four workflow fragments
are absent. The Task 3 integration test remains skipped without its explicit
environment gate.

- [ ] **Step 3: Add the clean-cluster workflow phase**

After the ordinary integration phase, stop/start the isolated cluster, then add:

```yaml
- name: Reset rqlite for shadow parity
  run: |
    bash ops/ha/ci-rqlite-cluster.sh stop
    bash ops/ha/ci-rqlite-cluster.sh start

- name: Prove redacted shadow export parity
  working-directory: backend
  env:
    MAESTRO_SHADOW_EXPORT_PROOF: "1"
  run: go test -tags=rqlite_integration ./internal/importer -run '^TestRQLiteShadowExportParity$' -count=1
```

The integration test must build customer, credited/uncredited orders, complete
OTA setting, principal/roles and protected fingerprints; apply through
`Apply`; create both mode-0600 exports and salt; execute the verifier; require
exact `{"differences":[],"status":"match"}\n`; and scan output/errors for the
fixture login, payment code, nonce, ciphertext and private URL markers.

- [ ] **Step 4: Preserve verifier independence**

The test must run the executable verifier rather than call Go comparison
helpers. Extend `test-shadow-verify.sh` only with invalid duplicate identity,
secret-marker and deterministic mismatch-order cases; keep its network-command
ban and exit-code contract unchanged.

- [ ] **Step 5: Verify full exact-SHA GREEN**

Require one workflow run where all of these pass:

- Go formatting;
- unit tests;
- race tests;
- vet;
- rqlite harness contract;
- ordinary real-rqlite integration;
- clean-cluster redacted shadow parity;
- existing full+delta versus fresh-full digest parity;
- final cluster stop/cleanup.

- [ ] **Step 6: Commit Task 4**

```bash
git add backend/internal/importer/shadow_workflow_test.go ops/ha/test-shadow-verify.sh .github/workflows/ha-control-plane.yml
git commit -m "test(ha): prove redacted shadow parity on rqlite"
```

### Task 5: Final security gate and durable handoff

**Files:**
- Modify: `docs/superpowers/plans/2026-08-12-maestrovpn-ha-shadow-export.md`
- Modify: `CONTEXT_HANDOFF.md`

**Interfaces:**
- Consumes: exact final Git SHA and exact successful workflow/job IDs.
- Produces: focused-plan completion record while parent Plan 02 Task 6 remains open.

- [ ] **Step 1: Run final scope and secret scans**

```bash
git diff --check
rg -n 'os.Exit\(run\(os.Args\[1:\], os.Stdout, os.Stderr, nil\)\)' backend/cmd/maestro-import/main.go
rg -n -g '*.go' 'ssh|exec\.Command\("curl"|production factory|MAESTRO_CONTROL_PLANE' backend/internal/importer
git diff --name-only codex/mobile-4d-deck...HEAD -- app/src/main app/src/test app/src/androidTest
```

Expected: factory remains nil; no network/live collector added; Android/TV diff
for this focused slice is empty. Review export artifacts/logs from the exact CI
run for all synthetic secret markers.

- [ ] **Step 2: Record exact evidence**

Update this plan's checkboxes and append to `CONTEXT_HANDOFF.md`:

- RED and GREEN commit SHAs;
- exact workflow run/job IDs and conclusion;
- proof that legacy and candidate export are independently produced;
- exact verifier match result;
- confirmation that production factory, servers, bots, clients and OTA were untouched;
- next gate: separate production factory design, then backup/restore/cutover gates.

- [ ] **Step 3: Verify docs, commit and push**

```bash
git diff --check
git add docs/superpowers/plans/2026-08-12-maestrovpn-ha-shadow-export.md CONTEXT_HANDOFF.md
git commit -m "docs(ha): record shadow export parity proof"
git push origin codex/ha-rqlite-task2
```

Confirm `git ls-remote` equals local full SHA and the worktree is clean.

- [ ] **Step 4: Preserve completion boundary**

Mark only this focused plan complete. Do not mark parent Task 6 or the HA
project production-ready. Do not wire the factory or touch S1-S4 as a follow-up
inside this plan.
