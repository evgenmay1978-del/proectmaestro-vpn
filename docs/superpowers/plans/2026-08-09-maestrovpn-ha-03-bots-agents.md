# MaestroVPN HA Outbox, Apply Agents and Telegram Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Превратить подтверждённые cluster commands в надёжное desired state на S1–S4 и подключить оба Telegram-бота к той же exactly-once бизнес-истине без локальной expiry.

**Architecture:** Control plane сохраняет абсолютное desired state, outbox и monotonic leases. Единственный локальный apply-agent каждого узла принимает подписанную команду только после quorum-проверки epoch/incarnation/fence, применяет generation идемпотентно и возвращает receipt; это относится и к полному desired state olcRTC на S3, поэтому после cutover panel/worker не запускает remote shell/SSH. Копии одного Go Telegram adapter на S2/S3/S4 используют lease/fence и нормализованные durable `BotPollState`/callback rows, keyed by a stable HMAC of numeric Telegram bot identity from `getMe`; token fingerprint is only the current credential version/route. Любой business result уже находится в rqlite до отправки сообщения, а глобальная Telegram buyer identity не зависит от `BOT_ID`, bot identity или token credential.

**Tech Stack:** Go 1.25 standard library, Ed25519, mTLS HTTP, rqlite transaction API, existing x-ui/S2 config adapters behind local driver interfaces, Python legacy bridge only as a temporary fenced artifact.

## Global Constraints

- Выполнять только после GREEN Plans 01–02 на точном Git SHA.
- Не подключаться к production S1–S4 и не запускать live agents/bots/reconcilers.
- Panel/worker не пишет VPN-конфиги и olcRTC state по shell/SSH или remote x-ui после HA cutover; все S2/S3 mutations проходят только через desired state, outbox и локальный apply-agent.
- Agent отказывает при недоступном quorum, stale epoch/incarnation/fence/generation или неверной подписи; last-good VPN остаётся запущенным.
- Side effects выполняются только из абсолютного desired state; локальная expiry никогда не повышает cluster expiry.
- Одна stable Telegram bot identity (HMAC numeric `getMe.id`) имеет один active poller lease и durable poll/callback/cutover state; token fingerprint — credential version/route, а `BOT_ID` — только deployment alias, не state owner, buyer identity или business idempotency key.
- Bot cutover всегда выполняется в порядке `fence old poller -> capture final offset + pending/in-flight callbacks/claims -> idempotent import -> verify digest/counts -> start new poller`; новый poller не получает lease до подтверждённого import receipt.
- Exactly-once относится к payment/expiry/generation. Telegram может редко повторить одинаковый текст после ambiguous send, но не business command.
- Полные live source/dependencies/schema/systemd/token ownership обоих ботов отсутствуют в Git. До их inventory и parity review production bot switch остаётся NO-GO.
- Никаких новых платёжных систем, webhook, live bot-token rotation или private subscription URLs в сообщениях владельцу. Token-rotation CAS проектируется и тестируется для сохранности state, но не выполняется на production этим планом.

---

### Task 10: Desired state, durable outbox, leases, receipts and tombstones

**Files:**
- Create: `backend/internal/controlplane/outbox.go`
- Create: `backend/internal/controlplane/outbox_test.go`
- Create: `backend/internal/controlplane/reconcile.go`
- Create: `backend/internal/controlplane/reconcile_test.go`
- Modify: `backend/internal/controlplane/types.go`
- Modify: `backend/internal/controlplane/service.go`

**Interfaces:**
- Produces: `DesiredSnapshot`, `ClaimOutbox`, `RenewNodeLease`, `RecordReceipt`, `ReconcileNode`, `AcknowledgeTombstone`.
- Consumes: order/trial/admin commands from Plan 02; one shared cluster epoch and per-node incarnation.

- [ ] **Step 1: Write failing state-machine tests**

```go
func TestDesiredGenerationNeverMovesBackward(t *testing.T)
func TestOutboxUniquenessPerOperationNodeServiceGeneration(t *testing.T)
func TestLeaseFenceMonotonicallyIncreases(t *testing.T)
func TestStaleLeaseCannotRecordReceipt(t *testing.T)
func TestSameGenerationDifferentPayloadHashConflicts(t *testing.T)
func TestDeleteKeepsTombstoneUntilEveryEnabledServiceAcknowledges(t *testing.T)
func TestReconcileRepairsMissingOutboxFromDesiredSnapshot(t *testing.T)
func TestFencedOrDownRequiredNodeBlocksTombstonePurge(t *testing.T)
func TestPermanentRetirementRequiresAuditedOwnerCAS(t *testing.T)
```

Assert no test updates customer expiry from a node receipt. A missing outbox row may be reconstructed from desired state; a receipt never becomes source of business truth.

- [ ] **Step 2: Run and verify RED**

Run: `cd backend && go test ./internal/controlplane -run 'TestDesired|TestOutbox|TestLease|TestStaleLease|TestSameGeneration|TestDeleteKeeps|TestReconcileRepairs|TestFencedOrDown|TestPermanentRetirement' -count=1`

Expected: FAIL because outbox/reconcile methods are absent.

- [ ] **Step 3: Implement absolute desired state and unique events**

```go
type DesiredState struct {
    CustomerID, NodeID, ServiceID string
    Generation int64
    Payload Envelope
    PayloadSHA256 string
    Tombstone bool
    OperationID string
}
type NodeLease struct {
    NodeID, ServiceID, HolderID string
    Epoch, Incarnation, Fence int64
    ExpiresAt time.Time
}
```

Every business transaction upserts `(customer,node,service)` only when the new generation is greater, or when the same generation has the identical hash. Insert outbox with unique `(operation,node,service,event_kind,generation)`. Lease acquisition is one `INSERT ... ON CONFLICT ... WHERE ... RETURNING` statement using DB `unixepoch()`: same-holder renewal keeps the fence, expired-holder handoff increments it, and empty `RETURNING` is `ErrLeaseHeld`. Lease TTL is 90 seconds and renewal interval 30 seconds. `RecordReceipt` strong-checks epoch, incarnation, current unexpired fence, generation and expected snapshot hash in the same transaction; no quorum creates neither lease nor local claim.

- [ ] **Step 4: Implement deletion semantics**

Subscription expiry increments generation and writes service-revoke desired state but preserves customer/credentials for later renewal. Hard delete writes a customer tombstone and immutable `tombstone_targets` set for every required service. Down, disabled or fenced S1 remains required and blocks purge. Only a separate owner-authorized permanent-retirement CAS with append-only audit may remove a target. Credentials remain encrypted until every remaining target acknowledges and 90 days have elapsed after the last required ack.

- [ ] **Step 5: Implement periodic full reconciliation**

