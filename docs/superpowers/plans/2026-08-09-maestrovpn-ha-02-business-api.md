# MaestroVPN HA Business, API and Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Реализовать единую транзакционную бизнес-истину для клиентов, заказов, ручной оплаты, trial, web-панели и старых Android API без переключения production.

**Architecture:** HTTP, web и Telegram adapters вызывают один `controlplane.Service`. Все решения и side-effect intents фиксируются одной rqlite transaction, а повтор команды читает сохранённый результат. Legacy JSON остаётся отдельным default-режимом до cutover; importer и shadow verifier работают только с redacted fixtures/snapshots и никогда не включают dual-write.

**Tech Stack:** Go 1.25 standard library, пакет `internal/rqlite` из Plan 01, SQLite SQL, Android/JUnit только для URL contract regression, GitHub Actions.

## Global Constraints

- Выполнять только после GREEN Plan 01 на точном Git SHA.
- Никаких production import, DNS, deploy, service restart, OTA/Release или live bot actions.
- `MAESTRO_CONTROL_PLANE` принимает только `legacy` или `rqlite`; unset означает `legacy`.
- Legacy и rqlite stores не открываются одновременно для business writes; dual-write и fallback-write запрещены.
- Сумма, валюта и duration берутся только из immutable `tariff_versions`; client body их не определяет.
- Секреты шифруются, lookup выполняется HMAC; raw token/device/private URL не логируются.
- Старые routes, HTTP codes, JSON names и внешние order states `pending|paid` сохраняются.
- TV UI/assets, `TvEskizHome.kt`, `TvEskizSpec.kt`, `tvm_*`, D-pad/focus/Back не изменяются.
- Android/Gradle и 3-node тесты запускаются только GitHub Actions.
- `SourceEventID`, actor, channel and occurred-at are audited but excluded from canonical business hash.
- Authenticated HA admin mutations require `Idempotency-Key`; anonymous legacy Android order creation remains compatible without one.
- A new device admission during no-quorum returns 503 and never bypasses the committed device limit.
- Empty provider event/receipt values are stored as SQL `NULL`, not empty strings.

---

### Task 5: Cluster-backed read models, settings, sessions and audit

**Files:**
- Create: `backend/internal/controlplane/store.go`
- Create: `backend/internal/controlplane/store_test.go`
- Create: `backend/internal/controlplane/customers.go`
- Create: `backend/internal/controlplane/customers_test.go`
- Create: `backend/internal/controlplane/service.go`
- Create: `backend/internal/controlplane/health.go`
- Create: `backend/internal/controlplane/health_test.go`
- Modify: `backend/internal/controlplane/types.go`

**Interfaces:**
- Produces: `Service`, `CustomerByToken`, `CustomerByLogin`, `Tariffs`, `ApprovedOTA`, `CreateSession`, `Authorize`, `RevokeSessions`, `Readiness`.
- Consumes: `rqlite.RQLite`, `SecretBox`, injected clock/random source; no global mutable state.

- [ ] **Step 1: Write failing strong-read and secrecy tests**

Create table-driven tests that require:

```go
func TestCustomerByTokenUsesHMACAndLinearizableRead(t *testing.T)
func TestCustomerByTokenNeverSendsPlainTokenToSQLOrError(t *testing.T)
func TestClaimDeviceAtomicallyEnforcesLimitAndStoresOnlyHMAC(t *testing.T)
func TestConcurrentSameDeviceClaimIsIdempotent(t *testing.T)
func TestTariffSnapshotIsImmutable(t *testing.T)
func TestMutableSettingsUseVersionCASAndAudit(t *testing.T)
func TestAuditRejectsUpdateAndDelete(t *testing.T)
func TestSessionCookieContract(t *testing.T)
func TestRevocationEpochInvalidatesExistingSession(t *testing.T)
func TestSettingSecretReferenceCASIsAtomic(t *testing.T)
func TestPrincipalRolesAreNormalizedAndDefaultDeny(t *testing.T)
func TestMissingReferencedSecretKeyVersionMakesReadinessRed(t *testing.T)
```

The fake rqlite recorder must see only the token HMAC in parameters. Session output must be `Secure`, `HttpOnly`, `SameSite=Strict`, bounded by a 30-minute absolute TTL and a matching CSRF token hash. Authorization must be default-deny: `owner` may confirm/cancel/change critical settings; `admin` receives only the explicit read/provision permission set.

- [ ] **Step 2: Run and verify RED**

Run: `cd backend && go test ./internal/controlplane -run 'TestCustomer|TestClaimDevice|TestConcurrentSameDevice|TestTariff|TestMutableSettings|TestAudit|TestSession|TestRevocation' -count=1`

Expected: FAIL because the store/service methods are absent.

- [ ] **Step 3: Implement narrow read/write methods**

```go
type Store struct { db rqlite.RQLite; secrets *SecretBox; clock Clock }
type Service struct { store *Store; ids IDSource; clock Clock }
type Permission string
const (
    PermissionCustomerRead Permission = "customer.read"
    PermissionProvision Permission = "customer.provision"
    PermissionPaymentDecide Permission = "payment.decide"
    PermissionCriticalSettings Permission = "settings.critical"
)
```

Use linearizable reads for customer/order/payment decisions and a strong read for explicit post-commit verification. Claim/reset device performs one transaction: HMAC identity lookup, idempotent existing-device return, DB-enforced limit and audit; a concurrent new device above the limit loses without a row. Store only validated public olcRTC/VKTurn/OTA values in versioned `cluster_settings`, allowlist identities in normalized HMAC `setting_members`, encrypted secret envelopes in `setting_secrets`, and roles in default-deny `principal_roles`. Public value, member/secret reference, revocation epoch and audit row change under one version-CAS transaction; a referenced unavailable key version makes read/write readiness red. The OTA value contains versionCode, versionName, APK size, SHA-256 and source release ID. Store only encrypted subscription tokens/credentials and HMAC lookup keys.

