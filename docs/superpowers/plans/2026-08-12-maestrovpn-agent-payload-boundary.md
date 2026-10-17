# MaestroVPN Agent Payload Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Materialize destination-isolated encrypted desired payloads inside the apply-agent before any local VPN driver call, while preserving one canonical subscription and absolute generation across S1-S4.

**Architecture:** Extend the existing control-plane `SecretBox` with a versioned, length-delimited desired-payload AAD and authenticated plaintext document. Inject a narrow opener into `Agent`; it converts the signed encrypted snapshot into a complete in-memory `MaterializedSnapshot` or fails before the driver. Drivers accept only materialized payloads, and the agent repeats the strong lease/snapshot check immediately before commit.

**Tech Stack:** Go 1.25 standard library, AES-GCM, SHA-256, canonical JSON, existing Ed25519 command protocol, existing rqlite-backed strong lease verification, GitHub Actions.

## Global Constraints

- One canonical customer subscription, absolute expiry and generation remain business truth in rqlite.
- Use one independently rotatable key ring per `(node_id, service_id)`; never fall back to a cluster-wide key.
- Bind AAD to `maestrovpn:desired:v1`, node, service, customer, generation, operation, tombstone and payload kind with length-delimited encoding.
- Never persist or log plaintext payloads; no plaintext in receipts, markers, errors, fixtures, artifacts or handoff documents.
- Open and validate every entry before the first driver call; one bad entry rejects the entire snapshot.
- WDTT and olcRTC remain restricted to the protected account allowlist.
- Do not deploy, distribute/rotate production keys, restart services or mutate customers in this plan.
- Every RED and GREEN commit is separate; run both HA workflows on the exact pushed SHA.

---

### Task 1: Versioned desired-payload codec and exact AAD

**Files:**
- Create: `backend/internal/controlplane/desired_payload.go`
- Create: `backend/internal/controlplane/desired_payload_test.go`
- Modify: `backend/internal/controlplane/crypto.go`

**Interfaces:**
- Consumes: existing `type Envelope`, `type SecretBox` and its versioned AES-GCM key ring.
- Produces:

```go
const DesiredPayloadVersion = 1

type DesiredPayloadScope struct {
	NodeID, ServiceID, CustomerID, OperationID, PayloadKind string
	Generation int64
	Tombstone bool
}

type DesiredPayloadDocument struct {
	Version int             `json:"version"`
	Kind string             `json:"kind"`
	Body json.RawMessage    `json:"body,omitempty"`
	BodySHA256 string       `json:"body_sha256"`
}

func (b *SecretBox) SealDesiredPayload(scope DesiredPayloadScope, body any) (Envelope, string, error)
func (b *SecretBox) OpenDesiredPayload(scope DesiredPayloadScope, envelope Envelope, envelopeSHA256 string) (DesiredPayloadDocument, error)
```

- The returned string from `SealDesiredPayload` is SHA-256 of canonical JSON encoding of the exact `Envelope`; it remains the desired/envelope digest used by outbox and signed snapshots.
- Tombstone documents use canonical body `{"tombstone":true}` and contain no credential.

- [ ] **Step 1: Write the failing codec and mutation tests**

Add table-driven tests named:

```go
func TestDesiredPayloadRoundTripBindsEnvelopeAndBodyDigests(t *testing.T)
func TestDesiredPayloadAADRejectsEveryScopeMutation(t *testing.T)
func TestDesiredPayloadRejectsUnknownVersionMalformedBodyOrDigest(t *testing.T)
func TestDesiredPayloadRotationReadsReferencedOldVersionAndSealsCurrent(t *testing.T)
func TestDesiredTombstoneContainsNoReusableCredential(t *testing.T)
```

The mutation table must independently change node, service, customer, generation,
operation, tombstone and payload kind and require `OpenDesiredPayload` failure.
Use only synthetic values such as `node-a`, `customer-a` and `example.invalid`.

- [ ] **Step 2: Push RED and prove the expected failure in GitHub**

```bash
git add backend/internal/controlplane/desired_payload_test.go
git commit -m "test(controlplane): require destination-bound desired payloads"
git push origin codex/ha-rqlite-task2
```

Expected: both HA workflows fail at backend compile/tests because the desired
payload types and methods do not exist; formatting setup must succeed.

- [ ] **Step 3: Implement canonical AAD and document validation**