`ReconcileNode` linearizable-reads desired rows and receipts in bounded pages, recreates missing unique outbox events and marks provisioning `ready|degraded|failed` without altering payment state. It reports lag/generation counts to the owner panel. It never reads x-ui/S2 expiry to advance control-plane state.

- [ ] **Step 6: Run tests and commit**

Run: `cd backend && go test -race ./internal/controlplane -run 'TestDesired|TestOutbox|TestLease|TestStaleLease|TestSameGeneration|TestDeleteKeeps|TestReconcileRepairs|TestFencedOrDown|TestPermanentRetirement' -count=1`

```bash
git add backend/internal/controlplane
git commit -m "feat(controlplane): add desired state and fenced outbox"
```

### Task 11: Signed and fenced apply-agent protocol

**Files:**
- Create: `backend/internal/applyagent/protocol.go`
- Create: `backend/internal/applyagent/protocol_test.go`
- Create: `backend/internal/applyagent/agent.go`
- Create: `backend/internal/applyagent/agent_test.go`
- Create: `backend/internal/applyagent/dispatcher.go`
- Create: `backend/internal/applyagent/dispatcher_test.go`
- Create: `backend/internal/applyagent/local_state.go`
- Create: `backend/internal/applyagent/local_state_test.go`
- Create: `backend/internal/applyagent/http.go`
- Create: `backend/internal/applyagent/http_test.go`
- Deferred until Task 12 has at least one real local driver:
  `backend/cmd/maestro-agent/main.go`

**Interfaces:**
- Produces: signed full `DesiredSnapshot`, dispatcher, `Agent.Apply`, private `/v1/apply`, signed `/v1/status`, `/livez`, `/readyz`, per-entry `ApplyReceipt`.
- Consumes: local `Driver`, quorum `LeaseVerifier`, Ed25519 signing public key and mTLS service identity.

- [ ] **Step 1: Write failing canonical-signature and monotonicity tests**

```go
func TestCanonicalCommandSignatureRejectsFieldMutation(t *testing.T)
func TestAgentRejectsWrongNodeServiceEpochOrIncarnation(t *testing.T)
func TestAgentRejectsOldFenceEvenBeforeSeeingNewCommand(t *testing.T)
func TestAgentRejectsOldGenerationAndHashConflict(t *testing.T)
func TestAgentSameGenerationSameHashIsNoOp(t *testing.T)
func TestNoQuorumCausesZeroDriverCalls(t *testing.T)
func TestFenceIsRecheckedImmediatelyBeforeSwap(t *testing.T)
func TestCrashAfterSideEffectBeforeReceiptRetriesAsHashNoOp(t *testing.T)
func TestConcurrentApplyIsSerializedPerService(t *testing.T)
func TestTwoS2EventsCoalesceIntoOneFullSnapshotCommand(t *testing.T)
func TestFullSweepAppliesDriftWithoutOutboxRow(t *testing.T)
func TestDesiredChangeDuringPrepareRejectsOldSnapshotBeforeSwap(t *testing.T)
func TestCommandBodyOverFourMiBIsRejected(t *testing.T)
func TestSignedStatusRequiresFreshStrongLeaseProof(t *testing.T)
```

The stale-fence test configures the fake verifier with a newer cluster fence while the agent has never received that newer command; the old command still must fail.

- [ ] **Step 2: Run and verify RED**

Run: `cd backend && go test ./internal/applyagent -count=1`

Expected: FAIL because package is absent.

- [ ] **Step 3: Define and sign one canonical envelope**

```go
type DesiredEntry struct {
    CustomerID, OperationID string
    Generation int64
    Tombstone bool
    Payload controlplane.Envelope
    PayloadSHA256 string
}
type DesiredSnapshot struct {
    NodeID, ServiceID, TriggerOperationID string
    Entries []DesiredEntry
    SnapshotSHA256 string
}
type ApplyCommand struct {
    Version int
    ClusterEpoch, NodeIncarnation, LeaseFence int64
    NodeID, ServiceID, HolderID string
    Snapshot DesiredSnapshot
    IssuedAtUnix, NotAfterUnix int64
}
type SignedCommand struct {
    KeyID string
    Command []byte
    Signature []byte
}
type LeaseVerifier interface {
    VerifyCurrentStrong(context.Context, nodeID, serviceID, holderID, snapshotSHA256 string, epoch, incarnation, fence int64) error
}
type Driver interface {
    Inspect(context.Context, MaterializedSnapshot) (AppliedState, error)
    Prepare(context.Context, MaterializedSnapshot) (PreparedChange, error)
    Commit(context.Context, PreparedChange) (AppliedState, error)
    Rollback(context.Context, PreparedChange) error
}
```

The outer `SignedCommand` has a 4 MiB limit. Verify Ed25519 over exact canonical command bytes and known `KeyID` before decoding entries or opening their node-scoped AES-GCM envelopes; AAD binds node/service/customer/generation. Sort entries by customer ID and verify aggregate snapshot SHA-256. Command lifetime is 60 seconds. Enforce exact mTLS node/service identity. Errors/logs contain operation ID and hashes only, never envelope/payload/credentials.

- [ ] **Step 4: Implement fail-closed apply sequence**

Dispatcher acquires the target lease by CAS, strong-reads the complete sorted `(node,service)` snapshot, coalesces pending events, signs it and sends one command. Under a per-service process+file lock the agent verifies signature/local marker, strong-checks epoch/incarnation/holder/fence/snapshot hash before `Prepare` and again immediately before `Commit`, then commits, health-checks and fsyncs a local aggregate marker. S2 service snapshots contain every user; x-ui may contain one entry. Dispatcher transaction rechecks fence/current snapshot, stores per-entry receipts and closes all satisfied events. An ambiguous response retries by actual aggregate hash without another reload. If desired state changed, stale receipt is rejected and the next sweep applies the latest snapshot; one unavailable target never blocks other targets.

- [ ] **Step 5: Harden the HTTP server**

Listen only on configured management address, require client cert SAN mapped to `controlplane-dispatcher`, disable redirects and set header/body/time limits. `/v1/status` contains nonce, node/service, readiness, cluster epoch/fence/snapshot digest and short expiry; the agent strong-verifies current lease then signs it with its node status key for DNS health. Public routes never expose agent endpoints. Startup fails without CA/cert/key, signing/decryption/status keys, node ID, incarnation and service allowlist.

- [ ] **Step 6: Run race/security tests and commit**

Run: `cd backend && go test -race ./internal/applyagent -count=1 && go vet ./internal/applyagent`

```bash
git add backend/internal/applyagent
git commit -m "feat(agent): add signed fenced apply protocol"
```

#### Task 11 payload-boundary subplan — GREEN (12.08.2026)