- [ ] **Step 4: Implement bounded read/write readiness**

`Readiness.Read` verifies schema checksum, key versions, required tariff/settings rows and age of the last verified linearizable commit. `Readiness.Write` checks quorum, filesystem free-space signal from the local node, updates exactly one `health_write_canary` row with a random nonce in a committed transaction, then linearizable-reads that nonce. A rollback-only probe is forbidden.

Tests must cover quorum loss, disk-full/read-only signal, stale schema/settings, nonce mismatch and successful canary without unbounded row growth.

- [ ] **Step 5: Run focused tests and commit**

Run: `cd backend && go test ./internal/controlplane -run 'TestCustomer|TestClaimDevice|TestConcurrentSameDevice|TestTariff|TestMutableSettings|TestAudit|TestSession|TestRevocation|TestReadiness' -count=1`

```bash
git add backend/internal/controlplane
git commit -m "feat(controlplane): add cluster read models and readiness"
```

### Task 6: Deterministic legacy snapshot importer and shadow digest

**Files:**
- Create: `backend/internal/importer/model.go`
- Create: `backend/internal/importer/decoder.go`
- Create: `backend/internal/importer/validate.go`
- Create: `backend/internal/importer/digest.go`
- Create: `backend/internal/importer/importer_test.go`
- Create: `backend/internal/importer/resume_test.go`
- Create: `backend/internal/importer/testdata/customers-valid.json`
- Create: `backend/internal/importer/testdata/orders-pending-credited.json`
- Create: `backend/internal/importer/testdata/collisions.json`
- Create: `backend/internal/importer/testdata/bot-bindings-v1.json`
- Create: `backend/internal/importer/testdata/settings-principals-v1.json`
- Create: `backend/internal/importer/testdata/full-then-delta/`
- Create: `backend/cmd/maestro-import/main.go`
- Create: `ops/ha/shadow-verify.sh`

**Interfaces:**
- Produces: `DecodeSnapshot`, `Validate`, `Plan`, `Apply`, `Digest`, immutable JSON report and exit codes `0=clean`, `2=blockers`, `3=input/system error`.
- Consumes: versioned normalized snapshots; Task 5 store; no SSH, live API or implicit file discovery.

- [ ] **Step 1: Write failing fixture tests**

Require deterministic results for repeated runs and explicit blockers for truncated JSON, duplicate/case-colliding login, duplicate raw/HMAC token, UUID/SubID/credential collision, contradictory expiry and unsupported bot schema fingerprint. A `pending+credited` legacy order becomes internal `confirmed/pending` with an audited `legacy_credit_preserved` marker and the exact already-stored customer expiry; it never increments expiry again. Uncredited pending remains `created`.

```go
func TestPlanPreservesExactLegacyIdentityBytesAndExpiry(t *testing.T)
func TestPlanReportsEveryCollisionInStableOrder(t *testing.T)
func TestPendingCreditedPreservesExpiryWithoutSecondCredit(t *testing.T)
func TestApplyTwiceHasSameCountsAndDigest(t *testing.T)
func TestUnsupportedBotSnapshotIsBlocking(t *testing.T)
func TestRequiredSettingsPrincipalsAndEncryptedSecretsArePreserved(t *testing.T)
func TestMissingLegacyPrincipalSecretBlocksApply(t *testing.T)
func TestCrashAtEveryBatchBoundaryResumesSameDigest(t *testing.T)
func TestDifferentDigestCannotResumePartialRun(t *testing.T)
func TestDeltaRequiresExactAppliedParentDigest(t *testing.T)
func TestDeltaDeletionCreatesTombstone(t *testing.T)
func TestFullThenDeltaEqualsFreshFinalFullDigest(t *testing.T)
func TestConcurrentResumeAppliesEachBatchOnce(t *testing.T)
func TestDeltaWithoutExplicitDeleteMarkerBlocks(t *testing.T)
```

- [ ] **Step 2: Run and verify RED**

Run: `cd backend && go test ./internal/importer -count=1`

Expected: FAIL because package is absent.

- [ ] **Step 3: Implement a versioned normalized input contract**

```go
type Snapshot struct {
    FormatVersion      int                     `json:"format_version"`
    SnapshotKind       string                  `json:"snapshot_kind"` // full|delta
    ParentSourceDigest string                  `json:"parent_source_digest,omitempty"`
    CapturedAt         time.Time               `json:"captured_at"`
    SourceHashes       map[string]string       `json:"source_hashes"`
    Customers          []LegacyCustomer        `json:"customers"`
    Orders             []LegacyOrder           `json:"orders"`
    Trials             []LegacyTrial           `json:"trials"`
    BotBindings        []LegacyBotBinding      `json:"bot_bindings"`
    Settings           []LegacySetting         `json:"settings"`
    Principals         []LegacyPrincipal       `json:"principals"`
    EncryptedSecrets   []LegacyEncryptedSecret `json:"encrypted_secrets"`
    Deletes            []LegacyDelete          `json:"deletes,omitempty"`
    BotPollStates      []LegacyBotPollState    `json:"bot_poll_states"`
    PendingCallbacks   []LegacyCallback        `json:"pending_callbacks"`
    BotCredentialRotations []LegacyBotCredentialRotation `json:"bot_credential_rotations,omitempty"`
}
type LegacyBotPollState struct {
    BotIdentityHMAC string
    CurrentTokenFingerprintHMAC string
    CredentialVersion int
    NextUpdateID int64
    CapturedFence uint64
}
type LegacyCallback struct {
    BotIdentityHMAC string
    TokenFingerprintHMAC string
    CredentialVersion int
    CallbackHMAC string
    OrderID string
    Action string
    State string
}
type LegacyBotCredentialRotation struct {
    BotIdentityHMAC, OldTokenFingerprintHMAC, NewTokenFingerprintHMAC string
    OldCredentialVersion, NewCredentialVersion int
    AuditDigest string
}
type Report struct {
    SourceDigest, PlanDigest string
    Counts map[string]int
    Blockers []Blocker
}
```