Use one explicit binary encoder, not string concatenation:

```go
func desiredPayloadAAD(version int, scope DesiredPayloadScope) ([]byte, error) {
	fields := []string{
		"maestrovpn:desired:v1", strconv.Itoa(version),
		scope.NodeID, scope.ServiceID,
		scope.CustomerID, strconv.FormatInt(scope.Generation, 10),
		scope.OperationID, strconv.FormatBool(scope.Tombstone), scope.PayloadKind,
	}
	var out bytes.Buffer
	for _, field := range fields {
		if field == "" || len(field) > maxSecretScopePart || !utf8.ValidString(field) {
			return nil, errors.New("controlplane: invalid desired payload scope")
		}
		if err := binary.Write(&out, binary.BigEndian, uint32(len(field))); err != nil {
			return nil, errors.New("controlplane: encode desired payload scope")
		}
		out.WriteString(field)
	}
	return out.Bytes(), nil
}
```

Implement `SealDesiredPayload` and `OpenDesiredPayload` on `SecretBox` so they
reuse its private versioned AEAD map. Decode with `DisallowUnknownFields`, reject
trailing JSON, require exact version/kind, canonical re-encoding, body SHA-256 and
exact envelope SHA-256. Errors must be constant redacted strings.

- [ ] **Step 4: Run targeted GREEN in GitHub**

Push the implementation commit:

```bash
git add backend/internal/controlplane/crypto.go backend/internal/controlplane/desired_payload.go
git commit -m "feat(controlplane): seal destination-bound desired payloads"
git push origin codex/ha-rqlite-task2
```

Required exact-SHA results: `go test ./internal/controlplane -run
'TestDesiredPayload|TestDesiredTombstone' -count=1`, full backend, race, vet and
both HA workflows GREEN.

---

### Task 2: Fail-closed materialized snapshot boundary

**Files:**
- Create: `backend/internal/applyagent/materialized.go`
- Create: `backend/internal/applyagent/materialized_test.go`
- Modify: `backend/internal/applyagent/agent.go`
- Modify: `backend/internal/applyagent/agent_test.go`

**Interfaces:**
- Consumes Task 1 `DesiredPayloadScope`, `DesiredPayloadDocument` and exact envelope digest.
- Produces:

```go
type PayloadOpener interface {
	OpenDesiredPayload(controlplane.DesiredPayloadScope, controlplane.Envelope, string) (controlplane.DesiredPayloadDocument, error)
}

type MaterializedEntry struct {
	CustomerID, OperationID, PayloadKind string
	Generation int64
	Tombstone bool
	Body json.RawMessage
	DesiredSHA256, BodySHA256 string
}

type MaterializedSnapshot struct {
	NodeID, ServiceID, TriggerOperationID, SnapshotSHA256 string
	Entries []MaterializedEntry
}
```

- Change `Driver.Inspect` and `Driver.Prepare` to accept `MaterializedSnapshot`.
  `Commit` continues to accept opaque `PreparedChange`.
- Add `Opener PayloadOpener` to `AgentConfig`; `NewAgent` fails when nil.

- [ ] **Step 1: Write failing all-or-nothing materialization tests**

Add exact cases:

```go
func TestAgentOpensEveryEntryBeforeFirstDriverCall(t *testing.T)
func TestAgentPayloadOpenFailureCausesZeroDriverCalls(t *testing.T)
func TestAgentRejectsPayloadKindBodyOrDigestMismatchBeforeDriver(t *testing.T)
func TestAgentDerivesExactAADScopeForEveryEntry(t *testing.T)
func TestDriverInterfaceAcceptsOnlyMaterializedSnapshot(t *testing.T)
func TestDesiredChangeDuringPrepareRejectsOldSnapshotBeforeSwap(t *testing.T)
func TestSecondStrongCheckUsesOriginalSignedSnapshotDigest(t *testing.T)
func TestSecondStrongCheckFailureRollsBackWithoutMarkerOrReceipt(t *testing.T)
```

The fake opener records scopes and returns synthetic JSON bodies. Configure the
second entry to fail and assert `Inspect`, `Prepare`, `Commit`, `Rollback` and
marker `Store` remain zero. Assert the opener is not called before signature,
identity, first strong lease and marker checks pass.

- [ ] **Step 2: Push RED and verify GitHub failure**