The approved node-service payload-isolation design and implementation subplan are
GREEN through exact SHA `2b713af2ff9bb5da6aa1353e091205d8f2805767`.
Task 1 final GREEN `4572c7f2efb7e9ca7ed179d54164d01757d840e1`
passed HA control-plane run `31623192987` and HA DR run `31623228487`.
Task 2 final GREEN `efe6d6f40b9d2020c2aaee3c58f34eb5b358eed8`
passed control-plane run `31630333917` and DR run `31630333923`.
Task 3 policy RED `5ce1447ce467b384d54103ff09b589011d29562c`
failed only at the missing workflow gate in control-plane run `31632212354`
and DR run `31632212414`. Final Task 3 GREEN
`2b713af2ff9bb5da6aa1353e091205d8f2805767` passed control-plane run
`31633321011` and DR run `31633321063`.

The durable boundary is now:

- the signed/encrypted `DesiredSnapshot` is authenticated and opened completely
  before the first driver call;
- drivers receive only `MaterializedSnapshot`, select exactly their configured
  local service and return actual observed hashes;
- no driver may open another node-service key ring, cache/log plaintext or expose
  dependency error text; outward errors are fixed and safe;
- CI rejects the old encrypted driver signatures and direct production Body
  logging/serialization sinks after proving the scanner on a synthetic forbidden
  fixture;
- `cmd/maestro-agent` stays deferred until at least one real local Task 12 driver
  exists. A no-op production runtime is forbidden.

Production remains NO-GO. Repository GREEN does not authorize server access,
deployment, restart, customer/bot/payment/DNS/OTA changes or protocol enablement.

### Task 12: Local x-ui, S2 and S3 olcRTC drivers; legacy writers become pre-cutover-only

**Files:**
- Create: `backend/internal/applyagent/xui_driver.go`
- Create: `backend/internal/applyagent/xui_driver_test.go`
- Create: `backend/internal/applyagent/s2_config_driver.go`
- Create: `backend/internal/applyagent/s2_config_driver_test.go`
- Create: `backend/internal/applyagent/s2_naive_adoption.go`
- Create: `backend/internal/applyagent/s2_naive_adoption_test.go`
- Create: `backend/internal/applyagent/olcrtc_driver.go`
- Create: `backend/internal/applyagent/olcrtc_driver_test.go`
- Create: `backend/internal/applyagent/testdata/s2-valid/`
- Create: `backend/internal/applyagent/testdata/s2-invalid/`
- Create: `backend/internal/applyagent/testdata/s2-naive-adoption/`
- Create: `backend/internal/importer/applied_run_attestation.go`
- Create: `backend/internal/importer/applied_run_attestation_test.go`
- Modify: `backend/cmd/maestro-import/main.go`
- Create: `ops/ha/s2-naive-adoption-manifest.py`
- Create: `ops/ha/tests/test_s2_naive_adoption_manifest.py`
- Create: `ops/ha/tests/fixtures/s2-naive-adoption-valid.json`
- Create: `ops/ha/tests/fixtures/s2-naive-adoption-source-mismatch.json`
- Create: `ops/ha/tests/fixtures/s2-naive-adoption-duplicate.json`
- Create: `ops/ha/tests/fixtures/s2-naive-adoption-ambiguous.json`
- Modify: `backend/internal/api/olcrtc_admin.go`
- Modify: `backend/internal/api/panel.go`
- Create: `backend/internal/api/olcrtc_ha_test.go`
- Modify: `ops/dates-reconcile.py`
- Modify: `ops/olcrtc-room.sh`
- Create: `ops/ha/dates-audit.py`
- Create: `ops/test_dates_reconcile_mode_guard.py`
- Create: `ops/test_olcrtc_room_mode_guard.py`
- Create: `deploy/ha/maestro-dates-audit.service`
- Modify: `backend/cmd/maestro-panel/main.go`
- Create: `backend/cmd/maestro-panel/main_test.go`

**Interfaces:**
- Produces: local-only x-ui S1/S3/S4 driver; separate full-snapshot `s2-hysteria2`, `s2-anytls`, `s2-naive` drivers; full-snapshot `s3-olcrtc` driver; signed versioned `AppliedImportAttestation` and `S2NaiveAdoptionManifest`, `NaiveAdoptionReport` and zero-unowned-imported-user cutover gate; new HA read-only drift report.
- Consumes: existing `xui`, `server2` and olcRTC parsers/renderers only behind injected local command/filesystem interfaces, Plan 02's completed `import_runs` plus immutable imported rows, an exact hard-fenced read-only S2 Caddy snapshot, the signed `S2NaiveAdoptionManifest`, and absolute desired snapshots from Tasks 10–11.

Every production driver consumes only the already authenticated
`MaterializedSnapshot`. It is configured for one local service and rejects any
other node/service or remote target before mutation. It reports hashes measured
from the actual post-apply local state, never substitutes the desired/envelope
digest as observation, never opens another node-service key ring, and never
caches, logs or serializes plaintext. Parser, validator, process and filesystem
failures map to fixed safe driver errors. Only after one such real driver is GREEN
may Task 12 add concrete `cmd/maestro-agent` wiring; a no-op driver/runtime is not
an acceptable intermediate production command.

- [ ] **Step 1: Write x-ui driver RED tests**

Test localhost-only endpoint enforcement, exact login/UUID/SubID/flow/absolute expiry preservation, add/update/delete idempotency, same-generation hash no-op, API error not interpreted as absence and rollback/receipt mismatch. Remote IP/hostname input must be rejected. Add `TestRqliteXUICompositionHasNoRemoteEndpoint` so an HA composition cannot smuggle a remote host into the local driver.

- [ ] **Step 2: Write S2 full-config and Naive adoption RED tests**

Use a fake filesystem/process runner to require:

1. restrictive `umask` and temp files on the same filesystem;
2. deterministic full render sorted by stable customer ID;
3. syntax and semantic validators for every managed config;
4. fsync file and containing directory;
5. atomic rename only after all validators pass;
6. exactly one restart/reload for each changed S2 service snapshot;
7. protocol health plus expected actual hash;
8. last-good rollback and one recovery reload if health fails.

Add crash points before validation, before rename, after rename, after reload and before receipt. A retry must converge without duplicate users or extra expiry.