The CLI requires explicit `--snapshot`, `--report` and `--mode=dry-run|apply`. `apply` additionally requires an exact previously produced `--expected-plan-digest` and protected decryption/key inputs; it aborts on any blocker, source hash drift or target/base mismatch. The normalized contract includes exact S1 `customers.json`, `orders.json`, `trials.json`, `panel-pw.hash`, legacy bearer principal, `olcrtc.json`, `wb.token`, VKTurn config/allowlists and approved OTA bytes, plus for each bot the cluster-HMAC of numeric `getMe.id`, current token fingerprint HMAC/credential version, final offset/fence, normalized pending/in-flight callbacks/paid claims, S2 bindings and S1/S3/S4 node exports. Raw bot IDs/tokens never enter reports or cluster rows. Two token fingerprints claiming the same or different stable bot identity are blocking unless a signed audited same-`getMe.id` rotation chain has strictly increasing credential versions; the digest binds that chain and rejects forks/collisions. Public values retain exact bytes/version; bearer/WB/transport key material is sealed in `EncryptedSecrets` and never plaintext JSON. Unknown live SQLite schemas or incomplete bot captures block production.

- [ ] **Step 4: Preserve exact values and make writes idempotent**

Keep original login casing/bytes, UUID, SubID, SubToken, credentials, absolute expiry, principal roles/hashes and setting bytes. Generate only internal IDs deterministically from source namespace plus original stable key. A full run requires an empty business target. A delta is an explicit list of typed upserts plus `Deletes{entity_kind,stable_source_key,expected_prior_digest}`; it requires `parent_source_digest` to equal the fully applied base while global writes remain frozen. A missing delete marker may never be inferred from omission. Customer/service deletes create durable desired-state tombstones, never silent row removal. The business digest excludes importer bookkeeping, so `full(base)+delta` must equal a fresh full import of the same final source.

`import_runs` records `applying|applied`, source/plan/parent/target digests and deterministic batch count. Each batch has the stable identity `(run_id,batch_index,batch_digest)` and its `applying -> applied` row plus data changes commit in one transaction; a crash before commit leaves no applied batch, a crash after commit resumes at the first missing index. Concurrent resumes contend on that identity and apply each batch once. The same source/plan digest becomes a no-op only after every batch and the final business digest are verified. A different digest cannot resume a partial run. No external writer is enabled during full/delta apply. Write no plaintext secrets to report/logs.
Legacy `trials.json` already contains HMACs, not raw anchors. Apply therefore requires the old `MAESTRO_TRIAL_SALT` through a separate protected key file, stores it encrypted as lookup key version 1 and checks both current and legacy HMACs. Missing/mismatched legacy salt is an importer blocker and never appears in report/artifacts.


- [ ] **Step 5: Add redacted shadow comparison**

`ops/ha/shadow-verify.sh` accepts two explicit exported digest files, compares customer/order count, HMAC identity, absolute expiry, generation, protocol tags, node set, settings/principal fingerprints, Maestro URL shape, Karing URL shape and exact OTA manifest. Output contains IDs hashed with a run-local salt and exits `2` on any difference. It never contacts production itself.

- [ ] **Step 6: Run tests, syntax check and commit**

Run: `cd backend && go test ./internal/importer -count=1`

Run: `bash -n ops/ha/shadow-verify.sh`

```bash
git add backend/internal/importer backend/cmd/maestro-import ops/ha/shadow-verify.sh
git commit -m "feat(ha): add deterministic legacy importer"
```

### Task 7: Exactly-once orders, payment decisions and time-based transitions

**Files:**
- Create: `backend/internal/controlplane/orders.go`
- Create: `backend/internal/controlplane/orders_test.go`
- Create: `backend/internal/controlplane/orders_integration_test.go`
- Create: `backend/internal/controlplane/sweeper.go`
- Create: `backend/internal/controlplane/sweeper_test.go`
- Modify: `backend/internal/controlplane/service.go`
- Modify: `backend/internal/controlplane/types.go`

**Interfaces:**
- Produces: `CreateOrder`, `MarkPaymentClaimed`, `ConfirmPayment`, `CancelOrder`, `OrderByID`, `ExpireDueOrders`, `ExpireDueCustomers`, `RunExpirySweep`.
- Consumes: immutable tariff rows, idempotency table, desired-state/outbox rows; provisioning never runs inside the HTTP handler.

- [ ] **Step 1: Write the RED concurrency matrix**

Against the isolated 3-node cluster add exact tests:

```go
func TestHundredConcurrentSameConfirmCreditsOnce(t *testing.T)
func TestSameKeyDifferentHashReturnsConflict(t *testing.T)
func TestLostResponseAfterCommitReturnsSavedResultAfterRestart(t *testing.T)
func TestTwoDifferentPaidOrdersBothExtendExpiry(t *testing.T)
func TestConfirmVersusCancelHasOneTerminalWinner(t *testing.T)
func TestReceiptReferenceCannotCreditSiblingOrder(t *testing.T)
func TestBotActiveOrderGuardReturnsExistingOrder(t *testing.T)
func TestDuplicatePaidClaimCreatesOneOwnerEvent(t *testing.T)
func TestNoQuorumMutationReturnsUnavailableWithoutPendingWrite(t *testing.T)
func TestActorChannelSourceEventAndTimeDoNotChangeHash(t *testing.T)
func TestExpiredCustomerRenewsFromConfirmedAt(t *testing.T)
func TestCallerCannotOverrideTariffSnapshot(t *testing.T)
func TestSameTelegramBuyerAcrossBothBotsSharesActiveGuard(t *testing.T)
func TestOrderExpiresAfterTwentyFourHoursAndReleasesGuard(t *testing.T)
func TestPaymentClaimedDoesNotAutoExpireBeforeOwnerDecision(t *testing.T)
func TestExpireVersusConfirmHasOneTerminalWinner(t *testing.T)
func TestCustomerExpirySweepRevokesEveryServiceOnce(t *testing.T)
func TestExpirySweepVersusRenewLatestGenerationWins(t *testing.T)
func TestSweeperLeaseHasOneActiveHolder(t *testing.T)
func TestStaleSweeperFenceCannotExpireAfterLeaseHandoff(t *testing.T)
func TestSweeperNoQuorumDoesNoSideEffect(t *testing.T)
func TestSweeperCrashAfterCommitDoesNotIncrementAgain(t *testing.T)
func TestSavedIdempotencyResponseContainsNoPrivateMaterial(t *testing.T)
```

For the first test assert exactly one `payments` row, one expiry delta, one generation increment and one unique outbox row per configured non-retired desired target service, including fenced/down S1. For two different paid orders assert both tariff durations are added serially. For all failure cases compare customer/order/payment/outbox counts before and after.

- [ ] **Step 2: Run in HA CI and verify RED**

Run: `cd backend && go test -tags=rqlite_integration ./internal/controlplane -run 'TestHundred|TestSameKey|TestLostResponse|TestTwoDifferent|TestConfirmVersus|TestReceipt|TestBotActive|TestDuplicatePaid|TestNoQuorumMutation|TestActorChannel|TestExpiredCustomer|TestCallerCannot|TestSameTelegramBuyer|TestOrderExpires|TestExpireVersus|TestCustomerExpirySweep|TestExpirySweepVersus|TestSweeper' -count=1`

Expected: FAIL because order commands are absent.

- [ ] **Step 3: Implement canonical command hashing and active-order creation**

For payment decisions hash canonical JSON containing only:

```json
{"decision":"confirm","order_id":"ord_...","payment_reference":"...","tariff_version":"tariff_1m_v1"}
```

Sort/fix field order in code; actor, channel, `SourceEventID`/callback, timestamps and proposed IDs are audit metadata. Creating a Telegram/authenticated-customer order inserts the order and `active_order_guards` in one transaction. For Telegram the guard scope is `telegram_user` and its HMAC is derived from raw `from_user.id` only, independent of `BOT_ID` and chat; both bots therefore return the same active order and create one `owner-claim:<order_id>`. The winning order records only its origin bot for client delivery. A guard conflict linearizable-reads and returns the existing non-terminal order. Anonymous legacy Android receives a new intent because it supplies no stable identity; owner views mark same-amount sibling intents and receipt uniqueness prevents double confirmation.

- [ ] **Step 4: Implement one first-writer confirm transaction**

A bounded attempt may linearizable-read immutable order/customer generation only to propose expected expiry/generation/response; that read never selects the winner. Submit one `/db/request?transaction` batch whose first statement inserts `(scope,command_type,idempotency_key,request_hash,operation_id,decision)`. The schema trigger validates `payment_claimed` only after the UNIQUE claim wins. In the same transaction:

1. insert one payment using the order's immutable amount/currency and unique provider event/receipt;
2. set order `payment_state=confirmed`, `provisioning_state=pending`, one `confirmed_at`;
3. update the current customer row with `expires_at_unix=max(expires_at_unix,confirmed_at)+tariff.duration_seconds` and `generation=generation+1`;
4. upsert absolute desired state and unique outbox rows for every non-retired S1–S4 desired target service; a fence suppresses apply, never creation of catch-up state;
5. remove the active-order guard;
6. append actor/channel/result-hash audit;
7. write `status=applied` plus only stable IDs/redacted fields, or a row-bound encrypted response envelope; never persist plaintext SubToken, private URL or protocol credential in idempotency response JSON.

Customer expiry updates with `WHERE generation=:expected_generation`; the final DB assertion verifies the resulting expiry/generation, payment, desired rows, outbox and protected saved response before `status=applied`. The adapter materializes a client URL only after its own authorized token decryption; logs/audit/replay rows remain redacted. A definite generation conflict rolls back the whole batch and performs a bounded fresh pre-read/retry because a different paid order legitimately won. A transport-unknown write is never replayed: linearizable operation-row read returns the protected saved response for the same hash, `ErrConflict` for a different hash and retryable 503 when no committed outcome can be proved. A UNIQUE duplicate follows the same read. No pre-read selects the winner and zero-row CAS is never success.

- [ ] **Step 5: Implement paid-claim and cancel with the same rules**

`MarkPaymentClaimed` changes only `created -> payment_claimed` and creates unique encrypted Telegram delivery `owner-claim:<order_id>`; the HTTP handler never calls Telegram directly. Duplicate update returns the existing state/event. `CancelOrder` competes through the same UNIQUE-command/trigger mechanism, allows only `created|payment_claimed -> canceled`, preserves evidence, removes every guard and never changes expiry. Confirmed payment cannot be canceled even if provisioning is pending/degraded. Before create/get/claim/confirm, a lazy DB-time CAS terminalizes only a due unclaimed `created` order after 24 hours and releases its guards; a `payment_claimed` order is retained until explicit owner confirm/cancel and emits one idempotent stale-claim owner alert after 24 hours. The scheduled path uses the identical commands. Public `GET /order/<id>` keeps the old external contract by returning 404 for canceled/expired orders instead of exposing a new state.