```bash
git add backend/internal/applyagent/materialized_test.go backend/internal/applyagent/agent_test.go
git commit -m "test(agent): require fail-closed payload materialization"
git push origin codex/ha-rqlite-task2
```

Expected: backend compile/tests fail because materialized types/opener and the new
driver signatures are absent.

- [ ] **Step 3: Implement materialization before the driver**

Implement:

```go
func materializeSnapshot(opener PayloadOpener, encrypted DesiredSnapshot) (MaterializedSnapshot, error)
```

For each sorted encrypted entry, construct `DesiredPayloadScope` from the signed
snapshot/entry fields, open it, copy the validated body, and append one
`MaterializedEntry`. Return no partial result on error. Preserve encrypted
`SnapshotSHA256` and `PayloadSHA256` only as non-secret identities.

Update `Agent.Apply` order to:

```text
VerifySignedCommand -> destination identity -> strong VerifyCurrentStrong ->
State.Load/monotonic validation -> materialize all -> Driver.Inspect/Prepare
```

Update existing fake drivers/tests mechanically to the new materialized
signature; do not add protocol-specific behavior.

- [ ] **Step 4: Push GREEN and require both workflows**

```bash
git add backend/internal/applyagent/materialized.go backend/internal/applyagent/agent.go backend/internal/applyagent/agent_test.go backend/internal/applyagent/materialized_test.go
git commit -m "feat(agent): materialize encrypted desired snapshots"
git push origin codex/ha-rqlite-task2
```

Required exact-SHA results: applyagent targeted tests, full backend, race, vet,
HA control-plane checks and HA DR restore drill GREEN.

---

---

### Task 3: Repository contract, secrecy audit and Task 12 handoff

**Files:**
- Modify: `docs/superpowers/plans/2026-08-09-maestrovpn-ha-03-bots-agents.md`
- Modify: `CONTEXT_HANDOFF.md`
- Modify: `.github/workflows/ha-control-plane.yml`
- Modify: `.github/workflows/ha-dr-restore-drill.yml`

**Interfaces:**
- Consumes Tasks 1-2 exact GREEN SHA and new materialized driver boundary.
- Produces a CI gate and authoritative Task 12 starting contract.

- [ ] **Step 1: Add a CI policy test before relying on it**

Extend both workflow format/test commands to include all Go files under
`internal/applyagent` and `internal/controlplane`, using a deterministic file
list. Add a repository Python policy assertion that fails if:

```text
Driver.Inspect(context.Context, DesiredSnapshot)
Driver.Prepare(context.Context, DesiredSnapshot)
```

reappears or if an applyagent production file logs/serializes
`MaterializedEntry.Body`.

- [ ] **Step 2: Run policy RED then GREEN in separate commits**

First commit the policy test and prove it detects a synthetic forbidden fixture
without modifying production code. Then update workflow wiring and prove both HA
workflows enforce it.

- [ ] **Step 3: Perform bounded secrecy and scope audit**

Run exact repository searches for plaintext fixtures, credentials, private
subscription URLs, `InsecureSkipVerify`, `verify=False`, raw envelope/body log
formatting and production addresses. Inspect the complete diff from the spec
commit. Expected: only controlplane/applyagent/tests/workflows/plans/handoff;
no `app/**`, production deploy, OTA, DNS, server or customer mutation.

- [ ] **Step 4: Update Plan 03 and handoff**

Mark the payload boundary subplan GREEN only with exact SHA and two workflow run
IDs. State that Task 12 drivers must consume `MaterializedSnapshot`, select only
their configured local service, return actual observed hashes, and cannot access
other node-service key rings. Keep `cmd/maestro-agent` deferred until at least one
real local driver exists; never create a no-op production runtime.

- [ ] **Step 5: Commit documentation checkpoint**

```bash
git add .github/workflows/ha-control-plane.yml .github/workflows/ha-dr-restore-drill.yml docs/superpowers/plans/2026-08-09-maestrovpn-ha-03-bots-agents.md CONTEXT_HANDOFF.md
git commit -m "docs: checkpoint agent payload isolation"
git push origin codex/ha-rqlite-task2
```

Production remains NO-GO after this plan. The next plan step is Task 12 local
drivers, followed by concrete `cmd/maestro-agent` wiring and later separately
approved staged deployment.