Add exact cases `TestAppliedRunAttestationIncludesExactS2NaiveRoster`, `TestAppliedRunAttestationRejectsIncompleteFinalImportOrCanonicalDigestDrift`, `TestS2HysteriaTwoEventsProduceOneRestart`, `TestS2AnyTLSTwoEventsProduceOneRestart`, `TestS2NaiveManifestBindsAppliedRunAttestationAndFencedCaddyBytes`, `TestS2NaiveManifestRejectsUnsignedExpiredSourceMismatchDuplicateOrAmbiguousMembership`, `TestS2NaiveManifestContainsNoPlaintextCredential`, `TestS2NaivePreservesUnrelatedCaddyBytesExactly`, `TestS2NaiveAdoptsImportedUserOutsideMarkerWithoutChangingCredentialBytes`, `TestS2NaiveAdoptionRejectsCredentialMismatchOrDuplicate`, `TestS2NaiveCutoverRequiresZeroUnownedImportedUsers`, `TestS2NaiveImportedExpiryProducesOneDelete`, `TestS2NaiveDeleteRetryDoesNotDuplicateOrResurrectUser`, `TestUnsafeHysteriaLoginFailsWholeSnapshotBeforeSwap`, `TestValidatorFailureLeavesLiveBytesAndReloadCountUnchanged` and `TestHealthFailureRestoresExactBytesModeAndLastGoodHash`.

The Naive adoption fixtures contain three byte-distinct classes: imported pre-existing users currently outside `# MTV-MANAGED-START ... # MTV-MANAGED-END`, already managed users, and unrelated legacy users. Assert that an imported user's exact username/password bytes move into the managed block once, its control-plane absolute expiry is unchanged, expired/tombstoned desired state removes it once, and every unrelated byte (including comments, whitespace, ordering and line endings) remains identical.

During the same final-import write-freeze, run `maestro-import attest-applied-run` against one quorum-strong transaction. It accepts only the final Plan 02 `import_runs.state == applied` row with complete deterministic batches and requires the recomputed digest of the complete current canonical business/service snapshot to equal that run's stored target digest. It derives the exact sorted `s2-naive` roster from current canonical customer, service-binding and protected credential rows (not row provenance), rejects a missing/duplicate/ambiguous binding, and signs this output with the migration-attestation key:

```go
type AppliedImportAttestation struct {
    Version int
    RunID, SourceDigest, PlanDigest, TargetDigest, CanonicalSnapshotDigest string
    S2NaiveUsers []struct { CustomerIDHMAC, UsernameHMAC, CredentialHMAC string; AbsoluteExpiryUnix, Generation int64 }
    CapturedAtUnix, NotAfterUnix int64
    SignerKeyID string
    Signature []byte
}
```

The attestation is the Task 12 authoritative producer contract missing from Plan 02: while writes remain frozen, it binds exact current canonical membership and expected credential HMACs to the applied target rather than inferring either from Caddy. The credential HMAC is domain-separated and keyed over the canonical username/password byte representation; it never exposes plaintext or a brute-forceable raw digest. An incomplete final run, any canonical target drift or a non-unique customer-to-`s2-naive` binding is production NO-GO.

Then run the read-only manifest producer on S2 only after the legacy Caddy writer has a signed hard-fence receipt. It joins the signed `AppliedImportAttestation` roster to the exact unnormalized Caddy bytes by keyed username and credential HMAC, rejecting any mismatch, and emits one signed, short-lived, version-1 manifest:

```go
type S2NaiveAdoptionManifest struct {
    Version int
    NodeID, ServiceID, AppliedImportAttestationSHA256 string
    SourceCaddySHA256, ManagedMarkerSHA256, UnrelatedBytesSHA256 string
    ImportedUsers []struct { CustomerIDHMAC, UsernameHMAC, CredentialHMAC string; AbsoluteExpiryUnix, Generation int64 }
    LegacyFenceReceiptSHA256, SignerKeyID string
    CapturedAtUnix, NotAfterUnix int64
    Signature []byte
}
```

The manifest contains hashes/HMACs only, never a plaintext username/password. Its signature binds the exact applied-run attestation, source Caddy digest, marker digest, unrelated-byte digest, absolute expiries and sorted imported membership. For any attested user, zero or more than one Caddy match, a duplicate inside/outside marker, an unfenced/changed source, or an ambiguous join is a production NO-GO. Producer fixtures and Go consumer fixtures must agree byte-for-byte on canonical serialization, signature verification and every digest before the driver is allowed to stage a file.

- [ ] **Step 3: Write S3 olcRTC and post-cutover writer RED tests**

Add exact cases `TestS3OlcRTCFullSnapshotIsSortedValidatedAndIdempotent`, `TestS3OlcRTCExpiryAndDeleteRemoveOnePerLoginExit`, `TestS3OlcRTCRetryDoesNotDuplicateRoomOrRestart`, `TestRqliteOlcRoomWritesDesiredOutboxWithoutExec`, `TestRqliteModeNeverConstructsRemoteServer2OrOlcShellWriter`, `TestLegacyOlcRoomScriptExitsThreeBeforeReadingSecretsOrSSH` and `TestLegacyOlcRoomScriptRunsOnlyInExplicitLegacyMode`. The driver fake owns local S3 files and systemd runner only; any hostname, SSH argv, `/bin/sh` dispatch from an HA composition, or mutation before the mode guard fails the test.

- [ ] **Step 4: Run and verify RED**

Run: `cd backend && go test ./internal/applyagent ./internal/api ./cmd/maestro-panel -run 'TestXUI|TestS2|TestS3OlcRTC|TestRqlite|TestLegacyOlc' -count=1`

Run: `cd backend && go test ./internal/importer -run 'TestAppliedRunAttestation' -count=1`

Run: `python -m unittest ops.test_dates_reconcile_mode_guard ops.test_olcrtc_room_mode_guard`

Run: `python -m unittest discover -s ops/ha/tests -p 'test_s2_naive_adoption_manifest.py'`

Expected: FAIL because drivers and guards are absent.

- [ ] **Step 5: Implement local drivers and one-time Naive adoption without direct cluster writes**

The x-ui driver may call an API only on loopback and distinguishes transport/API error from not-found. Each S2 service driver renders its complete sorted user snapshot into a private same-filesystem staging file, validates syntax+semantics, atomically renames that file and owns exactly one service reload. Unsafe Hysteria login fails the whole snapshot instead of being skipped. Validators are injected and mandatory; production enablement remains NO-GO until read-only inventory records exact installed binary/version and supported validation argv. No driver reads local expiry into desired state or accepts relative `+days`.

For the first `s2-naive` generation, verify and consume the exact signed `S2NaiveAdoptionManifest`, then re-hash the still-fenced live Caddy bytes before parsing them without normalization. Every manifest-listed imported user found outside `# MTV-MANAGED-START ... # MTV-MANAGED-END` must be adopted into that block with the credential bytes preserved exactly; a missing imported user, credential mismatch, duplicate inside/outside occurrence, source/receipt/fence digest mismatch, expired signature or ambiguous parse aborts before swap. `NaiveAdoptionReport{ImportedExpected, ImportedManaged, UnownedImported, UnrelatedSHA256}` is stored with the receipt. `RequireNaiveCutoverReady` returns success only when `UnownedImported == 0`, expected and managed counts match, the manifest membership is exhausted exactly once, and the unrelated-byte digest is unchanged. After that gate, ordinary full snapshots own only the managed marker; expiry/hard-delete arrive as desired tombstones, remove the managed entry once and never infer or extend expiry locally.