- [ ] **Step 6: Implement the single-active expiry sweeper**

Only when the cluster phase is `active`, workers compete for DB-time `cluster_job_leases(job='expiry-sweeper')` with a 90-second lease, 30-second renewal and 60-second scan. Every bounded expiry transaction begins with a DB assertion that the same worker still owns the unexpired monotonic lease fence in that exact request; a stale holder after handoff cannot change data. A customer with `status=active AND expires_at_unix<=unixepoch()` becomes `expired`, increments generation once and writes service-revoke desired state/outbox for every non-retired target. Only due `created` orders become terminal `expired`; `payment_claimed` produces an owner alert and stays decidable. Renewal/confirm and expiry use generation/state CAS, so whichever commits later emits the latest absolute desired state. Crash after commit resumes without a second generation; no quorum or lost lease performs no local side effect. The panel's legacy-named `delete_expired` action calls this same sweep and never hard-deletes customer credentials. The legacy reconciler remains active only in legacy mode; rqlite mode starts this sweeper instead.

- [ ] **Step 7: Run race/fault tests and commit**

Run: `cd backend && go test -race ./internal/controlplane -count=1`

Run: `cd backend && go test -tags=rqlite_integration ./internal/controlplane -run 'TestHundred|TestSameKey|TestLostResponse|TestTwoDifferent|TestConfirmVersus|TestReceipt|TestBotActive|TestDuplicatePaid|TestNoQuorumMutation|TestActorChannel|TestExpiredCustomer|TestCallerCannot|TestSameTelegramBuyer|TestOrderExpires|TestPaymentClaimed|TestExpireVersus|TestCustomerExpirySweep|TestExpirySweepVersus|TestSweeper|TestStaleSweeper|TestSavedIdempotency' -count=1`

```bash
git add backend/internal/controlplane
git commit -m "feat(controlplane): make manual payment exactly once"
```

### Task 8: Trial and compatibility adapter with exclusive runtime mode

**Files:**
- Create: `backend/internal/controlplane/trials.go`
- Create: `backend/internal/controlplane/trials_test.go`
- Create: `backend/internal/api/controlplane_port.go`
- Create: `backend/internal/api/controlplane_port_test.go`
- Create: `backend/internal/api/legacy_contract_test.go`
- Create: `backend/internal/api/panel_contract_test.go`
- Create: `backend/internal/api/panel_dashboard_test.go`
- Create: `backend/internal/controlplane/status.go`
- Create: `backend/internal/controlplane/status_test.go`
- Create: `backend/internal/controlplane/external_actions.go`
- Create: `backend/internal/controlplane/external_actions_test.go`
- Modify: `backend/internal/api/api.go`
- Modify: `backend/internal/api/order.go`
- Modify: `backend/internal/api/admin.go`
- Modify: `backend/internal/api/trial.go`
- Modify: `backend/internal/api/olcrtc_admin.go`
- Modify: `backend/internal/api/vkturn.go`
- Modify: `backend/internal/api/vkturn_panel.go`
- Modify: `backend/internal/api/panel.go`
- Modify: `backend/internal/api/panel_ui.go`
- Modify: `backend/cmd/maestro-panel/main.go`

**Interfaces:**
- Produces: `api.Business` adapter and mutually exclusive composition roots.
- Consumes: the existing `Provisioner`/JSON stores only in legacy mode; `controlplane.Service` only in rqlite mode.

- [ ] **Step 1: Freeze the legacy HTTP contract before refactoring**

Build a route-registration snapshot and golden `httptest` cases for `/healthz`, `/sub/<token>`, `/sub/<token>/info`, `/sub/<token>/helpers`, `/claim`, `/order/tariffs`, `/order`, `/order/<id>`, `/order/paid-claim`, `/trial`, `/update`, `/report`, every `/admin/*` route and every current panel route. The test fails if a registered route/action is absent from either adapter. Cover panel login/logout/me/password, customers/customer/stats, all `provision|extend|renew|set_expiry|reset_devices|disable|enable|delete|delete_expired` actions, olcRTC read/room/grant/WB token/WB room and VKTurn read/save/enable. Assert method/status/content-type and exact existing required JSON fields:

- customer: `login,sub_url,expires,active,protocols`;
- order create: `order_id,code,rub,days,tariff,sbp_phone,pay_url,status`;
- order poll: `order_id,status,rub,code` and conditional `sub_url`;
- `/helpers` is `200 {}`, `/healthz` is `ok <BuildCommit>`, expired `/sub` is `402`, `/report` is best-effort `204`.
- canceled/expired `GET /order/<id>` remains 404; HA `/claim` returns 404 for an unknown login and never auto-backfills external panels;
- a new device whose admission cannot be committed returns 503, while an already committed device may use a valid cached subscription snapshot.

- [ ] **Step 2: Write RED parity and mode tests**

Feed the same fixture through legacy and rqlite adapters and compare all stable fields/statuses. Exact tests `TestRQLiteRouteActionSnapshotIsComplete`, `TestExtendAndRenewRemainDistinct`, `TestRQLiteBulkImportReturns410WithoutMutation` and `TestRQLiteModeHasNoLegacyStoreProvisionerExecOrSSH` cover the complete current registration/action set. Require startup failure for an unknown mode or incomplete rqlite TLS/crypto configuration. Require unset mode to open legacy only; require rqlite mode not to open/mutate JSON stores, construct a legacy `Provisioner`, invoke `os/exec`/shell/SSH, or fall back after a 503.

- [ ] **Step 3: Implement the narrow adapter**

```go
type PanelAuth interface {
    CreateSession(context.Context, CreateSessionCommand) (SessionView, error)
    Authorize(context.Context, AuthorizeCommand) (PrincipalView, error)
    RevokeSessions(context.Context, RevokeSessionsCommand) error
    ChangePrincipalPassword(context.Context, ChangePasswordCommand) error
}
type Business interface {
    PanelAuth
    CustomerByToken(context.Context, string) (CustomerView, error)
    CustomerByLogin(context.Context, string) (CustomerView, error)
    ListCustomers(context.Context, CustomerFilter) ([]CustomerView, error)
    CustomerStats(context.Context) (CustomerStatsView, error)
    CustomerUsage(context.Context, string) (CustomerUsageView, error)
    Tariffs(context.Context) ([]TariffView, error)
    ApprovedOTA(context.Context) (OTAManifestView, error)
    CreateOrder(context.Context, CreateOrderCommand) (OrderView, error)
    OrderByID(context.Context, string) (OrderView, error)
    ListOrders(context.Context, OrderFilter) ([]OrderView, error)
    MarkPaymentClaimed(context.Context, ClaimPaymentCommand) (OrderView, error)
    ConfirmPayment(context.Context, ConfirmPaymentCommand) (ConfirmPaymentResult, error)
    CancelOrder(context.Context, CancelOrderCommand) (OrderView, error)
    SubscriptionSnapshot(context.Context, string) (SubscriptionSnapshot, error)
    TouchDevice(context.Context, TouchDeviceCommand) (DeviceDecision, error)
    RedeemTrial(context.Context, RedeemTrialCommand) (CustomerView, error)
    ProvisionCustomer(context.Context, ProvisionCustomerCommand) (CustomerView, error)
    ExtendCustomer(context.Context, ExtendCustomerCommand) (CustomerView, error)
    RenewCustomer(context.Context, RenewCustomerCommand) (CustomerView, error)
    SetCustomerExpiry(context.Context, SetExpiryCommand) (CustomerView, error)
    DisableCustomer(context.Context, CustomerStateCommand) (CustomerView, error)
    EnableCustomer(context.Context, CustomerStateCommand) (CustomerView, error)
    DeleteCustomer(context.Context, DeleteCustomerCommand) error
    RunExpirySweep(context.Context, ExpirySweepCommand) (OperationView, error)
    ResetDevices(context.Context, ResetDevicesCommand) error
    ReconcileServices(context.Context, ReconcileServicesCommand) (OperationView, error)
    ReadSetting(context.Context, string) (SettingView, error)
    UpdateSetting(context.Context, UpdateSettingCommand) (SettingView, error)
    OLCRTCState(context.Context) (OLCRTCView, error)
    SetOLCRTCRoom(context.Context, SetOLCRTCRoomCommand) (SettingView, error)
    SetOLCRTCGrant(context.Context, SetOLCRTCGrantCommand) (SettingView, error)
    WBTokenStatus(context.Context) (SecretStatusView, error)
    SetWBToken(context.Context, SetSecretCommand) error
    RequestWBRoom(context.Context, RequestWBRoomCommand) (ExternalActionView, error)
    VKTurnState(context.Context) (VKTurnView, error)
    UpdateVKTurn(context.Context, UpdateVKTurnCommand) (SettingView, error)
    SetVKTurnEnabled(context.Context, SetVKTurnEnabledCommand) (SettingView, error)
    ClusterStatus(context.Context) (ClusterStatusView, error)
    RecentAudit(context.Context, AuditFilter) ([]AuditView, error)
    MigrateServiceEndpoint(context.Context, MigrateEndpointCommand) (OperationView, error)
}
```

Keep `api.New(...)` as the legacy constructor so current tests/callers compile; add `api.NewControlPlane(...)` for HA. In `main.go`, branch once during startup. `legacy` retains current behavior exactly; `rqlite` builds only cluster-backed dependencies and disables the old startup/ticker expiry reconciler and direct multi-node `Provisioner` writes.

- [ ] **Step 4: Route every owner/admin write through canonical commands**

Add RED tests for concurrent `/admin/provision`, `/extend`, `/renew`, `/set-expiry`, `/reset-devices`, every backfill/migrate route and every panel action listed in Step 1 on two panel instances. Every authenticated HA mutation requires `Idempotency-Key`; the server derives its canonical command type/scope and refuses a reused key with a different hash. Provision/extend/renew/set-expiry/disable/enable/delete/reset use distinct canonical transactions that update absolute customer state, generation, desired state, outbox and a protected saved response; tests prove `extend` preserves the current legacy semantics and is not silently mapped to `renew`. The legacy-named `delete_expired` action calls `RunExpirySweep` and preserves customer identity/credentials. In rqlite mode `/admin/bulk-import` returns 410 with no mutation because import is offline CLI-only during a write freeze; backfill/migrate may reconcile only already-canonical customer rows and may not rediscover/import an external panel. Password change, olcRTC/VKTurn/allowlist and WB-token changes use one version CAS with encrypted secret reference, session revocation and audit. No HA handler opens a legacy store, calls `Provisioner`, executes a shell or uses SSH.

Room/grant changes write `s3-olcrtc` desired state/outbox; the local agent in Plan 03 applies them. `RequestWBRoom` first commits one `external_actions` row with durable `pending -> attempt_started -> succeeded|unknown` state under a fenced cluster-job lease. In the same request immediately before network I/O, the worker asserts its current unexpired fence and commits `attempt_started`; only then may it send the current WB create-room POST once. A crash before that marker is retryable. Any takeover that sees `attempt_started`, including crash before send, after provider acceptance or before result commit, stores `unknown` and never sends again for that action key. A returned room ID is assigned in one canonical room transaction; the owner may inspect unknown state and explicitly create a distinct replacement action. Tests `TestExternalActionCrashBoundariesPostAtMostOnce`, `TestExternalActionStaleLeaseCannotSend` and `TestExternalActionReplacementUsesNewKey` cover every boundary and count provider POSTs. Neither panel instance executes `/bin/sh`, SSH or S3 systemd directly.