The `s3-olcrtc` driver consumes one complete sorted snapshot containing global carrier state plus every per-login room/key/provider/transport and tombstone. On S3 it writes only private same-filesystem staging files, validates the full set, atomically swaps it, starts/reloads/stops only changed local `olcrtc-srv`/`olcrtc-srv@<login>` units, verifies expected aggregate hash and records per-entry receipts. Expiry/delete removes the intended per-login exit exactly once; retry with the same generation/hash is a no-op. It never opens SSH and never calls the legacy room script.

- [ ] **Step 6: Fence every legacy writer in repository behavior**

Keep existing `deploy/maestro-dates-reconcile.service`, `ops/dates-reconcile.py`, `backend/internal/server2` remote client and `ops/olcrtc-room.sh` only for explicit pre-cutover `MAESTRO_CONTROL_PLANE=legacy`. Both scripts place the mode guard before env/token/config reads, temp-file creation or subprocess/network calls and exit 3 for `rqlite` or any non-legacy value. HA installs only new `ops/ha/dates-audit.py` with `deploy/ha/maestro-dates-audit.service`; it has no apply code path.

In rqlite mode `main.go` never constructs the remote `server2.Client`, never exposes `OlcrtcRoomScript`, never starts `Provisioner.ReconcileExpiries`, and injects control-plane command services into both olcRTC admin/panel handlers. Those handlers commit `s3-olcrtc` desired state plus outbox in the canonical business transaction; they never mutate `olcconf` first and never execute `/bin/sh`, `ops/olcrtc-room.sh` or SSH. Static composition tests fail if any HA unit or rqlite handler references a legacy writer; cutover remains blocked until the legacy service/script fence and the zero-unowned-imported-user Naive gate both pass.

- [ ] **Step 7: Run tests and commit**

Run: `cd backend && go test -race ./internal/applyagent -count=1`

Run: `cd backend && go test -race ./internal/importer -run 'TestAppliedRunAttestation' -count=1`

Run: `cd backend && go test -race ./internal/api ./cmd/maestro-panel -run 'TestRqlite|TestLegacyOlc' -count=1`

Run: `python -m unittest ops.test_dates_reconcile_mode_guard ops.test_olcrtc_room_mode_guard`

Run: `python -m unittest discover -s ops/ha/tests -p 'test_s2_naive_adoption_manifest.py'`

```bash
git add backend/internal/applyagent backend/internal/importer/applied_run_attestation.go backend/internal/importer/applied_run_attestation_test.go backend/cmd/maestro-import/main.go backend/internal/api/olcrtc_admin.go backend/internal/api/panel.go backend/internal/api/olcrtc_ha_test.go backend/cmd/maestro-panel/main.go backend/cmd/maestro-panel/main_test.go ops/dates-reconcile.py ops/olcrtc-room.sh ops/ha/dates-audit.py ops/ha/s2-naive-adoption-manifest.py ops/ha/tests/test_s2_naive_adoption_manifest.py ops/ha/tests/fixtures/s2-naive-adoption-*.json ops/test_dates_reconcile_mode_guard.py ops/test_olcrtc_room_mode_guard.py deploy/ha/maestro-dates-audit.service
git commit -m "feat(agent): add local VPN and olcrtc drivers"
```

### Task 13: Crash-safe shared Telegram engine and fenced legacy-state replacement