- [ ] **Step 5: Make trial one transaction**

Canonicalize anti-abuse identities to current HMAC plus imported legacy key-version HMACs before storage. One transaction claims idempotency/anti-abuse identity, creates or reuses exactly one customer, writes absolute expiry/generation, desired state/outbox and saved response. Concurrent anchors/DRM identities that resolve through either key version return that response. Quorum loss returns 503 and creates neither customer nor local pending ledger.

- [ ] **Step 6: Gate externally visible `paid` on usable provisioning**

Keep internal payment/provisioning axes separate. Order poll exposes legacy `paid` only when payment is confirmed, canonical `/sub` validates and at least one currently enabled VPN service has `node_apply_receipts.generation >= order.result_generation`. A newer receipt must keep an older paid order usable; exact equality is forbidden. Otherwise it stays legacy `pending`; owner data adds `payment_state`, `provisioning_state`, ready/degraded nodes and operation ID.

- [ ] **Step 7: Run compatibility suites and commit**

Run: `cd backend && go test ./internal/api ./internal/controlplane -count=1`

Run: `cd backend && go test -race ./internal/api ./internal/controlplane -count=1`

```bash
git add backend/internal/api backend/internal/controlplane backend/cmd/maestro-panel/main.go
git commit -m "feat(api): add compatible HA control-plane mode"
```

### Task 9: Canonical subscription URLs, web RBAC and no-quorum reads