**Files:**
- Create: `backend/internal/controlplane/telegram_schema.go`
- Create: `backend/internal/controlplane/telegram_schema_test.go`
- Create: `backend/internal/controlplane/migrations/0002_bot_state.sql`
- Modify: `backend/internal/controlplane/migrations_test.go`
- Create: `backend/internal/controlplane/telegram.go`
- Create: `backend/internal/controlplane/telegram_test.go`
- Create: `backend/internal/bot/telegram_api.go`
- Create: `backend/internal/bot/engine.go`
- Create: `backend/internal/bot/engine_test.go`
- Create: `backend/internal/bot/cutover.go`
- Create: `backend/internal/bot/cutover_test.go`
- Create: `backend/internal/bot/messages.go`
- Create: `backend/internal/bot/messages_test.go`
- Create: `backend/cmd/maestro-bot/main.go`
- Create: `backend/internal/api/bot_internal.go`
- Create: `backend/internal/api/bot_internal_test.go`
- Create: `backend/internal/bot/gateway.go`
- Create: `backend/internal/bot/gateway_test.go`
- Create: `backend/internal/bot/telegram_api_test.go`
- Create: `backend/internal/bot/qr.go`
- Create: `backend/internal/bot/qr_test.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Create: `ops/ha/bot-source-gate.py`
- Create: `ops/ha/bot-cutover-gate.py`
- Create: `ops/ha/tests/test_bot_source_gate.py`
- Create: `ops/ha/tests/test_bot_cutover_gate.py`
- Create: `ops/ha/tests/fixtures/bot-source-manifest-valid.json`
- Create: `ops/ha/tests/fixtures/bot-source-manifest-missing-source.json`
- Create: `ops/ha/tests/fixtures/bot-cutover-bundle-valid.json`
- Create: `ops/ha/tests/fixtures/bot-cutover-bundle-gap.json`
- Create: `ops/ha/tests/fixtures/bot-cutover-bundle-stale-fence.json`
- Create: `ops/ha/tests/fixtures/bot-cutover-bundle-running-poller.json`
- Create: `ops/ha/tests/fixtures/bot-cutover-bundle-expired-replacement.json`
- Modify: `deploy/vpn_bot_maestro_orders.py`
- Modify: `deploy/s2_multiproto_patch.py`

**Interfaces:**
- Produces: durable normalized `BotPollState` per stable `BotIdentityHMAC` derived from numeric Telegram `getMe.id`, versioned `TokenFingerprintHMAC` credentials/routes, `PendingCallback` rows with `pending|in_flight|completed|expired|replaced` states, global `TelegramBuyerIdentity`, idempotent `BotCutoverBundle` importer, one `maestro-bot` binary parameterized by `BOT_ID`/token secret, mTLS `BotGateway`, private inbox/lease/delivery API, shared owner/client flows and PNG QR renderer.
- Consumes: private mTLS bot API backed by `controlplane.Service`, canonical `suburl.Variants`, Telegram HTTPS API, two signed source manifests and one signed final-state capture per stable bot identity with its current credential version; deployment `BOT_ID` remains a routing alias only, and the bot binary has no direct rqlite/store access or local expiry/order/customer SQLite.

- [ ] **Step 1: Write the inbox, callback and cutover crash matrix before engine code**

For each inbox state `received -> command_committed -> delivery_queued -> completed` and each callback state `pending -> in_flight -> completed|expired|replaced`, crash and restart the engine. Exact RED tests are:

```go
func TestIngestBatchCommitsEveryUpdateBeforeOffsetAdvance(t *testing.T)
func TestDuplicateUpdateDoesNotRepeatBusinessCommand(t *testing.T)
func TestDuplicateCallbackIDDoesNotRepeatBusinessCommand(t *testing.T)
func TestFreshProcessRejectsStalePollerFence(t *testing.T)
func TestOneBotTokenHasOneActivePoller(t *testing.T)
func TestPollStateIsDurableAndKeyedByStableBotIdentity(t *testing.T)
func TestBotIDRenameKeepsOffsetFenceAndCallbacks(t *testing.T)
func TestTokenRotationSameBotPreservesOffsetFenceCallbacksAndOnePoller(t *testing.T)
func TestTokenRotationDifferentGetMeBotIsRejected(t *testing.T)
func TestRawBotTokenIsNeverPersistedOrLogged(t *testing.T)
func TestLeaseRenewFailureCancelsLongPollAndBusinessCalls(t *testing.T)
func TestCrashAtEveryInboxStateResumesFromCluster(t *testing.T)
func TestPendingAndInFlightCallbacksResumeFromCluster(t *testing.T)
func TestCutoverRejectsNewPollerBeforeFenceCaptureAndImport(t *testing.T)
func TestCutoverImportsFinalOffsetCallbacksAndClaimsOnce(t *testing.T)
func TestCrashAfterOffsetAdvanceImportsNonterminalInbox(t *testing.T)
func TestCutoverRejectsMissingUpdateCoverageBelowFinalOffset(t *testing.T)
func TestCutoverRejectsForgedStaleOrRunningLegacyFenceProof(t *testing.T)
func TestPaidClaimAtFenceBoundaryIsNeverMissed(t *testing.T)
func TestLegacyInlineCallbackRequiresImportedBinding(t *testing.T)
func TestOldAndReplacementCallbackConfirmExactlyOnce(t *testing.T)
func TestLegacyReplacementDeadlineUsesDBTimeAndCapsThirtyDays(t *testing.T)
func TestReplacementToNativeRejectsPendingCallbackOrPaidClaim(t *testing.T)
func TestLegacyInlineRejectedAfterReplacementDeadlineWithoutBusinessCall(t *testing.T)
func TestNativePhaseRejectsLegacyInlineEvenWithOldBinding(t *testing.T)
func TestAmbiguousTelegramSendMayRepeatTextButNotBusinessOperation(t *testing.T)
func TestCallbackBindingRejectsWrongBotIdentityActionOwnerAndExpiry(t *testing.T)
func TestUnknownBusinessOutcomeRetriesIdenticalSerializedCommand(t *testing.T)
func TestTelegramBuyerIdentityIsGlobalAcrossBotIDs(t *testing.T)
func TestSameBuyerAcrossBotsCannotDuplicateTrialOrCredit(t *testing.T)
func TestPrimaryAndSecondaryBotConcurrentCreateShareOneOrderAndOwnerClaim(t *testing.T)
func TestFirstReadyReceiptQueuesExactlyOneClientReady(t *testing.T)
func TestFurtherNodeReceiptsDoNotDuplicateClientReady(t *testing.T)
func TestInternalBotAPIRequiresMatchingBotCertificateSAN(t *testing.T)
func TestBotStateMigration0002IsChecksummedRepeatableAndFKClean(t *testing.T)
```

Assert:

- `(bot_identity_hmac, update_id)` and Telegram callback ID are durable/unique; `bot_identity_hmac = HMAC(identity_key, canonical numeric getMe.id)`, and deployment `BOT_ID` is absent from those keys;
- token fingerprint HMAC is only the current credential version/route; raw bot token is never persisted, logged, captured or placed in a manifest;
- offset advances only after durable inbox acceptance and lives in one normalized `BotPollState` row per stable bot identity;
- pending and in-flight callback rows retain serialized operation ID, source kind, fence, encrypted result/reference hash and completion time across restart;
- stale/expired poller stops `getUpdates` and makes zero business calls;
- bot-to-business command verifies the current stable-bot lease fence and credential version inside its transaction;
- new-poller lease fails until a fresh signed legacy hard-fence proof, final offset/callback/claim/inbox capture and matching import receipt are all durable;
- every accepted legacy update below the captured final offset has either a terminal proof or an imported nonterminal inbox row; an update at/above it is consumed by the new poller, so the fence boundary has no gap;
- an ambiguous Telegram delivery retries the same delivery operation and never repeats its business command;
- S2/S3/S4 copies can acquire the lease after expiry without overlapping pollers.

- [ ] **Step 2: Write one contract suite for both bot configurations and one global buyer**

Run identical fixtures through `bot-primary` and `bot-secondary`, then repeat them with the same canonical numeric Telegram user ID on both. Compute `buyer_hmac = HMAC(cluster_identity_key, "telegram:user:" + canonical_user_id)` without deployment `BOT_ID`, stable bot identity or token fingerprint; bot/chat/message addressing is a separate encrypted delivery route. The same buyer sees one account/order history and cannot receive a second trial, payment credit or generation by changing bots.

Client creates/selects tariff, sees amount/SBP/payment code/order ID, presses «Я оплатил», and only `MarkPaymentClaimed` executes. Owner callback is bound to stable bot identity/order/action/allowlisted owner/expiry; token credential version is audit/route metadata, not callback ownership. It first answers callback «Проверяю…», then calls canonical confirm/cancel and redraws from cluster truth. Legacy `moconf:<order_id>` and `mocancel:<order_id>` buttons are accepted only through an imported binding while `CutoverState == replacement` and before its DB-time deadline; `aclsub:` never emits a private URL and instructs the owner to reopen the current cluster-backed card.

Owner output contains amount, tariff snapshot, code, order ID, payment/provisioning status and operation ID; owner messages, audit, errors, logs and artifacts never contain SubToken/private URL/credentials/raw backend body. Client-ready key is `client-ready:<order_id>` and is queued only by the first matching ready receipt; its delivery route records the selected token/chat separately and may contain only that client's own query-free Maestro URL plus separate `?format=links` Karing/iPhone action.

- [ ] **Step 3: Run and verify RED**

Run: `cd backend && go test ./internal/bot ./internal/controlplane ./internal/api -run 'TestIngestBatch|TestDuplicateUpdate|TestDuplicateCallback|TestFreshProcess|TestOneBotToken|TestPollState|TestBotIDRename|TestTokenRotation|TestRawBotToken|TestLeaseRenewFailure|TestCrashAtEveryInbox|TestPendingAndInFlight|TestCutover|TestPaidClaimAtFence|TestLegacyInline|TestOldAndReplacement|TestLegacyReplacement|TestReplacementToNative|TestNativePhase|TestAmbiguousTelegramSend|TestCallbackBinding|TestUnknownBusinessOutcome|TestTelegramBuyer|TestSameBuyer|TestPrimaryAndSecondary|TestFirstReadyReceipt|TestFurtherNodeReceipts|TestInternalBotAPI|TestBotStateMigration|TestBotContract|TestOwner|TestClientReady' -count=1`

Expected: FAIL because poll state, cutover importer and engine are absent.

- [ ] **Step 4: Implement normalized stable-bot state and explicit schema migration**

Add checksummed numbered migration `0002_bot_state.sql`; never rewrite Plan 01's applied `0001`. It creates normalized poll, inbox, callback, terminal-proof, buyer, delivery-route, credential-version, cutover-bundle and hard-fence-receipt tables with foreign keys, unique business operation IDs and DB-time constraints. Migration tests apply `0001 -> 0002` twice, verify the recorded checksum, reject a changed checksum and run `foreign_key_check`.

```go
type BotPollState struct {
    BotIdentityHMAC, CurrentTokenFingerprintHMAC string
    CredentialVersion, NextUpdateID int64
    LeaseEpoch, LeaseFence int64
    CutoverState, CaptureSHA256 string
    ReplacementNotAfterUnix int64
}
type PendingCallback struct {
    CallbackHMAC, BotIdentityHMAC, OrderID, Action string
    SourceKind, State, BusinessOperationID, OwnerHMAC string
    ExpiresAtUnix, LeaseFence int64
    ResultCiphertext []byte
    ResultSHA256 string
    CompletedAtUnix int64
}
type CapturedInboxRow struct {
    UpdateID int64
    State, BusinessOperationID, PayloadSHA256 string
    SerializedUpdateCiphertext []byte
}
type TerminalUpdateProof struct {
    UpdateID int64
    BusinessOperationID, ResultSHA256 string
}
type LegacyPollerFenceProof struct {
    BotIdentityHMAC, HostIdentityHMAC, UnitName, UnitIncarnation string
    PIDInventorySHA256, ExporterHandoffSHA256, FenceNonce string
    CapturedAtUnix, NotAfterUnix int64
    UnitInactive, UnitMasked bool
    MainPID int64
    SignerKeyID string
    Signature []byte
}
type TelegramBuyerIdentity struct {
    BuyerHMAC string
}
type BotCutoverBundle struct {
    BotIdentityHMAC, TokenFingerprintHMAC string
    CredentialVersion, FinalNextUpdateID, ReplacementNotAfterUnix int64
    LegacyFenceProof LegacyPollerFenceProof
    NonterminalInbox []CapturedInboxRow
    TerminalUpdates []TerminalUpdateProof
    PendingCallbacks, InFlightCallbacks []PendingCallback
    PendingPaidClaimOperationIDs []string
    AcceptedUpdateSetSHA256, SourceStateSHA256 string
}
```

Store `BotPollState` by `BotIdentityHMAC = HMAC(identity_key, canonical numeric getMe.id)`, not token fingerprint and not deployment `BOT_ID`. `CurrentTokenFingerprintHMAC` identifies only the active credential route and monotonic `CredentialVersion` identifies its audited version. Store inbox, pending/in-flight callbacks, claims, delivery routes and global buyer identities in normalized encrypted/HMAC-backed tables. Raw bot tokens are memory-only secrets and never enter DB, logs, manifests or capture bundles.

- [ ] **Step 5: Implement hard-fenced import, one-poller engine and bounded replacement**

Implement audited CAS transitions `legacy_running -> fenced -> captured -> imported -> replacement -> native`, with `replacement -> blocked` at its deadline when unresolved work prevents `native`, and `blocked -> native` only after that work becomes terminal. The old host attestor issues `LegacyPollerFenceProof` only after the exact systemd unit is stopped and masked, `MainPID == 0`, a process inventory proves no matching poller, and the final exporter handoff digest is fixed. Verify its host signer, bot identity, unit incarnation, nonce, signature and at-most-60-second DB-time validity, then recheck inactive/masked/PID-zero immediately before granting the first new lease. Forged, stale, replayed or still-running evidence is a hard NO-GO.

After the hard fence, capture the stable bot identity, credential fingerprint/version, final durable next offset, every accepted update below that offset, every pending/in-flight callback and every paid claim into the signed `BotCutoverBundle`. Each accepted update must appear exactly once as either a terminal proof or a complete nonterminal inbox row with encrypted serialized update and stable business operation ID. If the old source cannot enumerate its exact accepted-update journal, or capture/import counts and canonical SHA-256 differ, cutover is NO-GO. Import the bundle idempotently in one transaction before any new poller lease. A repeated bundle digest is a no-op; a different digest for the same bot/fence is a conflict. This explicitly covers a crash after legacy offset advance but before business commit: the nonterminal row is imported and replayed with its original operation ID. Telegram updates never returned remain at or above `FinalNextUpdateID` for the new poller.

Token rotation is a separate audited CAS on the same stable bot identity: fence the old credential/poller, call Telegram `getMe` with the proposed credential in memory, require the same numeric bot ID/HMAC, carry forward offset/fence/callbacks/claims unchanged, increment credential version, then allow one new poller. A different `getMe.id`, an unfenced old poller or a raw token in durable input rejects rotation before state changes. No live token rotation is performed by this plan.

New callback data is a random `cb_...` token whose encrypted binding fixes stable bot identity/order/action/owner/expiry; token rotation does not invalidate it. Claiming a callback atomically moves `pending -> in_flight` with current fence and stable business operation ID; completion stores encrypted result or durable result reference plus its hash and completion time before any Telegram edit/answer. Restart resumes the same operation and result. Old inline and replacement callbacks for the same order/action share `confirm:<order_id>` or `cancel:<order_id>`, so simultaneous old+new delivery executes one canonical command.

Set the replacement hard deadline from database time in the import transaction and cap it at `imported_at_db + 30 days`; each imported callback expiry is the earlier valid source expiry or that hard deadline, and a missing/untrusted source expiry gets the hard deadline. Only imported `moconf:`/`mocancel:` bindings are accepted before that deadline while state is exactly `replacement`. At the deadline a DB transaction expires every remaining legacy binding and rejects all later inline callbacks before business lookup. Direct `replacement -> native` is rejected while any imported callback or paid-claim operation is pending/in-flight; the deadline instead moves the bot to `blocked` if such work remains. Those already accepted operations continue by durable operation ID, and the bot can enter `native` only after they are terminal. State `native` rejects legacy inline data even if an old binding row still exists.

Derive `TelegramBuyerIdentity` only from the canonical Telegram user ID and cluster identity key. Neither deployment `BOT_ID`, stable bot identity nor token fingerprint participates in buyer, entitlement, trial, order, payment or credit uniqueness. Delivery payload is encrypted; Telegram HTTPS uses normal certificate verification, bounded timeouts/body limits and errors that omit token/response body.

- [ ] **Step 6: Implement safe message/link rendering**

Primary Maestro QR/button uses query-free URL; Karing/iPhone uses exactly `?format=links`. Pin `github.com/skip2/go-qrcode` to `v0.0.0-20200617195104-da1b6568686e` and encode 512px PNG with Medium correction from exact URL bytes. Never advise `app=karing`; strip `app,format,device,platform` before switching. Harden tracked Python fragments by removing `verify=False`, owner private URL and exception-body disclosure, but do not deploy or count them as full bot sources.

- [ ] **Step 7: Add live-source, final-state capture and replacement gates**

`ops/ha/bot-source-gate.py` accepts exactly two signed manifests and exits `0=complete`, `2=production NO-GO`, `3=malformed/system`. Each manifest contains format version, bot alias, stable bot identity HMAC derived from numeric `getMe.id`, current token fingerprint HMAC/credential version (never the raw token), tracked repository path, source commit/tree SHA-256, entrypoint, dependency-lock path/hash, DB schema fingerprint, systemd unit path/hash, callback prefixes, owner-allowlist HMAC, current polling host, exact legacy state-exporter path/hash and capture time. Gate verifies paths at the named commit, locked dependencies, absence of `.env`/DB/token files and a shared contract-suite result.

`ops/ha/bot-cutover-gate.py` validates the signed final-state bundle and cluster import receipt for each stable bot identity. It verifies the fresh host-signed hard-fence proof (inactive and masked exact unit, matching incarnation, PID zero, process/exporter digests and nonce), equal bot identity/credential version/fingerprint/final offset, exact canonical accepted-update coverage, equal nonterminal inbox/terminal proof/pending callback/paid-claim counts and digests, and no new-poller lease before import. A missing update, forged/stale/running-poller proof, receipt mismatch or unknown old-source journal is production NO-GO. A token-rotation receipt additionally proves the in-memory credential returned the same numeric `getMe.id` and the old credential was fenced.

The gate uses database time for the maximum 30-day replacement deadline. Before the deadline it permits only imported legacy bindings; at/after it those bindings must be expired and legacy inline callbacks must be rejected without a business call. `native` is allowed only when every imported callback and paid claim is terminal; unresolved work yields `blocked`, never an unbounded acceptance window. Current two tracked fragments do not satisfy these gates; production enablement exits before token/service access until both full sources, exact accepted-update capture/import, parity and hard-fence evidence are proven.

- [ ] **Step 8: Run security/race and cutover-gate tests, then commit**

Run: `cd backend && go test -race ./internal/bot ./internal/controlplane ./internal/api -count=1 && go vet ./internal/bot ./internal/controlplane ./internal/api ./cmd/maestro-bot`

Run: `python -m unittest discover -s ops/ha/tests -p 'test_bot_*_gate.py'`

Run: `rg -n 'verify=False|app=karing|sub_url.*owner|exception' deploy backend/internal/bot`

Expected: no insecure TLS bypass, manual Karing suffix advice, owner private URL or raw exception rendering remains in executable bot paths; both cutover fixtures prove that an offset/callback/claim gap is a production NO-GO.

```bash
git add backend/internal/controlplane/telegram_schema.go backend/internal/controlplane/telegram_schema_test.go backend/internal/controlplane/migrations/0002_bot_state.sql backend/internal/controlplane/migrations_test.go backend/internal/controlplane/telegram.go backend/internal/controlplane/telegram_test.go backend/internal/api/bot_internal.go backend/internal/api/bot_internal_test.go backend/internal/bot backend/cmd/maestro-bot backend/go.mod backend/go.sum ops/ha/bot-source-gate.py ops/ha/bot-cutover-gate.py ops/ha/tests deploy/vpn_bot_maestro_orders.py deploy/s2_multiproto_patch.py
git commit -m "feat(bot): add crash-safe shared Telegram adapter"
```

## Plan 03 acceptance

- Desired state/outbox/generation/receipts/tombstones recover from missed events without reading node expiry as truth.
- Agent rejects stale epoch/incarnation/fence/generation by quorum check before side effect and again before swap.
- S2 full-config apply validates, atomically swaps, reloads once and rolls back last-good on failed health.
- A signed applied-run attestation binds Plan 02's complete imported rows and target digest to the exact `s2-naive` roster; a second signed versioned manifest binds that roster to a hard-fenced Caddy digest, so every imported pre-existing user is adopted once into `MTV-MANAGED` with credential bytes and cluster expiry preserved, zero unowned imported users is a hard cutover gate, and unrelated Caddy users remain byte-identical.
- S3 olcRTC changes, expiry and deletion flow through desired/outbox and the local `s3-olcrtc` agent; no post-cutover shell/SSH path is wired.
- Legacy expiry and olcRTC writers run only in explicit pre-cutover legacy mode and exit before secret/config reads in rqlite mode; HA deployment is audit-only.
- Checksummed migration `0002` adds the normalized bot state without changing applied `0001`; each stable Telegram bot identity has one `BotPollState`, independent of deployment alias and token rotation.
- A fresh host-signed hard-fence proof precedes an exact accepted-update journal import: every update below the final offset is terminal or imported nonterminal, so a crash after offset advance cannot miss a paid claim and no new poller starts early.
- Legacy inline and replacement callbacks share one business operation key, so an old+new duplicate confirms once; callback result is durable before Telegram side effects, and the DB-time replacement window ends within 30 days with legacy inline data rejected in `native`.
- Telegram buyer identity is global across both bots and independent of deployment `BOT_ID`, stable bot identity and token fingerprint; switching bots or rotating the same bot token cannot duplicate trial, payment credit or generation.
- Both bot configurations pass one crash/duplicate/fence/payment contract suite and never duplicate credit.
- Canonical bot links are correct and owner messages contain no private subscription URL.
- Generic bot/agent code is repository-ready, but live bot switch remains NO-GO until both exact live source inventories, state-import receipts and hard-fence evidence are present.
- Production services, tokens, VPN configs, DNS, OTA and TV UI/assets remain unchanged.