**Files:**
- Create: `backend/internal/suburl/url.go`
- Create: `backend/internal/suburl/url_test.go`
- Create: `backend/internal/controlplane/endpoint_migration.go`
- Create: `backend/internal/controlplane/endpoint_migration_test.go`
- Modify: `backend/internal/api/api.go`
- Modify: `backend/internal/api/panel.go`
- Modify: `backend/internal/api/panel_ui.go`
- Modify: `app/src/main/java/com/maestrovpn/tv/utils/MaestroSub.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/utils/MaestroSubTest.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/IosKaringDialog.kt`
- Create: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/IosKaringLinkTest.kt`

**Interfaces:**
- Produces: `suburl.Variants(base, token) {Maestro, Links}`, `MigrateServiceEndpoint`, canonical query stripping, `/livez`, `/readyz/read`, `/readyz/write`, cluster-backed owner/admin sessions and redacted owner operations dashboard.
- Consumes: `subgen.GenerateSingbox`, `subgen.ShareLinks`, Task 5 readiness/store.

- [ ] **Step 1: Write the URL matrix RED tests**

Cover absent/repeated/mixed-case `app`, `format`, `device`, `platform`, unrelated query keys and fragments. Output rules are exact:

```text
Maestro = https://wapmixx.ru:8911/sub/<token>
Links   = https://wapmixx.ru:8911/sub/<token>?format=links
```

Variant/device keys are removed before selecting a variant; no device metadata is forwarded. Incoming `?app=karing` remains a server-side alias for links. The primary QR/button is always query-free; a separate Karing/iPhone action uses only `?format=links`.

- [ ] **Step 2: Implement one URL builder and use it at backend boundaries**

Replace hand-built concatenation in `api/order.go`, `api/admin.go` and `api/panel.go` with `suburl.Variants`. Preserve `subgen.GenerateSingbox` and `subgen.ShareLinks` output bytes. Adjust `MaestroSub.stripDeviceMetadata` and only the link-building helper used by `IosKaringDialog` so old/repeated Karing parameters cannot leak into the Maestro import path. Do not change dialog layout, any TV UI/assets or screen geometry.

- [ ] **Step 3: Migrate the S1 VLESS endpoint before any apex switch**

Write these RED tests before the command:

```go
func TestMigrateS1VLESSEndpointPreservesCredentialsAndChangesOnlyHost(t *testing.T)
func TestS1VLESSMigrationIsIdempotentAndInvalidatesSnapshots(t *testing.T)
func TestS1VLESSMigrationCrashResumesSameOperation(t *testing.T)
func TestS1VLESSMigrationRejectsUnexpectedOldHostOrUnverifiedProof(t *testing.T)
func TestGeneratedSubscriptionsContainNoApexVLESS443(t *testing.T)
func TestLegacyApexVLESSHasIndependentFallback(t *testing.T)
func TestApexReadinessFailsUntilS1VLESSDigestClean(t *testing.T)
func TestS1DownAliasRecordAndFallbackAllowApexGate(t *testing.T)
```

Implement a resumable deterministic operation for `MigrateServiceEndpoint{Service:"s1-vless", ExpectedFrom:"wapmixx.ru", To:"s1-vless.wapmixx.ru"}`. It requires a signed provider-read proof that the exact alias resolves only to the immutable inventoried S1 address and that the lowered TTL wait elapsed; any unexpected record/address blocks. Because S1 is currently down and VLESS-Reality does not require a certificate for this alias, live S1/TLS reachability is explicitly not an apex rescue prerequisite: the endpoint stays `degraded/pending` until S1 returns, while proven S2/S3/S4 fallbacks carry clients. The command rejects any unexpected old endpoint and changes only the S1 VLESS server host. Endpoint data is stored separately from credentials: preserve UUID, SubID, SubToken, password, SNI, public key, short ID, fingerprint and flow byte-for-byte. Each affected customer batch CAS-updates the endpoint reference, increments subscription generation once, rewrites absolute desired state/outbox and invalidates the prior verified snapshot; `(operation_id,batch_index,digest)` makes crash resume exact and a repeat a no-op.

Before apex DNS is eligible, the final digest must prove that newly generated subscriptions contain zero `wapmixx.ru:443` VLESS endpoints and that every still-installed legacy client has either consumed the migrated subscription or already has an independently reachable S2/S3/S4 fallback. This task prepares repository logic and fixtures only; it does not change production DNS or customer data.

- [ ] **Step 4: Replace in-memory HA panel security and expose owner operations only in rqlite mode**

Use Task 5 principals/sessions, CSRF, revocation epoch and explicit permission checks on every server-side action. Register and contract-test `${panelBase}api/orders`, `api/order/confirm`, `api/order/cancel`, `api/cluster-status` and `api/audit`; only `owner` can confirm/cancel payments, while delegated `admin` is read/provision-only. The owner UI must list pending/payment-claimed orders and allow idempotent confirm/cancel without exposing SubToken or a private URL. Its redacted status view includes quorum/leader and read/write readiness, replication/apply lag, outbox depth/age, node receipts/fences, both bot poller/inbox/delivery states, DNS/TLS target and probe result, last verified backup plus current RPO/RTO drill, and recent audit failures. Add per-actor and per-IP bounded cluster rate limits and tests for permissions, CSRF, pagination, empty/error states and secret-free HTML/JSON/log output. Legacy panel mode remains unchanged until cutover.

- [ ] **Step 5: Add health routes and stale read policy**

Keep `/healthz`; add `/livez`, `/readyz/read`, `/readyz/write`. The no-quorum matrix is exact:

| Request | Result during global no-quorum |
|---|---|
| `/healthz`, `/livez` | 200 process liveness |
| `/readyz/read`, `/readyz/write` | 503 |
| known committed device `/sub` with verified snapshot age <= 60m | last committed response |
| cached expired `/sub/info` | 200 with `active=false` |
| cached expired base `/sub`, helpers or links | existing 402 |
| unknown token, empty cache or uncommitted new device | 503, never a false 404/admission |
| `/claim`, every `/order*`, `/trial`, admin/panel mutations | 503 |
| `/report` | existing best-effort 204 |
| HA `/update` without strong-approved OTA catalog | 503 |

The in-memory cache stores a `SubscriptionSnapshot`, settings version, committed device HMAC and verification time only after a successful strong transaction read. A process restart begins empty and fails closed. Snapshot age over 60 minutes, an unverified snapshot or a token revoked/deleted at verification time returns 503. A strongly verified expired snapshot is cached only to serve the exact expired `200 /info` and `402` semantics in the table above.

- [ ] **Step 6: Verify OTA metadata without publishing**

Add API tests that compare the approved cluster manifest fields `versionCode`, `versionName`, APK `size`, `sha256` with injected GitHub/Yandex fixture manifests byte-for-byte. A difference makes read readiness red and the panel shows a redacted mismatch. No Release upload, mirror sync or OTA manifest mutation is part of this task.

- [ ] **Step 7: Run backend tests and GitHub Android unit task**

Run: `cd backend && go test ./internal/suburl ./internal/api ./internal/controlplane -run 'Test.*' -count=1`

In `ha-control-plane.yml`, pin every setup action by full commit SHA, install the declared JDK/Android SDK, and run the exact existing Gradle unit target with `MaestroSubTest`. Libbox is an explicit workflow input containing source workflow run ID, artifact ID, artifact ZIP SHA-256 and embedded libbox digest; download that exact artifact, verify both digests against the committed build manifest, and reject `latest` or "latest successful" lookup. Verify no `TvEskizHome.kt`, `TvEskizSpec.kt` or `tvm_*` diff. Do not run `android.yml` and do not publish artifacts as Release/OTA.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/suburl backend/internal/controlplane/endpoint_migration.go backend/internal/controlplane/endpoint_migration_test.go backend/internal/api app/src/main/java/com/maestrovpn/tv/utils/MaestroSub.kt app/src/test/java/com/maestrovpn/tv/utils/MaestroSubTest.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/IosKaringDialog.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/IosKaringLinkTest.kt
git commit -m "fix(api): canonicalize subscription URL variants"
```

## Plan 02 acceptance

- 100 identical confirms produce one payment, one expiry increment and one generation; different hashes conflict.
- Two different paid orders both extend the current expiry; confirm/cancel has one terminal winner.
- An unclaimed order expires after 24 hours, while `payment_claimed` remains owner-decidable; a stale sweeper fence cannot revoke or expire anything.
- Full-plus-delta import resumes each batch once and equals a fresh final-full business digest, including explicit deletes, settings, principals, bot offsets and callbacks.
- S1 endpoint migration changes only the server host, preserves every identity/credential byte, invalidates cached generations once and gates apex on zero new apex:443 VLESS plus independent HA fallback.
- The exhaustive current route/action snapshot, distinct extend/renew behavior, owner order actions and redacted HA dashboard pass; rqlite bulk-import is 410 and no HA path reaches legacy stores/shell/SSH.
- HA Android unit CI uses pinned actions and one exact digest-verified libbox artifact, never a mutable latest-success lookup.
- Legacy API golden tests pass in both adapters; rqlite errors never fall back to JSON writes.
- Trial, settings, sessions and audit are cluster-backed in HA mode.
- Maestro URLs are query-free; Karing uses exactly `?format=links`; private URLs are absent from owner views/logs.
- No-quorum mutations are 503 and `/sub` stale grace is bounded to 60 minutes.
- Importer/shadow tests are deterministic, but production import remains blocked until exact live bot sources/schema and fence evidence exist.
- No TV UI/assets, Release, OTA, DNS, service or production data was changed.
